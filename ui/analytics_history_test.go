package ui

import (
	"context"
	"math"
	"strings"
	"sync"
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
	waitHistoryFor(t, app, mock, "acc1", n)
}

// waitHistoryFor is waitHistory for a named account.
//
// Waiting on the call counter alone is not enough and was the cause of a flake:
// the counter is incremented as the request starts, while the cache is written
// after it returns. A test that reads the cache on the counter alone races the
// goroutine it just triggered.
func waitHistoryFor(t *testing.T, app *App, mock *mockClient, accountID string, n int64) {
	t.Helper()

	if !waitFor(func() bool { return mock.LoadHistoryCalls.Load() >= n }) {
		t.Fatalf("LoadHistory called %d times, want %d", mock.LoadHistoryCalls.Load(), n)
	}
	if !waitFor(func() bool {
		app.dataMutex.RLock()
		defer app.dataMutex.RUnlock()
		data := app.analytics.byAccount[accountID]
		return data != nil && !data.loading && !data.historyAt.IsZero()
	}) {
		t.Fatalf("the history load for %s never settled", accountID)
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

	waitHistoryFor(t, app, mock, "acc2", 2)

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
	updateAnalyticsHistoryScreens(app)
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

	updateAnalyticsHistoryScreens(app)
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

	updateAnalyticsHistoryScreens(app)
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

	updateAnalyticsHistoryScreens(app)
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

// TestHistoryHorizon covers the window one pass covers, including the case the
// broker actually produces: money arrives before the first purchase.
func TestHistoryHorizon(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	trade := now.AddDate(-2, 0, 0)
	money := now.AddDate(-3, 0, 0)

	cases := []struct {
		name             string
		account          models.AccountInfo
		wantTrades       time.Time
		wantTransactions time.Time
	}{
		{
			name:             "money before the first trade",
			account:          models.AccountInfo{ID: "a", FirstTradeDate: trade, FirstNonTradeDate: money},
			wantTrades:       trade,
			wantTransactions: money,
		},
		{
			name:             "only a first trade",
			account:          models.AccountInfo{ID: "a", FirstTradeDate: trade},
			wantTrades:       trade,
			wantTransactions: trade,
		},
		{
			name:             "only a first movement: an account that never traded",
			account:          models.AccountInfo{ID: "a", FirstNonTradeDate: money},
			wantTrades:       time.Time{},
			wantTransactions: money,
		},
		{
			name:             "nothing known",
			account:          models.AccountInfo{ID: "a"},
			wantTrades:       time.Time{},
			wantTransactions: time.Time{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := historyHorizon(c.account, now)
			if !req.TradesFrom.Equal(c.wantTrades) {
				t.Errorf("TradesFrom = %v, want %v", req.TradesFrom, c.wantTrades)
			}
			if !req.TransactionsFrom.Equal(c.wantTransactions) {
				t.Errorf("TransactionsFrom = %v, want %v", req.TransactionsFrom, c.wantTransactions)
			}
			if !req.To.Equal(now) {
				t.Errorf("To = %v, want %v", req.To, now)
			}
		})
	}
}

// TestHistoryStatusText renders every state the cache can be in.
func TestHistoryStatusText(t *testing.T) {
	boundary := time.Date(2024, 5, 12, 0, 0, 0, 0, time.UTC)
	bundle := func(stop api.StopReason, complete bool) *api.HistoryBundle {
		return &api.HistoryBundle{Boundary: boundary, Complete: complete, Stop: stop}
	}

	cases := []struct {
		name string
		data analyticsAccountData
		want string
	}{
		{"nothing loaded", analyticsAccountData{}, ""},
		{"an error wins over a stale bundle",
			analyticsAccountData{historyErr: "не удалось", history: bundle(api.StopNone, true)}, "не удалось"},
		{"complete", analyticsAccountData{history: bundle(api.StopNone, true)}, ""},
		{"quota", analyticsAccountData{history: bundle(api.StopQuota, false)}, "квот"},
		{"rate limited", analyticsAccountData{history: bundle(api.StopRateLimited, false)}, "лимит API"},
		{"error mid-pass", analyticsAccountData{history: bundle(api.StopError, false)}, "прервана"},
		{"guard", analyticsAccountData{history: bundle(api.StopGuard, false)}, "12.05.2024"},
		{"incomplete without a reason", analyticsAccountData{history: bundle(api.StopNone, false)}, "12.05.2024"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := historyStatusText(c.data)
			if c.want == "" {
				if got != "" {
					t.Errorf("status = %q, want empty", got)
				}
				return
			}
			if !strings.Contains(got, c.want) {
				t.Errorf("status = %q, want it to mention %q", got, c.want)
			}
		})
	}
}

// TestHistoryLoadFraction measures a pass in the steps that take real time:
// the walk's windows and the benchmark's two bar windows after it.
func TestHistoryLoadFraction(t *testing.T) {
	cases := []struct {
		name string
		load historyLoad
		want float64
	}{
		{"before the first report", historyLoad{}, 0},
		{"total known, nothing done", historyLoad{windowTotal: 3}, 0},
		{"one window of three", historyLoad{windows: 1, windowTotal: 3}, 0.2},
		{"every window, no bars yet", historyLoad{windows: 3, windowTotal: 3}, 0.6},
		{"every window and one bar window", historyLoad{windows: 3, windowTotal: 3, bars: 1}, 0.8},
		{"the whole pass", historyLoad{windows: 3, windowTotal: 3, bars: 2}, 1},
		{"stopped short, bars after", historyLoad{windows: 1, windowTotal: 3, bars: 2}, 0.6},
		{"a pass that reported no window", historyLoad{bars: 1}, 0.5},
		{"more done than the total is clamped", historyLoad{windows: 9, windowTotal: 3, bars: 2}, 1},
		{"more bars than asked for is clamped", historyLoad{windows: 3, windowTotal: 3, bars: 5}, 1},
		{"a negative count is clamped", historyLoad{windows: -4, windowTotal: 3}, 0},
		{"a negative total is not divided by", historyLoad{windows: 1, windowTotal: -9}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := historyLoadFraction(c.load); math.Abs(got-c.want) > 1e-9 {
				t.Errorf("historyLoadFraction(%+v) = %v, want %v", c.load, got, c.want)
			}
		})
	}
}

