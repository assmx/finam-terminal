package api

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"finam-terminal/models"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Loader tuning. All of these are package variables rather than constants so
// tests can shorten a pass; the values come from the track reconnaissance
// (2026-09-04) and its reasoning is recorded in the track spec.
var (
	// historyChunk is the width of one request window. The only interval cap
	// ever measured on this API is 366 days on daily Bars; 92 days sits well
	// under it and survives a tighter cap on Trades/Transactions if there is
	// one. A ten-year account costs 40 chunks per method.
	historyChunk = 92 * 24 * time.Hour

	// historyMinChunk is where splitting stops. A single day that still comes
	// back full is accepted as it is: halving hours would multiply requests for
	// a case no real account produces.
	historyMinChunk = 24 * time.Hour

	// historyLimit is the record cap asked for on every request. Its exact
	// maximum could not be measured, which does not matter — the split on a
	// full response makes correctness independent of the value.
	historyLimit int32 = 1000

	// historyPace separates consecutive requests. The broker refuses a burst
	// rather than a volume; the same 150ms proved itself on the Index tab.
	historyPace = 150 * time.Millisecond

	// historyMaxRequests bounds one pass. Twice the 40 chunks a ten-year
	// account needs per method.
	historyMaxRequests = 80

	// historyQuotaReserve is how much of the minute quota a pass leaves for the
	// rest of the terminal. 10% of the observed limit of 200 — and refused
	// requests spend the quota too, so the margin is not theoretical.
	historyQuotaReserve int64 = 20

	// historyQuotaProbe is the estimate above which a pass spends one request
	// checking the quota first. Below it the check would cost more than it saves.
	historyQuotaProbe = 5
)

// StopReason says why a pass ended early, or that it did not.
type StopReason int

const (
	// StopNone means the pass covered its whole window.
	StopNone StopReason = iota
	// StopGuard means historyMaxRequests was reached.
	StopGuard
	// StopRateLimited means the API answered ResourceExhausted.
	StopRateLimited
	// StopError means an ordinary failure ended the pass.
	StopError
	// StopQuota means the pre-check refused to start the pass.
	StopQuota
)

// String renders the reason for logs and tests.
func (r StopReason) String() string {
	switch r {
	case StopNone:
		return "none"
	case StopGuard:
		return "guard"
	case StopRateLimited:
		return "rate limited"
	case StopError:
		return "error"
	case StopQuota:
		return "quota"
	default:
		return fmt.Sprintf("StopReason(%d)", int(r))
	}
}

// HistoryRequest describes one pass over an account's history.
//
// A zero TradesFrom or TransactionsFrom means that method is not loaded at all,
// which is how an account with no trades avoids paying for a walk that can only
// return nothing.
type HistoryRequest struct {
	AccountID        string
	TradesFrom       time.Time
	TransactionsFrom time.Time
	To               time.Time
}

// HistoryProgress is reported after every finished window so a screen can say
// how far the load has got.
//
// Done and Total count windows — one chunk of one method — rather than
// requests. A window that has to be split costs more requests but stays one
// step, so Done never runs past Total. Total is known before the first request
// and capped by the request guard, because a longer pass stops there: an
// unsplit pass that reaches the guard ends exactly on its total.
type HistoryProgress struct {
	Done  int
	Total int
}

// HistoryBundle is everything one pass produced.
//
// Boundary is the point from which the history is complete: for a pass that
// finished it is the earliest date asked for, and for one that stopped it is
// where it stopped. Partial data is always kept — a screen showing three years
// of a ten-year account with a note saying so is worth more than an error.
type HistoryBundle struct {
	Trades       []models.Trade
	Transactions []models.Transaction
	Boundary     time.Time
	Complete     bool
	Stop         StopReason
	StopErr      error
	Requests     int
}

// methodPass is the per-method state of one walk.
type methodPass struct {
	name     string // the API method, for quota matching and logs
	from     time.Time
	boundary time.Time
	done     bool
}

