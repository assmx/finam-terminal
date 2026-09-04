package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"finam-terminal/api"
	"finam-terminal/models"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/marketdata"
	"github.com/gdamore/tcell/v2"
)

// historyMock answers LoadHistory with a small bundle and counts the calls.
func historyMock() *mockClient {
	return &mockClient{
		LoadHistoryFunc: func(_ context.Context, req api.HistoryRequest) (*api.HistoryBundle, error) {
			return &api.HistoryBundle{
				Trades: []models.Trade{{
					ID: "T1-" + req.AccountID, Symbol: "SBER@MISX", Side: "Buy",
					Quantity: "10", Price: "100", Currency: "RUB",
					Timestamp: time.Now().Add(-24 * time.Hour),
				}},
				Transactions: []models.Transaction{{
					ID: "X1-" + req.AccountID, Category: "DEPOSIT",
					Amount: 100000, Currency: "RUB",
					Timestamp: time.Now().Add(-48 * time.Hour),
				}},
				Boundary: req.To.AddDate(-1, 0, 0),
				Complete: true,
			}, nil
		},
		GetBarsFunc: func(_ string, _ string, _ marketdata.TimeFrame, from, _ time.Time) ([]models.Bar, error) {
			return []models.Bar{{Timestamp: from.Add(24 * time.Hour), Close: 2000}}, nil
		},
	}
}

// historyApp puts an app on the Analytics tab with an account whose history
// dates are known, so the loader has a window to walk.
func historyApp(t *testing.T, mock *mockClient, accounts ...models.AccountInfo) (*App, func(*tcell.EventKey) *tcell.EventKey) {
	t.Helper()

	if len(accounts) == 0 {
		accounts = []models.AccountInfo{{
			ID:             "acc1",
			FirstTradeDate: time.Now().AddDate(-2, 0, 0),
		}}
	}
	app := NewApp(mock, accounts)
	setupInputHandlers(app)
	app.portfolioView.TabbedView.SetTab(TabAnalytics)

	t.Cleanup(app.Stop)
	return app, app.app.GetInputCapture()
}

// waitHistory waits for the loader to have run n times and settled.
func waitHistory(t *testing.T, app *App, mock *mockClient, n int64) {
	t.Helper()

	if !waitFor(func() bool { return mock.LoadHistoryCalls.Load() >= n }) {
		t.Fatalf("LoadHistory called %d times, want %d", mock.LoadHistoryCalls.Load(), n)
	}
	if !waitFor(func() bool {
		app.dataMutex.RLock()
		defer app.dataMutex.RUnlock()
		data := app.analytics.byAccount["acc1"]
		return data != nil && !data.loading
	}) {
		t.Fatal("the history load never settled")
	}
}

// TestHistory_LazyOnFirstVisit loads once when the Trades screen is opened,
// and not before.
func TestHistory_LazyOnFirstVisit(t *testing.T) {
	mock := historyMock()
	app, capture := historyApp(t, mock)

	if got := mock.LoadHistoryCalls.Load(); got != 0 {
		t.Fatalf("LoadHistory called %d times before the screen was opened, want 0", got)
	}

	capture(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))
	waitHistory(t, app, mock, 1)

	app.dataMutex.RLock()
	bundle := app.analytics.byAccount["acc1"].history
	app.dataMutex.RUnlock()

	if bundle == nil || len(bundle.Trades) != 1 {
		t.Fatalf("history = %+v, want the loaded bundle", bundle)
	}
}

// TestHistory_SharedBetweenTradesAndMoney: the two screens read one cache, so
// opening the second costs nothing.
func TestHistory_SharedBetweenTradesAndMoney(t *testing.T) {
	mock := historyMock()
	app, capture := historyApp(t, mock)

	capture(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))
	waitHistory(t, app, mock, 1)

	capture(tcell.NewEventKey(tcell.KeyRune, '3', tcell.ModNone))
	capture(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))
	capture(tcell.NewEventKey(tcell.KeyRune, '3', tcell.ModNone))

	if got := mock.LoadHistoryCalls.Load(); got != 1 {
		t.Errorf("LoadHistory called %d times across both screens, want 1", got)
	}
}

// TestHistory_NotLoadedOnATick is the budget promise: the five-second refresh
// never reaches for history.
func TestHistory_NotLoadedOnATick(t *testing.T) {
	mock := historyMock()
	app, capture := historyApp(t, mock)

	capture(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))
	waitHistory(t, app, mock, 1)

	for range 20 {
		app.applyAccountData("acc1", nil, nil, &models.AccountInfo{ID: "acc1"})
	}

	if got := mock.LoadHistoryCalls.Load(); got != 1 {
		t.Errorf("LoadHistory called %d times after twenty ticks, want 1", got)
	}
}