// TestHistoryLoadFraction_OnlyMovesForward walks every pass length step by step:
// each step moves the bar forward, and a pass that ran to the end fills it.
func TestHistoryLoadFraction_OnlyMovesForward(t *testing.T) {
	for total := 0; total <= 12; total++ {
		prev := historyLoadFraction(historyLoad{})
		if prev != 0 {
			t.Fatalf("total %d: the bar starts at %v, want 0", total, prev)
		}

		steps := []historyLoad{}
		for done := 1; done <= total; done++ {
			steps = append(steps, historyLoad{windows: done, windowTotal: total})
		}
		for bars := 1; bars <= benchmarkSteps; bars++ {
			steps = append(steps, historyLoad{windows: total, windowTotal: total, bars: bars})
		}

		for _, s := range steps {
			got := historyLoadFraction(s)
			if got <= prev {
				t.Errorf("total %d: step %+v shows %v after %v, want it further on", total, s, got, prev)
			}
			prev = got
		}
		if prev != 1 {
			t.Errorf("total %d: a finished pass ends at %v, want 1", total, prev)
		}
	}
}

// TestHistory_ProgressFollowsThePass drives the loader through a pass the mock
// reports window by window, then through the two bar requests, and reads the
// fraction at every step: empty before the first answer, forward on every
// window and every bar window, full at the end — for the same requests as
// before.
func TestHistory_ProgressFollowsThePass(t *testing.T) {
	mock := historyMock()
	mock.LoadHistoryProgress = []api.HistoryProgress{
		{Done: 1, Total: 3},
		{Done: 2, Total: 3},
		{Done: 3, Total: 3},
	}

	var app *App
	var mu sync.Mutex
	var seen []float64
	observe := func() {
		app.dataMutex.RLock()
		data := app.analytics.byAccount["acc1"]
		loading, fraction := data.loading, historyLoadFraction(data.load)
		app.dataMutex.RUnlock()

		mu.Lock()
		defer mu.Unlock()
		if !loading {
			t.Errorf("step %d: the pass is not marked as loading", len(seen))
		}
		seen = append(seen, fraction)
	}
	mock.LoadHistoryObserve = observe
	bars := mock.GetBarsFunc
	mock.GetBarsFunc = func(accountID, symbol string, tf marketdata.TimeFrame, from, to time.Time) ([]models.Bar, error) {
		observe()
		return bars(accountID, symbol, tf, from, to)
	}

	app, capture := historyApp(t, mock)
	capture(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))
	waitHistory(t, app, mock, 1)

	app.dataMutex.RLock()
	final := historyLoadFraction(app.analytics.byAccount["acc1"].load)
	app.dataMutex.RUnlock()

	mu.Lock()
	got := append(append([]float64(nil), seen...), final)
	mu.Unlock()

	// Before each of the three reports, after the last one, before each of the
	// two bar requests, and once the pass is over.
	want := []float64{0, 0.2, 0.4, 0.6, 0.6, 0.8, 1}
	if len(got) != len(want) {
		t.Fatalf("fractions = %v, want %v", got, want)
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Errorf("fractions = %v, want %v", got, want)
			break
		}
	}

	if n := mock.LoadHistoryCalls.Load(); n != 1 {
		t.Errorf("LoadHistory called %d times, want 1 — the bar costs no request", n)
	}
	if n := mock.GetBarsCalls.Load(); n != 2 {
		t.Errorf("GetBars called %d times, want 2 — the bar costs no request", n)
	}
}