// LoadHistory walks an account's history backwards from To in chunks and
// returns everything it managed to load.
//
// It never retries. A refusal, a failure or the request guard ends the pass and
// the partial result is returned with the reason; only a pass that loaded
// nothing at all reports an error. The caller decides whether to ask again —
// which, on a rate limit, is the one thing that must not happen automatically.
func (c *Client) LoadHistory(ctx context.Context, req HistoryRequest, progress func(HistoryProgress)) (*HistoryBundle, error) {
	started := time.Now()

	to := req.To
	if to.IsZero() {
		to = time.Now()
	}

	bundle := &HistoryBundle{Stop: StopNone}

	passes := make([]*methodPass, 0, 2)
	if !req.TradesFrom.IsZero() && req.TradesFrom.Before(to) {
		passes = append(passes, &methodPass{name: "Trades", from: req.TradesFrom, boundary: to})
	}
	if !req.TransactionsFrom.IsZero() && req.TransactionsFrom.Before(to) {
		passes = append(passes, &methodPass{name: "Transactions", from: req.TransactionsFrom, boundary: to})
	}
	if len(passes) == 0 {
		bundle.Boundary = to
		bundle.Complete = true
		return bundle, nil
	}

	estimated := 0
	for _, p := range passes {
		estimated += estimateRequests(p.from, to)
	}

	if reason, err := c.checkHistoryQuota(passes, to); reason != StopNone {
		bundle.Stop = reason
		bundle.StopErr = err
		bundle.Boundary = to
		log.Printf("[WARN] History load for %s not started: %v", req.AccountID, err)
		return bundle, nil
	}

	w := &historyWalk{
		client:    c,
		ctx:       ctx,
		accountID: req.AccountID,
		total:     min(estimated, historyMaxRequests),
		progress:  progress,
		bundle:    bundle,
	}

	// Trades first: the screen that triggers the load leads with them.
	for _, p := range passes {
		w.run(p, to)
		if w.stopped() {
			break
		}
	}

	bundle.Requests = w.requests
	bundle.Boundary = combinedBoundary(passes)

	// Windows are disjoint and a re-requested chunk drops its old records, so a
	// duplicate should not arise. This is the belt for the braces: a record the
	// API sends without a timestamp cannot be located in any window, so
	// dropRange cannot remove it, and splitting that chunk would deliver it
	// twice. FIFO matching would count it twice with it.
	bundle.Trades = dedupeByID(bundle.Trades, func(t models.Trade) string { return t.ID })
	bundle.Transactions = dedupeByID(bundle.Transactions, func(t models.Transaction) string { return t.ID })
	bundle.Complete = bundle.Stop == StopNone && allDone(passes) && !w.incomplete

	log.Printf("[INFO] History loaded for %s: %d request(s), %d trade(s), %d transaction(s), "+
		"largest response %d, from %s, complete=%t, stop=%s, took %s",
		req.AccountID, bundle.Requests, len(bundle.Trades), len(bundle.Transactions),
		w.largestResponse, bundle.Boundary.Format("2006-01-02"), bundle.Complete,
		bundle.Stop, time.Since(started).Round(time.Millisecond))

	// Nothing loaded and something went wrong: there is no partial result to
	// show, so the failure is the answer.
	if bundle.Stop != StopNone && bundle.Requests > 0 &&
		len(bundle.Trades) == 0 && len(bundle.Transactions) == 0 && bundle.StopErr != nil {
		return bundle, bundle.StopErr
	}
	return bundle, nil
}

// historyWalk carries the mutable state of one pass.
type historyWalk struct {
	client    *Client
	ctx       context.Context
	accountID string
	progress  func(HistoryProgress)
	bundle    *HistoryBundle

	// total is the number of windows the pass can take; windows is how many
	// it has finished.
	total   int
	windows int

	requests        int
	largestResponse int
	paced           bool

	// incomplete is set when a window could not be recovered even after
	// splitting down to the smallest chunk.
	incomplete bool
}

func (w *historyWalk) stopped() bool { return w.bundle.Stop != StopNone }