// TestHistory_PerAccount: switching accounts loads the new one and keeps the
// old one's answer.
func TestHistory_PerAccount(t *testing.T) {
	mock := historyMock()
	app, capture := historyApp(t, mock,
		models.AccountInfo{ID: "acc1", FirstTradeDate: time.Now().AddDate(-1, 0, 0)},
		models.AccountInfo{ID: "acc2", FirstTradeDate: time.Now().AddDate(-1, 0, 0)},
	)

	capture(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))
	waitHistory(t, app, mock, 1)

	app.dataMutex.Lock()
	app.selectedIdx = 1
	app.dataMutex.Unlock()
	app.EnterAnalyticsTab()

	if !waitFor(func() bool { return mock.LoadHistoryCalls.Load() >= 2 }) {
		t.Fatalf("LoadHistory called %d times after switching accounts, want 2", mock.LoadHistoryCalls.Load())
	}

	app.dataMutex.RLock()
	first := app.analytics.byAccount["acc1"]
	second := app.analytics.byAccount["acc2"]
	app.dataMutex.RUnlock()

	if first == nil || first.history == nil {
		t.Error("the first account's history was discarded on switching")
	}
	if second == nil || second.history == nil {
		t.Error("the second account's history was not loaded")
	}
	if first != nil && second != nil && first.history == second.history {
		t.Error("both accounts share one bundle")
	}

	// Coming back is instant.
	app.dataMutex.Lock()
	app.selectedIdx = 0
	app.dataMutex.Unlock()
	app.EnterAnalyticsTab()

	if got := mock.LoadHistoryCalls.Load(); got != 2 {
		t.Errorf("LoadHistory called %d times after returning to a loaded account, want 2", got)
	}
}

// TestHistory_NoWindowMakesNoRequest: an account with no known history start
// has nothing to walk.
func TestHistory_NoWindowMakesNoRequest(t *testing.T) {
	mock := historyMock()
	_, capture := historyApp(t, mock, models.AccountInfo{ID: "acc1"})

	capture(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))

	// Give a would-be goroutine a chance to run before concluding it did not.
	waitFor(func() bool { return mock.LoadHistoryCalls.Load() > 0 })

	if got := mock.LoadHistoryCalls.Load(); got != 0 {
		t.Errorf("LoadHistory called %d times for an account with no dates, want 0", got)
	}
}

// TestHistory_RefreshTailAndCooldown: R tops up the tail, and a second R
// inside the cooldown is refused with a message rather than silently ignored.
func TestHistory_RefreshTailAndCooldown(t *testing.T) {
	mock := historyMock()
	app, capture := historyApp(t, mock)

	capture(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))
	waitHistory(t, app, mock, 1)

	// The first pass just finished, so R is inside the cooldown.
	capture(tcell.NewEventKey(tcell.KeyRune, 'R', tcell.ModNone))
	if got := mock.LoadHistoryCalls.Load(); got != 1 {
		t.Errorf("LoadHistory called %d times inside the cooldown, want 1", got)
	}
	if status := app.analyticsView().TradeStatus.GetText(true); !strings.Contains(status, "подождите") {
		t.Errorf("status = %q, want it to say the refresh is on cooldown", status)
	}

	// Move the clock back past the cooldown.
	app.dataMutex.Lock()
	app.analytics.byAccount["acc1"].historyAt = time.Now().Add(-2 * analyticsRefreshCooldown)
	app.dataMutex.Unlock()

	capture(tcell.NewEventKey(tcell.KeyRune, 'R', tcell.ModNone))
	waitHistory(t, app, mock, 2)
}

// TestHistory_TailIsANarrowWindow: the top-up asks only for what it is missing,
// not for the whole account again.
func TestHistory_TailIsANarrowWindow(t *testing.T) {
	mock := historyMock()
	var windows []api.HistoryRequest
	mock.LoadHistoryFunc = func(_ context.Context, req api.HistoryRequest) (*api.HistoryBundle, error) {
		windows = append(windows, req)
		return &api.HistoryBundle{Boundary: req.To.AddDate(-1, 0, 0), Complete: true}, nil
	}

	app, capture := historyApp(t, mock)

	capture(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))
	waitHistory(t, app, mock, 1)

	app.dataMutex.Lock()
	app.analytics.byAccount["acc1"].historyAt = time.Now().Add(-2 * analyticsRefreshCooldown)
	app.dataMutex.Unlock()

	capture(tcell.NewEventKey(tcell.KeyRune, 'R', tcell.ModNone))
	waitHistory(t, app, mock, 2)

	if len(windows) != 2 {
		t.Fatalf("recorded %d requests, want 2", len(windows))
	}
	full := windows[0].To.Sub(windows[0].TradesFrom)
	tail := windows[1].To.Sub(windows[1].TradesFrom)
	if tail >= full {
		t.Errorf("the top-up window is %v against the first pass's %v; it must be narrower", tail, full)
	}
	if tail > 7*24*time.Hour {
		t.Errorf("the top-up window is %v, want a few days at most", tail)
	}
}