// TestHistory_RefreshStartsTheBarAgain: R on a loaded history is a pass of its
// own, and its bar starts from nothing rather than from where the last one
// ended.
func TestHistory_RefreshStartsTheBarAgain(t *testing.T) {
	mock := historyMock()
	app, capture := historyApp(t, mock)

	capture(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))
	waitHistory(t, app, mock, 1)

	app.dataMutex.Lock()
	app.analytics.byAccount["acc1"].historyAt = time.Now().Add(-2 * analyticsRefreshCooldown)
	app.dataMutex.Unlock()

	var mu sync.Mutex
	atStart := -1.0
	mock.LoadHistoryObserve = func() {
		app.dataMutex.RLock()
		fraction := historyLoadFraction(app.analytics.byAccount["acc1"].load)
		app.dataMutex.RUnlock()

		mu.Lock()
		defer mu.Unlock()
		if atStart < 0 {
			atStart = fraction
		}
	}

	capture(tcell.NewEventKey(tcell.KeyRune, 'R', tcell.ModNone))
	waitHistory(t, app, mock, 2)

	mu.Lock()
	defer mu.Unlock()
	if atStart != 0 {
		t.Errorf("the refresh bar starts at %v, want 0 — the previous pass ended full", atStart)
	}
}

// TestFormatHistoryDate: a boundary that was never established renders as a
// dash rather than as year one.
func TestFormatHistoryDate(t *testing.T) {
	if got := formatHistoryDate(time.Time{}); got != "—" {
		t.Errorf("formatHistoryDate(zero) = %q, want a dash", got)
	}
	at := time.Date(2024, 5, 12, 0, 0, 0, 0, time.Local)
	if got := formatHistoryDate(at); got != "12.05.2024" {
		t.Errorf("formatHistoryDate = %q, want 12.05.2024", got)
	}
}

// TestMergeHistory covers the folding rules directly, including the one that
// matters most: a clean top-up must not erase the fact that the original pass
// was truncated.
func TestMergeHistory(t *testing.T) {
	early := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	cached := &api.HistoryBundle{
		Trades:   []models.Trade{{ID: "old", Timestamp: early}},
		Boundary: early,
		Complete: false,
		Stop:     api.StopGuard,
	}
	fresh := &api.HistoryBundle{
		Trades:   []models.Trade{{ID: "new", Timestamp: late}},
		Boundary: late,
		Complete: true,
		Stop:     api.StopNone,
	}

	merged := mergeHistory(cached, fresh)
	if len(merged.Trades) != 2 {
		t.Errorf("merged %d trades, want both", len(merged.Trades))
	}
	if !merged.Boundary.Equal(early) {
		t.Errorf("Boundary = %v, want the deeper of the two (%v)", merged.Boundary, early)
	}
	if merged.Complete {
		t.Error("Complete = true; a top-up cannot complete a truncated pass")
	}
	if merged.Stop != api.StopGuard {
		t.Errorf("Stop = %v, want the original truncation to survive", merged.Stop)
	}

	if got := mergeHistory(nil, fresh); got != fresh {
		t.Error("merging into an empty cache did not return the fresh bundle")
	}
	if got := mergeHistory(cached, nil); got != cached {
		t.Error("merging nothing into a cache did not return the cache")
	}
}