// run walks one method backwards from to down to p.from.
//
// Every window starts a whole number of chunks before to, so the walk takes
// exactly estimateRequests(p.from, to) windows — the total the progress is
// measured against. Stepping from the previous start instead drifted by an
// instant per window, and a span a few nanoseconds past a whole number of
// chunks then took one window fewer than its total and skipped p.from itself.
func (w *historyWalk) run(p *methodPass, to time.Time) {
	start, end := to, to
	for {
		if w.stopped() {
			return
		}
		start = start.Add(-historyChunk)
		last := !start.After(p.from)
		if last {
			start = p.from
		}
		if !w.fetchWindow(p, start, end) {
			return
		}
		p.boundary = start
		w.windows++
		w.report()
		if last {
			break
		}
		// Windows are disjoint: the next one ends where this one starts, minus
		// an instant. Sharing the boundary would fetch a record sitting exactly
		// on it twice.
		end = start.Add(-time.Nanosecond)
	}
	p.done = true
}

// fetchWindow loads [from, to] for one method, splitting the window when the
// answer comes back at the limit. It reports whether the walk may continue.
func (w *historyWalk) fetchWindow(p *methodPass, from, to time.Time) bool {
	if w.requests >= historyMaxRequests {
		w.bundle.Stop = StopGuard
		w.bundle.StopErr = fmt.Errorf("stopped after %d requests", w.requests)
		log.Printf("[WARN] History load for %s hit the request guard of %d; history is loaded from %s",
			w.accountID, historyMaxRequests, from.Format("2006-01-02"))
		return false
	}
	if err := w.ctx.Err(); err != nil {
		w.bundle.Stop = StopError
		w.bundle.StopErr = err
		return false
	}

	w.pace()

	n, truncated, err := w.fetch(p.name, from, to)
	w.requests++
	if err != nil {
		if status.Code(err) == codes.ResourceExhausted {
			w.bundle.Stop = StopRateLimited
		} else {
			w.bundle.Stop = StopError
		}
		w.bundle.StopErr = err
		return false
	}
	if n > w.largestResponse {
		w.largestResponse = n
	}

	if !truncated {
		return true
	}

	// The window was cut. Halve it and ask again for both sides; the records
	// just received are dropped, since the halves cover the same span.
	if to.Sub(from) <= historyMinChunk {
		log.Printf("[WARN] History load for %s: a single day (%s) still returns %d records; "+
			"part of that day is missing", w.accountID, from.Format("2006-01-02"), n)
		w.incomplete = true
		return true
	}

	mid := from.Add(to.Sub(from) / 2)
	w.dropRange(p.name, from, to)
	if !w.fetchWindow(p, mid.Add(time.Nanosecond), to) {
		return false
	}
	return w.fetchWindow(p, from, mid)
}

// fetch performs one request and files the records into the bundle. It returns
// how many records arrived and whether the answer looks truncated.
func (w *historyWalk) fetch(method string, from, to time.Time) (int, bool, error) {
	switch method {
	case "Trades":
		trades, err := w.client.GetTrades(w.accountID, from, to, historyLimit)
		if err != nil {
			return 0, false, err
		}
		w.bundle.Trades = append(w.bundle.Trades, trades...)
		return len(trades), historyLimit > 0 && len(trades) >= int(historyLimit), nil
	default:
		txs, err := w.client.GetTransactions(w.accountID, from, to, historyLimit)
		if err != nil {
			return 0, false, err
		}
		w.bundle.Transactions = append(w.bundle.Transactions, txs...)
		return len(txs), historyLimit > 0 && len(txs) >= int(historyLimit), nil
	}
}

// dropRange removes the records of a window that is about to be re-requested in
// halves, so splitting cannot duplicate what it recovers.
func (w *historyWalk) dropRange(method string, from, to time.Time) {
	inWindow := func(ts time.Time) bool {
		return !ts.Before(from) && !ts.After(to)
	}
	if method == "Trades" {
		kept := w.bundle.Trades[:0]
		for _, t := range w.bundle.Trades {
			if !inWindow(t.Timestamp) {
				kept = append(kept, t)
			}
		}
		w.bundle.Trades = kept
		return
	}
	kept := w.bundle.Transactions[:0]
	for _, t := range w.bundle.Transactions {
		if !inWindow(t.Timestamp) {
			kept = append(kept, t)
		}
	}
	w.bundle.Transactions = kept
}