// TestHistory_MergesTail keeps what the first pass loaded and adds what the
// second one brought, without duplicating the overlap.
func TestHistory_MergesTail(t *testing.T) {
	mock := historyMock()
	call := 0
	shared := models.Trade{ID: "shared", Symbol: "SBER@MISX", Side: "Buy",
		Quantity: "1", Price: "100", Timestamp: time.Now().Add(-2 * time.Hour)}

	mock.LoadHistoryFunc = func(_ context.Context, req api.HistoryRequest) (*api.HistoryBundle, error) {
		call++
		if call == 1 {
			return &api.HistoryBundle{
				Trades:   []models.Trade{{ID: "old", Symbol: "SBER@MISX", Side: "Buy", Quantity: "1", Price: "90", Timestamp: time.Now().Add(-100 * time.Hour)}, shared},
				Boundary: req.To.AddDate(-1, 0, 0),
				Complete: true,
			}, nil
		}
		return &api.HistoryBundle{
			Trades:   []models.Trade{shared, {ID: "new", Symbol: "SBER@MISX", Side: "Sell", Quantity: "1", Price: "110", Timestamp: time.Now()}},
			Boundary: req.TradesFrom,
			Complete: true,
		}, nil
	}

	app, capture := historyApp(t, mock)

	capture(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))
	waitHistory(t, app, mock, 1)

	app.dataMutex.Lock()
	app.analytics.byAccount["acc1"].historyAt = time.Now().Add(-2 * analyticsRefreshCooldown)
	app.dataMutex.Unlock()

	capture(tcell.NewEventKey(tcell.KeyRune, 'R', tcell.ModNone))
	waitHistory(t, app, mock, 2)

	app.dataMutex.RLock()
	trades := app.analytics.byAccount["acc1"].history.Trades
	app.dataMutex.RUnlock()

	seen := map[string]int{}
	for _, tr := range trades {
		seen[tr.ID]++
	}
	if len(trades) != 3 {
		t.Errorf("got %d trades, want 3 (old, shared, new): %v", len(trades), seen)
	}
	for _, id := range []string{"old", "shared", "new"} {
		if seen[id] != 1 {
			t.Errorf("trade %s appears %d times, want once", id, seen[id])
		}
	}
}

// TestHistory_PartialDataStays: a pass stopped by a rate limit keeps what it
// loaded and says so.
func TestHistory_PartialDataStays(t *testing.T) {
	mock := historyMock()
	mock.LoadHistoryFunc = func(_ context.Context, req api.HistoryRequest) (*api.HistoryBundle, error) {
		return &api.HistoryBundle{
			Trades:   []models.Trade{{ID: "T1", Symbol: "SBER@MISX", Side: "Buy", Quantity: "1", Price: "100", Timestamp: time.Now()}},
			Boundary: req.To.AddDate(0, -3, 0),
			Complete: false,
			Stop:     api.StopRateLimited,
		}, nil
	}

	app, capture := historyApp(t, mock)

	capture(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))
	waitHistory(t, app, mock, 1)

	app.dataMutex.RLock()
	bundle := app.analytics.byAccount["acc1"].history
	app.dataMutex.RUnlock()

	if bundle == nil || len(bundle.Trades) != 1 {
		t.Fatalf("history = %+v, want the partial bundle kept", bundle)
	}

	// The tview event loop is not running under test, so the queued redraw
	// never fires; run it the way the loop would.
	app.updateAnalyticsHistoryStatus()
	status := app.analyticsView().TradeStatus.GetText(true)
	if !strings.Contains(status, "лимит") {
		t.Errorf("status = %q, want it to name the rate limit", status)
	}
}

// TestHistory_IncompleteIsMarked tells the reader the numbers cover less than
// they asked for.
func TestHistory_IncompleteIsMarked(t *testing.T) {
	mock := historyMock()
	boundary := time.Date(2024, 5, 12, 0, 0, 0, 0, time.UTC)
	mock.LoadHistoryFunc = func(context.Context, api.HistoryRequest) (*api.HistoryBundle, error) {
		return &api.HistoryBundle{Boundary: boundary, Complete: false, Stop: api.StopGuard}, nil
	}

	app, capture := historyApp(t, mock)

	capture(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))
	waitHistory(t, app, mock, 1)

	app.updateAnalyticsHistoryStatus()
	status := app.analyticsView().TradeStatus.GetText(true)
	if !strings.Contains(status, "12.05.2024") {
		t.Errorf("status = %q, want it to name the date history starts from", status)
	}
}

