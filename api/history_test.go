package api

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	tradeapiv1 "github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1"
	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/accounts"
	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/metrics"
	"google.golang.org/genproto/googleapis/type/decimal"
	"google.golang.org/genproto/googleapis/type/interval"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// historyRecorder answers Trades and Transactions from a generator keyed by the
// requested window, and remembers every window it was asked for.
type historyRecorder struct {
	mu sync.Mutex

	// tradesIn / txsIn return the records that fall in [from, to].
	tradesIn func(from, to time.Time, limit int32) []*tradeapiv1.AccountTrade
	txsIn    func(from, to time.Time, limit int32) []*accounts.Transaction

	tradesErr func(call int) error
	txsErr    func(call int) error

	tradeWindows []window
	txWindows    []window
}

type window struct {
	from, to time.Time
	limit    int32
}

func (h *historyRecorder) trades(_ context.Context, in *accounts.TradesRequest, _ ...grpc.CallOption) (*accounts.TradesResponse, error) {
	h.mu.Lock()
	call := len(h.tradeWindows)
	h.tradeWindows = append(h.tradeWindows, windowOf(in.GetInterval(), in.GetLimit()))
	h.mu.Unlock()

	if h.tradesErr != nil {
		if err := h.tradesErr(call); err != nil {
			return nil, err
		}
	}
	if h.tradesIn == nil {
		return &accounts.TradesResponse{}, nil
	}
	w := windowOf(in.GetInterval(), in.GetLimit())
	return &accounts.TradesResponse{Trades: h.tradesIn(w.from, w.to, in.GetLimit())}, nil
}

func (h *historyRecorder) transactions(_ context.Context, in *accounts.TransactionsRequest, _ ...grpc.CallOption) (*accounts.TransactionsResponse, error) {
	h.mu.Lock()
	call := len(h.txWindows)
	h.txWindows = append(h.txWindows, windowOf(in.GetInterval(), in.GetLimit()))
	h.mu.Unlock()

	if h.txsErr != nil {
		if err := h.txsErr(call); err != nil {
			return nil, err
		}
	}
	if h.txsIn == nil {
		return &accounts.TransactionsResponse{}, nil
	}
	w := windowOf(in.GetInterval(), in.GetLimit())
	return &accounts.TransactionsResponse{Transactions: h.txsIn(w.from, w.to, in.GetLimit())}, nil
}

func windowOf(iv *interval.Interval, limit int32) window {
	return window{from: iv.GetStartTime().AsTime(), to: iv.GetEndTime().AsTime(), limit: limit}
}

func (h *historyRecorder) counts() (int, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.tradeWindows), len(h.txWindows)
}

// historyClient wires a recorder into a Client, with an optional quota answer.
func historyClient(rec *historyRecorder, quotas []*metrics.GetUsageMetricsResponse_QuotaUsage, quotaErr error) *Client {
	c := &Client{
		accountsClient: &mockAccountsServiceClient{
			TradesFunc:       rec.trades,
			TransactionsFunc: rec.transactions,
		},
		assetMicCache:       map[string]string{},
		assetLotCache:       map[string]float64{},
		tradeLotCache:       map[string]float64{},
		instrumentNameCache: map[string]string{},
	}
	c.usageMetricsClient = &mockUsageMetricsServiceClient{
		GetUsageMetricsFunc: func(_ context.Context, _ *metrics.GetUsageMetricsRequest, _ ...grpc.CallOption) (*metrics.GetUsageMetricsResponse, error) {
			if quotaErr != nil {
				return nil, quotaErr
			}
			return &metrics.GetUsageMetricsResponse{Quotas: quotas}, nil
		},
	}
	return c
}

// noPace zeroes the inter-request pause for the duration of a test.
func noPace(t *testing.T) {
	t.Helper()
	prev := historyPace
	historyPace = 0
	t.Cleanup(func() { historyPace = prev })
}

func tradeAt(id string, ts time.Time) *tradeapiv1.AccountTrade {
	return &tradeapiv1.AccountTrade{
		TradeId:   id,
		Symbol:    "SBER@MISX",
		Side:      tradeapiv1.Side_SIDE_BUY,
		Size:      &decimal.Decimal{Value: "1"},
		Price:     &decimal.Decimal{Value: "100"},
		Timestamp: timestamppb.New(ts),
	}
}

