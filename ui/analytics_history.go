package ui

import (
	"fmt"
	"log"
	"math"
	"time"

	"finam-terminal/analytics"
	"finam-terminal/api"
	"finam-terminal/models"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/marketdata"
)

// analyticsRefreshCooldown is the shortest gap between two history passes for
// one account. R inside it is refused with a message rather than silently
// ignored, so the key never feels broken.
//
// A var so tests can shorten it.
var analyticsRefreshCooldown = 10 * time.Second

// historyTailWindow is how far back a top-up reaches. A day of overlap is
// enough to catch anything that settled late, and the merge deduplicates what
// arrives twice.
var historyTailWindow = 24 * time.Hour

// benchmarkWindow is the width of each bar window at the ends of the horizon.
//
// Fourteen days rather than seven because the reconnaissance found a seven-day
// window over the Russian New Year holidays returning zero bars: the holidays
// run to the 10th, and an account opened in early January is an ordinary case.
var benchmarkWindow = 14 * 24 * time.Hour

// historyHorizon is the window one full pass covers for an account.
//
// Trades are walked from the first one the broker reports; transactions from
// the earlier of the account's opening date and its first non-trade movement,
// because money arrives before the first purchase. A zero date means that
// method is not walked at all — there is nothing to find.
func historyHorizon(account models.AccountInfo, now time.Time) api.HistoryRequest {
	req := api.HistoryRequest{AccountID: account.ID, To: now}

	req.TradesFrom = account.FirstTradeDate

	switch {
	case !account.FirstNonTradeDate.IsZero() && !account.FirstTradeDate.IsZero():
		req.TransactionsFrom = earlier(account.FirstNonTradeDate, account.FirstTradeDate)
	case !account.FirstNonTradeDate.IsZero():
		req.TransactionsFrom = account.FirstNonTradeDate
	default:
		req.TransactionsFrom = account.FirstTradeDate
	}

	return req
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// ensureHistoryLoaded starts a pass for the active account when its cache is
// empty and nothing is already walking it.
//
// It is called when the Trades or Money screen becomes visible, and nowhere
// else: the five-second tick must never reach for history.
func (a *App) ensureHistoryLoaded() {
	account, ok := a.activeAccount()
	if !ok {
		return
	}

	a.dataMutex.RLock()
	data := a.analytics.byAccount[account.ID]
	loaded := data != nil && (data.history != nil || data.loading || data.historyErr != "")
	a.dataMutex.RUnlock()

	if loaded {
		return
	}
	a.loadAnalyticsHistoryAsync(account, false)
}

// refreshHistoryTail is R on the Trades or Money screen: reload the newest day
// and merge it into what is cached.
//
// With nothing cached it is a first pass instead. Either way the cooldown
// applies, because the expensive case is exactly the one a user would press R
// on twice.
func (a *App) refreshHistoryTail() {
	account, ok := a.activeAccount()
	if !ok {
		return
	}

	a.dataMutex.RLock()
	data := a.analytics.byAccount[account.ID]
	var last time.Time
	inFlight := false
	hasHistory := false
	if data != nil {
		last, inFlight, hasHistory = data.historyAt, data.loading, data.history != nil
	}
	a.dataMutex.RUnlock()

	if inFlight {
		return
	}
	if wait := analyticsRefreshCooldown - time.Since(last); !last.IsZero() && wait > 0 {
		a.setHistoryStatus(fmt.Sprintf("[yellow]подождите %d с до следующего обновления[-]", int(wait.Seconds())+1))
		return
	}

	a.loadAnalyticsHistoryAsync(account, hasHistory)
}

// loadHistoryAsync walks the account off the event loop.
//
// tail asks for the newest day only; a full pass covers the account's whole
// life. Both take the application's context, so shutting down cancels a walk
// in flight rather than leaving it running against a closed screen.
func (a *App) loadAnalyticsHistoryAsync(account models.AccountInfo, tail bool) {
	now := time.Now()
	req := historyHorizon(account, now)
	if tail {
		req.TradesFrom = now.Add(-historyTailWindow)
		req.TransactionsFrom = now.Add(-historyTailWindow)
	}
	if req.TradesFrom.IsZero() && req.TransactionsFrom.IsZero() {
		// Nothing is known about when this account's history starts, so there
		// is no window to walk. The screens say so.
		a.setHistoryStatus(noHistoryStartText)
		return
	}

	a.dataMutex.Lock()
	data := a.analyticsAccountLocked(account.ID)
	if data.loading {
		a.dataMutex.Unlock()
		return
	}
	data.loading = true
	data.historyErr = ""
	data.load = historyLoad{}
	a.dataMutex.Unlock()

	// The bar goes up at once, empty: the first report may be a quota check
	// and a request away.
	updateAnalyticsHistoryScreens(a)

	go func() {
		bundle, err := a.client.LoadHistory(a.ctx, req, func(p api.HistoryProgress) {
			a.dataMutex.Lock()
			data.load.windows, data.load.windowTotal = p.Done, p.Total
			a.dataMutex.Unlock()
			a.queueDraw(func() { updateAnalyticsHistoryScreens(a) })
		})

		benchmark, benchmarkOK := a.loadBenchmark(account.ID, req, bundle, func() {
			a.dataMutex.Lock()
			data.load.bars++
			a.dataMutex.Unlock()
			a.queueDraw(func() { updateAnalyticsHistoryScreens(a) })
		})

		a.dataMutex.Lock()
		data.loading = false
		data.historyAt = time.Now()
		switch {
		case err != nil:
			data.historyErr = historyErrorText(err)
		case bundle != nil:
			data.history = mergeHistory(data.history, bundle)
			data.historyErr = ""
			if benchmarkOK {
				data.benchmark, data.benchmarkOK = benchmark, true
			}
		}
		a.dataMutex.Unlock()

		if err != nil {
			log.Printf("[WARN] Failed to load history for %s: %v", account.ID, err)
		}

		a.queueDraw(func() { updateAnalyticsHistoryScreens(a) })
	}()
}

// mergeHistory folds a pass into what is already cached.
//
// A first pass has nothing to merge into; a top-up overlaps the cache by a day
// and is deduplicated by id. The boundary is the earlier of the two: history is
// complete only as far back as the deepest pass reached.
func mergeHistory(cached, fresh *api.HistoryBundle) *api.HistoryBundle {
	if cached == nil {
		return fresh
	}
	if fresh == nil {
		return cached
	}

	merged := *fresh
	merged.Trades = analytics.MergeTrades(cached.Trades, fresh.Trades)
	merged.Transactions = analytics.MergeTransactions(cached.Transactions, fresh.Transactions)
	merged.Boundary = earlier(cached.Boundary, fresh.Boundary)
	merged.Complete = cached.Complete && fresh.Complete
	if fresh.Stop == api.StopNone {
		merged.Stop, merged.StopErr = cached.Stop, cached.StopErr
	}
	return &merged
}

// loadBenchmark fetches the index bars at both ends of the horizon.
//
// Two narrow windows rather than one long request: the daily timeframe refuses
// an interval wider than 366 days and the horizon is routinely longer. A
// failure here is not fatal — the index comparison is a nicety, and losing it
// must not cost the history that was just walked.
//
// answered runs after each bar request, failed or not: it is a step of the
// pass that took real time, and the progress bar counts it.
func (a *App) loadBenchmark(accountID string, req api.HistoryRequest, bundle *api.HistoryBundle, answered func()) (analytics.Benchmark, bool) {
	from := req.TradesFrom
	if from.IsZero() || (!req.TransactionsFrom.IsZero() && req.TransactionsFrom.Before(from)) {
		from = req.TransactionsFrom
	}
	if bundle != nil && !bundle.Boundary.IsZero() && bundle.Boundary.After(from) {
		// History only reaches the boundary, so the comparison starts there
		// too. Comparing the account's partial result against the index's full
		// one would flatter whichever ran better early.
		from = bundle.Boundary
	}
	if from.IsZero() || !from.Before(req.To) {
		return analytics.Benchmark{}, false
	}

	symbol := indexList[0].Symbol

	first, err := a.client.GetBars(accountID, symbol, marketdata.TimeFrame_TIME_FRAME_D, from, from.Add(benchmarkWindow))
	answered()
	if err != nil {
		log.Printf("[WARN] Benchmark bars unavailable for the start of the horizon: %v", err)
		return analytics.Benchmark{}, false
	}

	last, err := a.client.GetBars(accountID, symbol, marketdata.TimeFrame_TIME_FRAME_D, req.To.Add(-benchmarkWindow), req.To)
	answered()
	if err != nil {
		log.Printf("[WARN] Benchmark bars unavailable for the end of the horizon: %v", err)
		return analytics.Benchmark{}, false
	}

	return analytics.BenchmarkFromBars(first, last, from, req.To)
}

// activeAccount is the account the screens are showing.
func (a *App) activeAccount() (models.AccountInfo, bool) {
	a.dataMutex.RLock()
	defer a.dataMutex.RUnlock()

	if a.selectedIdx < 0 || a.selectedIdx >= len(a.accounts) {
		return models.AccountInfo{}, false
	}
	return a.accounts[a.selectedIdx], true
}

// analyticsAccountLocked returns the per-account slot, creating it on first
// use. The caller holds dataMutex for writing.
func (a *App) analyticsAccountLocked(accountID string) *analyticsAccountData {
	data, ok := a.analytics.byAccount[accountID]
	if !ok {
		data = &analyticsAccountData{}
		a.analytics.byAccount[accountID] = data
	}
	return data
}

// analyticsAccountSnapshot copies what the renderers need for the active
// account, so drawing never holds the lock.
func (a *App) analyticsAccountSnapshot() (models.AccountInfo, analyticsAccountData, bool) {
	a.dataMutex.RLock()
	defer a.dataMutex.RUnlock()

	if a.selectedIdx < 0 || a.selectedIdx >= len(a.accounts) {
		return models.AccountInfo{}, analyticsAccountData{}, false
	}
	account := a.accounts[a.selectedIdx]
	data := a.analytics.byAccount[account.ID]
	if data == nil {
		return account, analyticsAccountData{}, true
	}
	return account, *data, true
}

// benchmarkSteps is how many bar requests loadBenchmark makes after the walk:
// one window at each end of the horizon.
const benchmarkSteps = 2

// historyLoad is how far a pass has got, counted in the steps that take real
// time: the windows of the history walk, then the benchmark's bar windows.
type historyLoad struct {
	windows     int // finished history windows, HistoryProgress.Done
	windowTotal int // windows the pass can take, HistoryProgress.Total; 0 until the first report
	bars        int // bar requests answered
}

// historyLoadFraction is the share of a pass that is done, in [0, 1].
//
// Before the walk reports anything nothing is done, which reads as the empty
// bar. The first report fixes the total, so from then on every step can only
// move the bar forward. A pass that stops early never reaches 1 and does not
// need to: the screen that explains the stop replaces the bar the moment the
// pass ends.
func historyLoadFraction(l historyLoad) float64 {
	steps := l.windowTotal + benchmarkSteps
	if steps <= 0 {
		return 0
	}
	return clampFraction(float64(l.windows+l.bars) / float64(steps))
}

// clampFraction pins a share to [0, 1] and reads NaN as nothing done.
func clampFraction(f float64) float64 {
	switch {
	case math.IsNaN(f), f < 0:
		return 0
	case f > 1:
		return 1
	}
	return f
}

// historyErrorText turns a failed pass into the line on the screen. A rate
// limit is named as such: it is the one failure waiting actually fixes.
func historyErrorText(err error) string {
	if api.IsRateLimited(err) {
		return "[red]лимит API исчерпан — подождите и нажмите R[-]"
	}
	return "[red]не удалось загрузить историю, R повторить[-]"
}

// historyStatusText is the whole status line for a settled cache: an error, an
// interrupted pass, or a note that the history does not reach as far back as
// the account does. A pass in flight has no line: the progress bar replaces the
// whole screen while it runs.
func historyStatusText(data analyticsAccountData) string {
	if data.historyErr != "" {
		return data.historyErr
	}
	if data.history == nil {
		return ""
	}

	switch data.history.Stop {
	case api.StopQuota:
		return "[yellow]загрузка не начата: не хватает квот API, R повторить[-]"
	case api.StopRateLimited:
		return fmt.Sprintf("[yellow]лимит API — история загружена с %s, R догрузить[-]",
			formatHistoryDate(data.history.Boundary))
	case api.StopError:
		return fmt.Sprintf("[yellow]загрузка прервана — история с %s, R повторить[-]",
			formatHistoryDate(data.history.Boundary))
	case api.StopGuard:
		return fmt.Sprintf("[yellow]данные с %s, ранняя история не загружена[-]",
			formatHistoryDate(data.history.Boundary))
	}

	if !data.history.Complete {
		return fmt.Sprintf("[yellow]данные с %s, ранняя история не загружена[-]",
			formatHistoryDate(data.history.Boundary))
	}
	return ""
}

// formatHistoryDate renders a boundary for the status line, or a dash when the
// pass never established one.
func formatHistoryDate(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format("02.01.2006")
}

// noHistoryStartText is the status line of an account whose history start the
// broker has not reported: there is no window to walk.
const noHistoryStartText = "[yellow]нет данных о начале истории счёта[-]"

// hasHistoryStart reports whether the broker has said when the account's
// history begins. Without either date a full pass has nothing to walk.
func hasHistoryStart(account models.AccountInfo) bool {
	return !account.FirstTradeDate.IsZero() || !account.FirstNonTradeDate.IsZero()
}

// historyStatusLine is the status line for the account on screen: the settled
// state of its cache or, with nothing loaded and no history start reported,
// why nothing will load. It is derived on every redraw, so entering a screen
// cannot wipe it the way a line written once by the loader was wiped.
func historyStatusLine(account models.AccountInfo, data analyticsAccountData) string {
	if text := historyStatusText(data); text != "" {
		return text
	}
	if data.history == nil && !hasHistoryStart(account) {
		return noHistoryStartText
	}
	return ""
}

// setHistoryStatus writes one line to both history screens.
func (a *App) setHistoryStatus(text string) {
	view := a.analyticsView()
	view.TradeStatus.SetText(text)
	view.MoneyStatus.SetText(text)
}

// showHistoryLoad puts the progress bar up in place of the Trades and Money
// screens, or takes it down, and moves focus with it.
//
// Focus moves only when the change would strand it: on the screen the bar now
// covers, or on the bar being taken away. A search window, a profile or a
// modal opened while the pass ran keeps its focus; closing it hands focus to
// whatever the tab shows by then.
func (a *App) showHistoryLoad(loading bool, fraction float64) {
	view := a.analyticsView()
	before := view.Focusable()
	view.SetHistoryLoad(loading, fraction)
	if after := view.Focusable(); after != before && a.app.GetFocus() == before {
		a.app.SetFocus(after)
	}
}