// TestHistory_ErrorShowsRetryHint.
func TestHistory_ErrorShowsRetryHint(t *testing.T) {
	mock := historyMock()
	mock.LoadHistoryFunc = func(context.Context, api.HistoryRequest) (*api.HistoryBundle, error) {
		return nil, context.DeadlineExceeded
	}

	app, capture := historyApp(t, mock)

	capture(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))
	waitHistory(t, app, mock, 1)

	app.updateAnalyticsHistoryStatus()
	status := app.analyticsView().TradeStatus.GetText(true)
	if !strings.Contains(status, "R") {
		t.Errorf("status = %q, want the retry hint", status)
	}
}

// TestHistory_QuotaBlockIsExplained.
func TestHistory_QuotaBlockIsExplained(t *testing.T) {
	mock := historyMock()
	mock.LoadHistoryFunc = func(context.Context, api.HistoryRequest) (*api.HistoryBundle, error) {
		return &api.HistoryBundle{Stop: api.StopQuota, StopErr: context.DeadlineExceeded}, nil
	}

	app, capture := historyApp(t, mock)

	capture(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))
	waitHistory(t, app, mock, 1)

	app.updateAnalyticsHistoryStatus()
	if status := app.analyticsView().TradeStatus.GetText(true); !strings.Contains(status, "квот") {
		t.Errorf("status = %q, want it to explain the quota refusal", status)
	}
}

// TestHistory_OnlyOnePassAtATime: a second trigger while one is running is
// dropped rather than stacking a duplicate walk.
func TestHistory_OnlyOnePassAtATime(t *testing.T) {
	mock := historyMock()
	mock.LoadHistoryDelay = 150 * time.Millisecond

	app, capture := historyApp(t, mock)

	capture(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))
	for range 5 {
		capture(tcell.NewEventKey(tcell.KeyRune, '3', tcell.ModNone))
		capture(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))
	}
	waitHistory(t, app, mock, 1)

	if got := mock.LoadHistoryCalls.Load(); got != 1 {
		t.Errorf("LoadHistory called %d times while one pass was in flight, want 1", got)
	}
}

// TestHistory_StopCancelsThePass: shutting the app down cancels a walk instead
// of leaving it running against a closed application.
func TestHistory_StopCancelsThePass(t *testing.T) {
	mock := historyMock()
	cancelled := make(chan struct{}, 1)
	mock.LoadHistoryFunc = func(ctx context.Context, _ api.HistoryRequest) (*api.HistoryBundle, error) {
		select {
		case <-ctx.Done():
			cancelled <- struct{}{}
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
			return &api.HistoryBundle{}, nil
		}
	}

	app, capture := historyApp(t, mock)

	capture(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))
	if !waitFor(func() bool { return mock.LoadHistoryCalls.Load() >= 1 }) {
		t.Fatal("the pass never started")
	}

	app.Stop()

	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Error("the pass was not cancelled when the app stopped")
	}
}

// TestHistory_BenchmarkBarsLoadedWithHistory: the index comparison costs two
// requests, taken alongside the walk rather than on their own trigger.
func TestHistory_BenchmarkBarsLoadedWithHistory(t *testing.T) {
	mock := historyMock()
	app, capture := historyApp(t, mock)

	capture(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))
	waitHistory(t, app, mock, 1)

	if !waitFor(func() bool { return mock.GetBarsCalls.Load() >= 2 }) {
		t.Fatalf("GetBars called %d times, want 2 (one window at each end)", mock.GetBarsCalls.Load())
	}
	if got := mock.GetBarsCalls.Load(); got != 2 {
		t.Errorf("GetBars called %d times, want exactly 2", got)
	}

	app.dataMutex.RLock()
	ok := app.analytics.byAccount["acc1"].benchmarkOK
	app.dataMutex.RUnlock()
	if !ok {
		t.Error("the benchmark was not computed from the bars")
	}
}

// TestHistory_BenchmarkFailureIsNotFatal: bars are a nicety, and losing them
// must not cost the history.
func TestHistory_BenchmarkFailureIsNotFatal(t *testing.T) {
	mock := historyMock()
	mock.GetBarsFunc = func(string, string, marketdata.TimeFrame, time.Time, time.Time) ([]models.Bar, error) {
		return nil, context.DeadlineExceeded
	}

	app, capture := historyApp(t, mock)

	capture(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))
	waitHistory(t, app, mock, 1)

	app.dataMutex.RLock()
	data := app.analytics.byAccount["acc1"]
	hasHistory := data.history != nil
	benchOK := data.benchmarkOK
	app.dataMutex.RUnlock()

	if !hasHistory {
		t.Error("the history was discarded because the benchmark failed")
	}
	if benchOK {
		t.Error("the benchmark reports success after a failed request")
	}
}