// pace waits between requests. The first request of a pass is not delayed.
func (w *historyWalk) pace() {
	if !w.paced {
		w.paced = true
		return
	}
	if historyPace <= 0 {
		return
	}
	select {
	case <-w.ctx.Done():
	case <-time.After(historyPace):
	}
}

func (w *historyWalk) report() {
	if w.progress == nil {
		return
	}
	w.progress(HistoryProgress{Done: w.windows, Total: w.total})
}

// checkHistoryQuota asks whether the remaining quota can cover the pass.
//
// It costs one request and is skipped for a short pass, where the check would
// cost more than it saves. Anything unclear — the lookup failed, no quota
// matches the method — lets the pass proceed: refusing to load because the
// diagnostic could not run would turn a safety net into an outage.
func (c *Client) checkHistoryQuota(passes []*methodPass, to time.Time) (StopReason, error) {
	needed := make(map[string]int, len(passes))
	probe := false
	for _, p := range passes {
		n := estimateRequests(p.from, to)
		needed[p.name] = n
		if n > historyQuotaProbe {
			probe = true
		}
	}
	if !probe {
		return StopNone, nil
	}

	quotas, err := c.GetUsageMetrics()
	if err != nil {
		log.Printf("[WARN] History quota pre-check unavailable, proceeding: %v", err)
		return StopNone, nil
	}

	for _, p := range passes {
		want := int64(needed[p.name]) + historyQuotaReserve
		for _, q := range quotas {
			if !quotaMatchesMethod(q.Name, p.name) {
				continue
			}
			if q.Remaining < want {
				return StopQuota, fmt.Errorf("quota %s has %d request(s) left, the pass needs %d plus a reserve of %d%s",
					q.Name, q.Remaining, needed[p.name], historyQuotaReserve, resetSuffix(q.ResetAt))
			}
			break
		}
	}
	return StopNone, nil
}

// resetSuffix renders the reset time when the API reported one. A quota
// untouched in the current window carries none, which is the normal case.
func resetSuffix(reset time.Time) string {
	if reset.IsZero() {
		return ""
	}
	return ", resets at " + reset.Local().Format("15:04:05")
}

// quotaMatchesMethod reports whether a quota name refers to the given API
// method.
//
// The match is on the dot-qualified suffix, case-insensitively: quota names
// arrive as "AccountsService.trades" in lower camelCase. Matching the bare
// method name would also catch OrdersService.subscribeTrades and
// MarketDataService.latestTrades, which are entirely different quotas.
func quotaMatchesMethod(quotaName, method string) bool {
	if quotaName == "" || method == "" {
		return false
	}
	return strings.HasSuffix(strings.ToLower(quotaName), "."+strings.ToLower(method))
}

// estimateRequests is how many chunks a window takes — exactly the number of
// windows run walks it in.
//
// The division is in integers. A chunk is some 8·10^15 nanoseconds, so a span
// of two chunks is already past the 2^53 a float64 holds exactly, and the
// nanosecond that costs the walk one more window was rounded away.
func estimateRequests(from, to time.Time) int {
	span := to.Sub(from)
	if span <= 0 {
		return 0
	}
	n := span / historyChunk
	if span%historyChunk != 0 {
		n++
	}
	return int(n)
}

// combinedBoundary is the point from which every requested method is complete —
// the latest of their individual boundaries, since history is only as deep as
// its shallowest source.
func combinedBoundary(passes []*methodPass) time.Time {
	var boundary time.Time
	for _, p := range passes {
		if boundary.IsZero() || p.boundary.After(boundary) {
			boundary = p.boundary
		}
	}
	return boundary
}

func allDone(passes []*methodPass) bool {
	for _, p := range passes {
		if !p.done {
			return false
		}
	}
	return true
}

// dedupeByID drops repeated records, keeping the first of each id and the
// original order.
//
// A record with an empty id is always kept: the API is not known to omit ids,
// but if it did, collapsing every such record into one would silently delete
// real trades — far worse than keeping a duplicate.
func dedupeByID[T any](items []T, id func(T) string) []T {
	if len(items) < 2 {
		return items
	}
	seen := make(map[string]struct{}, len(items))
	out := items[:0]
	for _, it := range items {
		key := id(it)
		if key != "" {
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
		}
		out = append(out, it)
	}
	return out
}