func txAt(id string, ts time.Time) *accounts.Transaction {
	return &accounts.Transaction{
		Id:                  id,
		TransactionCategory: accounts.Transaction_DEPOSIT,
		Timestamp:           timestamppb.New(ts),
	}
}

// TestLoadHistory_WalksInChunks checks the shape of the walk: backwards from
// To, one chunk at a time, never past From, with the limit on every request.
func TestLoadHistory_WalksInChunks(t *testing.T) {
	noPace(t)

	to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	from := to.Add(-3 * historyChunk) // exactly three chunks

	rec := &historyRecorder{}
	client := historyClient(rec, nil, nil)

	bundle, err := client.LoadHistory(context.Background(), HistoryRequest{
		AccountID:  "ACC001",
		TradesFrom: from,
		To:         to,
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tradeCalls, txCalls := rec.counts()
	if tradeCalls != 3 {
		t.Errorf("Trades called %d times, want 3 chunks", tradeCalls)
	}
	if txCalls != 0 {
		t.Errorf("Transactions called %d times, want 0 when TransactionsFrom is zero", txCalls)
	}

	// Newest chunk first, and the windows are disjoint and contiguous: a
	// record sitting exactly on a chunk boundary must be fetched once, not
	// twice, and must not fall between two chunks either.
	for i, w := range rec.tradeWindows {
		if w.limit != historyLimit {
			t.Errorf("chunk %d limit = %d, want %d", i, w.limit, historyLimit)
		}
		if i == 0 {
			if !w.to.Equal(to) {
				t.Errorf("first chunk ends %v, want %v", w.to, to)
			}
			continue
		}
		prev := rec.tradeWindows[i-1]
		if !w.to.Equal(prev.from.Add(-time.Nanosecond)) {
			t.Errorf("chunk %d ends %v, want one instant before the previous start %v", i, w.to, prev.from)
		}
	}
	oldest := rec.tradeWindows[len(rec.tradeWindows)-1]
	if oldest.from.Before(from) {
		t.Errorf("oldest chunk starts %v, before the requested %v", oldest.from, from)
	}

	if !bundle.Complete {
		t.Error("Complete = false, want true for an uninterrupted pass")
	}
	if bundle.Stop != StopNone {
		t.Errorf("Stop = %v, want StopNone", bundle.Stop)
	}
	if !bundle.Boundary.Equal(from) {
		t.Errorf("Boundary = %v, want %v", bundle.Boundary, from)
	}
	if bundle.Requests != 3 {
		t.Errorf("Requests = %d, want 3", bundle.Requests)
	}
}

// TestLoadHistory_BothMethods loads trades first, then transactions, and counts
// both in the request total.
func TestLoadHistory_BothMethods(t *testing.T) {
	noPace(t)

	to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	rec := &historyRecorder{
		tradesIn: func(from, _ time.Time, _ int32) []*tradeapiv1.AccountTrade {
			return []*tradeapiv1.AccountTrade{tradeAt("T"+from.Format("0102"), from.Add(time.Hour))}
		},
		txsIn: func(from, _ time.Time, _ int32) []*accounts.Transaction {
			return []*accounts.Transaction{txAt("X"+from.Format("0102"), from.Add(time.Hour))}
		},
	}
	client := historyClient(rec, nil, nil)

	bundle, err := client.LoadHistory(context.Background(), HistoryRequest{
		AccountID:        "ACC001",
		TradesFrom:       to.Add(-2 * historyChunk),
		TransactionsFrom: to.Add(-historyChunk),
		To:               to,
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tradeCalls, txCalls := rec.counts()
	if tradeCalls != 2 || txCalls != 1 {
		t.Errorf("calls = %d trades / %d transactions, want 2/1", tradeCalls, txCalls)
	}
	if bundle.Requests != 3 {
		t.Errorf("Requests = %d, want 3 (both methods counted)", bundle.Requests)
	}
	if len(bundle.Trades) != 2 || len(bundle.Transactions) != 1 {
		t.Errorf("records = %d trades / %d transactions, want 2/1", len(bundle.Trades), len(bundle.Transactions))
	}

	// The boundary is the point from which BOTH methods are complete, which is
	// the later of the two starts.
	want := to.Add(-historyChunk)
	if !bundle.Boundary.Equal(want) {
		t.Errorf("Boundary = %v, want %v (the later of the two windows)", bundle.Boundary, want)
	}
}

// TestLoadHistory_SplitsTruncatedChunk is the core of the loader: a response of
// exactly limit records means the window was cut, so the chunk is halved and
// asked again until each half comes back short.
func TestLoadHistory_SplitsTruncatedChunk(t *testing.T) {
	noPace(t)
	prev := historyLimit
	historyLimit = 4
	t.Cleanup(func() { historyLimit = prev })

	to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	from := to.Add(-historyChunk)

	// The full chunk holds 8 records; any half holds 4 or fewer. So the first
	// request comes back truncated and each half comes back short.
	all := make([]*tradeapiv1.AccountTrade, 0, 8)
	for i := range 8 {
		all = append(all, tradeAt(fmt.Sprintf("T%d", i), from.Add(time.Duration(i+1)*historyChunk/9)))
	}

	rec := &historyRecorder{
		tradesIn: func(wf, wt time.Time, limit int32) []*tradeapiv1.AccountTrade {
			var in []*tradeapiv1.AccountTrade
			for _, tr := range all {
				ts := tr.Timestamp.AsTime()
				if !ts.Before(wf) && !ts.After(wt) {
					in = append(in, tr)
				}
			}
			if limit > 0 && len(in) > int(limit) {
				in = in[len(in)-int(limit):] // truncation keeps the newest
			}
			return in
		},
	}
	client := historyClient(rec, nil, nil)

	bundle, err := client.LoadHistory(context.Background(), HistoryRequest{
		AccountID:  "ACC001",
		TradesFrom: from,
		To:         to,
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls, _ := rec.counts()
	if calls < 3 {
		t.Errorf("Trades called %d times, want at least 3 (one truncated chunk plus two halves)", calls)
	}

	if len(bundle.Trades) != 8 {
		t.Errorf("got %d trades, want all 8 — splitting must recover what truncation hid", len(bundle.Trades))
	}
	seen := map[string]int{}
	for _, tr := range bundle.Trades {
		seen[tr.ID]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("trade %s appears %d times; a re-requested half must not duplicate records", id, n)
		}
	}
	if !bundle.Complete {
		t.Error("Complete = false, want true — splitting resolved the truncation")
	}
}

// TestLoadHistory_TruncatedSingleDayAccepted stops splitting at one day. A day
// that still comes back full is taken as it is; halving hours would multiply
// requests for a case no real account produces.
func TestLoadHistory_TruncatedSingleDayAccepted(t *testing.T) {
	noPace(t)
	prevLimit, prevChunk := historyLimit, historyChunk
	historyLimit, historyChunk = 2, 2*24*time.Hour
	t.Cleanup(func() { historyLimit, historyChunk = prevLimit, prevChunk })

	to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	from := to.Add(-historyChunk)

	// Every window, however narrow, answers with exactly the limit.
	rec := &historyRecorder{
		tradesIn: func(wf, _ time.Time, limit int32) []*tradeapiv1.AccountTrade {
			out := make([]*tradeapiv1.AccountTrade, 0, limit)
			for i := range int(limit) {
				out = append(out, tradeAt(fmt.Sprintf("%s-%d", wf.Format("0102150405"), i), wf.Add(time.Minute)))
			}
			return out
		},
	}
	client := historyClient(rec, nil, nil)

	done := make(chan struct{})
	var bundle *HistoryBundle
	var err error
	go func() {
		bundle, err = client.LoadHistory(context.Background(), HistoryRequest{
			AccountID:  "ACC001",
			TradesFrom: from,
			To:         to,
		}, nil)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("LoadHistory did not finish: splitting is not bottoming out at one day")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls, _ := rec.counts()
	if calls > historyMaxRequests {
		t.Errorf("Trades called %d times, more than the guard of %d", calls, historyMaxRequests)
	}
	if bundle.Complete {
		t.Error("Complete = true, but a day that stayed truncated means history is missing")
	}
}

// TestLoadHistory_Guard stops a runaway pass and keeps what it has.
func TestLoadHistory_Guard(t *testing.T) {
	noPace(t)
	prev := historyMaxRequests
	historyMaxRequests = 5
	t.Cleanup(func() { historyMaxRequests = prev })

	to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	from := to.Add(-100 * historyChunk) // far more chunks than the guard allows

	rec := &historyRecorder{
		tradesIn: func(wf, _ time.Time, _ int32) []*tradeapiv1.AccountTrade {
			return []*tradeapiv1.AccountTrade{tradeAt(wf.Format("20060102"), wf.Add(time.Hour))}
		},
	}
	client := historyClient(rec, nil, nil)

	bundle, err := client.LoadHistory(context.Background(), HistoryRequest{
		AccountID:  "ACC001",
		TradesFrom: from,
		To:         to,
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if bundle.Requests > historyMaxRequests {
		t.Errorf("Requests = %d, want at most the guard of %d", bundle.Requests, historyMaxRequests)
	}
	if bundle.Stop != StopGuard {
		t.Errorf("Stop = %v, want StopGuard", bundle.Stop)
	}
	if bundle.Complete {
		t.Error("Complete = true after hitting the guard")
	}
	if len(bundle.Trades) == 0 {
		t.Error("the partial history was discarded; a stopped pass must keep what it loaded")
	}
	if bundle.Boundary.Before(from) || !bundle.Boundary.Before(to) {
		t.Errorf("Boundary = %v, want a point between %v and %v", bundle.Boundary, from, to)
	}
}

// TestLoadHistory_RateLimited stops at the first refusal and keeps the partial
// result. Retrying into a rate limit is exactly the wrong reflex.
func TestLoadHistory_RateLimited(t *testing.T) {
	noPace(t)

	to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	from := to.Add(-10 * historyChunk)

	rec := &historyRecorder{
		tradesIn: func(wf, _ time.Time, _ int32) []*tradeapiv1.AccountTrade {
			return []*tradeapiv1.AccountTrade{tradeAt(wf.Format("20060102"), wf.Add(time.Hour))}
		},
		tradesErr: func(call int) error {
			if call >= 2 {
				return status.Error(codes.ResourceExhausted, "quota exceeded")
			}
			return nil
		},
	}
	client := historyClient(rec, nil, nil)

	bundle, err := client.LoadHistory(context.Background(), HistoryRequest{
		AccountID:  "ACC001",
		TradesFrom: from,
		To:         to,
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls, _ := rec.counts()
	if calls != 3 {
		t.Errorf("Trades called %d times, want 3 — the pass must stop on the first refusal", calls)
	}
	if bundle.Stop != StopRateLimited {
		t.Errorf("Stop = %v, want StopRateLimited", bundle.Stop)
	}
	if bundle.StopErr == nil || status.Code(bundle.StopErr) != codes.ResourceExhausted {
		t.Errorf("StopErr = %v, want the ResourceExhausted status", bundle.StopErr)
	}
	if len(bundle.Trades) != 2 {
		t.Errorf("got %d trades, want the 2 loaded before the refusal", len(bundle.Trades))
	}
	if bundle.Complete {
		t.Error("Complete = true after a rate limit")
	}
}

// TestLoadHistory_ErrorKeepsPartial treats an ordinary failure the same way: no
// retry, keep what arrived.
func TestLoadHistory_ErrorKeepsPartial(t *testing.T) {
	noPace(t)

	to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	rec := &historyRecorder{
		tradesIn: func(wf, _ time.Time, _ int32) []*tradeapiv1.AccountTrade {
			return []*tradeapiv1.AccountTrade{tradeAt(wf.Format("20060102"), wf.Add(time.Hour))}
		},
		tradesErr: func(call int) error {
			if call >= 1 {
				return errors.New("connection reset")
			}
			return nil
		},
	}
	client := historyClient(rec, nil, nil)

	bundle, err := client.LoadHistory(context.Background(), HistoryRequest{
		AccountID:  "ACC001",
		TradesFrom: to.Add(-5 * historyChunk),
		To:         to,
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bundle.Stop != StopError {
		t.Errorf("Stop = %v, want StopError", bundle.Stop)
	}
	if len(bundle.Trades) != 1 {
		t.Errorf("got %d trades, want the 1 loaded before the failure", len(bundle.Trades))
	}
	if bundle.StopErr == nil {
		t.Error("StopErr = nil, want the underlying failure")
	}
}

// TestLoadHistory_ErrorOnFirstRequest is the one case that reports an error:
// nothing was loaded, so there is no partial result to show.
func TestLoadHistory_ErrorOnFirstRequest(t *testing.T) {
	noPace(t)

	rec := &historyRecorder{
		tradesErr: func(int) error { return errors.New("connection reset") },
	}
	client := historyClient(rec, nil, nil)

	to := time.Now()
	bundle, err := client.LoadHistory(context.Background(), HistoryRequest{
		AccountID:  "ACC001",
		TradesFrom: to.Add(-historyChunk),
		To:         to,
	}, nil)
	if err == nil {
		t.Fatal("expected an error when nothing at all was loaded")
	}
	if bundle != nil && len(bundle.Trades) != 0 {
		t.Errorf("bundle carries %d trades alongside the error", len(bundle.Trades))
	}
}

// TestLoadHistory_ContextCancelled stops the walk when the application shuts
// down.
func TestLoadHistory_ContextCancelled(t *testing.T) {
	noPace(t)

	ctx, cancel := context.WithCancel(context.Background())
	to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	rec := &historyRecorder{}
	rec.tradesIn = func(wf, _ time.Time, _ int32) []*tradeapiv1.AccountTrade {
		if calls, _ := rec.counts(); calls >= 2 {
			cancel()
		}
		return []*tradeapiv1.AccountTrade{tradeAt(wf.Format("20060102"), wf.Add(time.Hour))}
	}
	client := historyClient(rec, nil, nil)

	bundle, err := client.LoadHistory(ctx, HistoryRequest{
		AccountID:  "ACC001",
		TradesFrom: to.Add(-50 * historyChunk),
		To:         to,
	}, nil)
	cancel()

	if err == nil && bundle.Complete {
		t.Error("the pass ran to completion despite a cancelled context")
	}
	calls, _ := rec.counts()
	if calls > 5 {
		t.Errorf("Trades called %d times after cancellation, want the walk to stop promptly", calls)
	}
}

// TestLoadHistory_Progress reports movement to the caller so the screen can say
// how far the load has got.
func TestLoadHistory_Progress(t *testing.T) {
	noPace(t)

	to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	from := to.Add(-4 * historyChunk)

	rec := &historyRecorder{}
	client := historyClient(rec, nil, nil)

	var seen []HistoryProgress
	if _, err := client.LoadHistory(context.Background(), HistoryRequest{
		AccountID:  "ACC001",
		TradesFrom: from,
		To:         to,
	}, func(p HistoryProgress) { seen = append(seen, p) }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(seen) != 4 {
		t.Fatalf("progress reported %d times, want once per request (4)", len(seen))
	}
	for i, p := range seen {
		if p.Requests != i+1 {
			t.Errorf("progress %d: Requests = %d, want %d", i, p.Requests, i+1)
		}
		if p.Estimated < 4 {
			t.Errorf("progress %d: Estimated = %d, want at least the 4 chunks", i, p.Estimated)
		}
	}
	if !seen[len(seen)-1].Boundary.Equal(from) {
		t.Errorf("final progress boundary = %v, want %v", seen[len(seen)-1].Boundary, from)
	}
}

// --- quota pre-check ---

func quota(name string, remaining int64, reset time.Time) *metrics.GetUsageMetricsResponse_QuotaUsage {
	q := &metrics.GetUsageMetricsResponse_QuotaUsage{Name: name, Limit: 200, Remaining: remaining}
	if !reset.IsZero() {
		q.ResetTime = timestamppb.New(reset)
	}
	return q
}

// TestLoadHistory_QuotaBlocks refuses to start a long pass that the remaining
// quota cannot cover.
func TestLoadHistory_QuotaBlocks(t *testing.T) {
	noPace(t)

	to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	reset := to.Add(42 * time.Second)

	rec := &historyRecorder{}
	client := historyClient(rec, []*metrics.GetUsageMetricsResponse_QuotaUsage{
		quota("AccountsService.trades", 21, reset),
		quota("AccountsService.transactions", 200, time.Time{}),
	}, nil)

	bundle, err := client.LoadHistory(context.Background(), HistoryRequest{
		AccountID:  "ACC001",
		TradesFrom: to.Add(-10 * historyChunk), // 10 requests + reserve 20 > 21 remaining
		To:         to,
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if bundle.Stop != StopQuota {
		t.Errorf("Stop = %v, want StopQuota", bundle.Stop)
	}
	if calls, _ := rec.counts(); calls != 0 {
		t.Errorf("Trades called %d times, want 0 — a blocked pass must not start", calls)
	}
	if bundle.StopErr == nil {
		t.Fatal("StopErr = nil, want a message naming the method and the reset time")
	}
	msg := bundle.StopErr.Error()
	for _, want := range []string{"AccountsService.trades", "21"} {
		if !contains(msg, want) {
			t.Errorf("StopErr = %q, want it to mention %q", msg, want)
		}
	}
}

// TestLoadHistory_QuotaAllows starts the pass when the remaining quota covers
// the estimate plus the reserve.
func TestLoadHistory_QuotaAllows(t *testing.T) {
	noPace(t)

	to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	rec := &historyRecorder{}
	client := historyClient(rec, []*metrics.GetUsageMetricsResponse_QuotaUsage{
		quota("AccountsService.trades", 200, time.Time{}),
		quota("AccountsService.transactions", 200, time.Time{}),
	}, nil)

	bundle, err := client.LoadHistory(context.Background(), HistoryRequest{
		AccountID:  "ACC001",
		TradesFrom: to.Add(-10 * historyChunk),
		To:         to,
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bundle.Stop != StopNone {
		t.Errorf("Stop = %v, want StopNone", bundle.Stop)
	}
	if calls, _ := rec.counts(); calls != 10 {
		t.Errorf("Trades called %d times, want 10", calls)
	}
}

// TestLoadHistory_QuotaUnavailableFailsOpen proceeds when the quota lookup
// itself fails. Refusing to load because the check could not run would turn a
// diagnostic into an outage.
func TestLoadHistory_QuotaUnavailableFailsOpen(t *testing.T) {
	noPace(t)

	to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	rec := &historyRecorder{}
	client := historyClient(rec, nil, errors.New("metrics unavailable"))

	bundle, err := client.LoadHistory(context.Background(), HistoryRequest{
		AccountID:  "ACC001",
		TradesFrom: to.Add(-10 * historyChunk),
		To:         to,
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bundle.Stop != StopNone {
		t.Errorf("Stop = %v, want StopNone when the quota check could not run", bundle.Stop)
	}
	if calls, _ := rec.counts(); calls != 10 {
		t.Errorf("Trades called %d times, want the pass to proceed", calls)
	}
}

// TestLoadHistory_QuotaSkippedForShortPass does not spend a request checking
// quotas for a pass of a few chunks.
func TestLoadHistory_QuotaSkippedForShortPass(t *testing.T) {
	noPace(t)

	to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	rec := &historyRecorder{}

	var quotaCalls int
	client := historyClient(rec, nil, nil)
	client.usageMetricsClient = &mockUsageMetricsServiceClient{
		GetUsageMetricsFunc: func(_ context.Context, _ *metrics.GetUsageMetricsRequest, _ ...grpc.CallOption) (*metrics.GetUsageMetricsResponse, error) {
			quotaCalls++
			return &metrics.GetUsageMetricsResponse{}, nil
		},
	}

	if _, err := client.LoadHistory(context.Background(), HistoryRequest{
		AccountID:  "ACC001",
		TradesFrom: to.Add(-2 * historyChunk),
		To:         to,
	}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if quotaCalls != 0 {
		t.Errorf("GetUsageMetrics called %d times for a two-chunk pass, want 0", quotaCalls)
	}
}

// TestQuotaMatchesMethod pins the name matching. Suffix-matching the bare
// method name would also match OrdersService.subscribeTrades and
// MarketDataService.latestTrades, which are different quotas entirely.
func TestQuotaMatchesMethod(t *testing.T) {
	cases := []struct {
		quotaName string
		method    string
		want      bool
	}{
		{"AccountsService.trades", "Trades", true},
		{"AccountsService.transactions", "Transactions", true},
		{"AccountsService.Trades", "Trades", true},
		{"OrdersService.subscribeTrades", "Trades", false},
		{"MarketDataService.latestTrades", "Trades", false},
		{"OrdersService.subscribeOrderTrade", "Trades", false},
		{"AccountsService.getAccount", "Trades", false},
		{"", "Trades", false},
	}
	for _, c := range cases {
		if got := quotaMatchesMethod(c.quotaName, c.method); got != c.want {
			t.Errorf("quotaMatchesMethod(%q, %q) = %v, want %v", c.quotaName, c.method, got, c.want)
		}
	}
}

// TestEstimateRequests turns a window into the number of chunks it takes.
func TestEstimateRequests(t *testing.T) {
	cases := []struct {
		name string
		span time.Duration
		want int
	}{
		{"zero", 0, 0},
		{"negative", -time.Hour, 0},
		{"under one chunk", time.Hour, 1},
		{"exactly one chunk", historyChunk, 1},
		{"one chunk plus an hour", historyChunk + time.Hour, 2},
		{"ten chunks", 10 * historyChunk, 10},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
			if got := estimateRequests(to.Add(-c.span), to); got != c.want {
				t.Errorf("estimateRequests(span %v) = %d, want %d", c.span, got, c.want)
			}
		})
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestDedupeByID keeps the first of each id, preserves order, and never
// collapses records that carry no id at all.
func TestDedupeByID(t *testing.T) {
	type rec struct {
		id, tag string
	}
	in := []rec{
		{"a", "first"},
		{"b", "b"},
		{"a", "second"},
		{"", "blank one"},
		{"", "blank two"},
		{"c", "c"},
	}
	out := dedupeByID(in, func(r rec) string { return r.id })

	want := []string{"first", "b", "blank one", "blank two", "c"}
	if len(out) != len(want) {
		t.Fatalf("got %d records, want %d: %+v", len(out), len(want), out)
	}
	for i, w := range want {
		if out[i].tag != w {
			t.Errorf("position %d = %q, want %q", i, out[i].tag, w)
		}
	}
}

// TestLoadHistory_NoWindowsCostsNothing: an account with no known history start
// makes no request at all rather than walking a decade of nothing.
func TestLoadHistory_NoWindowsCostsNothing(t *testing.T) {
	rec := &historyRecorder{}
	client := historyClient(rec, nil, nil)

	bundle, err := client.LoadHistory(context.Background(), HistoryRequest{
		AccountID: "ACC001",
		To:        time.Now(),
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tradeCalls, txCalls := rec.counts(); tradeCalls != 0 || txCalls != 0 {
		t.Errorf("calls = %d/%d, want none", tradeCalls, txCalls)
	}
	if !bundle.Complete {
		t.Error("Complete = false; a pass with nothing to load is trivially complete")
	}
}

// TestLoadHistory_FromAfterTo makes no request for a window that runs backwards.
// The API rejects such an interval outright, and there is nothing to ask for.
func TestLoadHistory_FromAfterTo(t *testing.T) {
	rec := &historyRecorder{}
	client := historyClient(rec, nil, nil)

	to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if _, err := client.LoadHistory(context.Background(), HistoryRequest{
		AccountID:  "ACC001",
		TradesFrom: to.Add(time.Hour),
		To:         to,
	}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls, _ := rec.counts(); calls != 0 {
		t.Errorf("Trades called %d times for a backwards window, want 0", calls)
	}
}
