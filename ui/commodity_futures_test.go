package ui

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"finam-terminal/config"
	"finam-terminal/models"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func runCommodityFuturesEventLoop(t *testing.T, app *App) <-chan struct{} {
	t.Helper()
	drawn := make(chan struct{}, 1)
	screen := tcell.NewSimulationScreen("UTF-8")
	app.app.SetScreen(screen).SetRoot(tview.NewBox(), true)
	app.app.SetAfterDrawFunc(func(tcell.Screen) {
		select {
		case drawn <- struct{}{}:
		default:
		}
	})
	stopped := make(chan error, 1)
	go func() { stopped <- app.app.Run() }()
	t.Cleanup(func() {
		app.Stop()
		select {
		case err := <-stopped:
			if err != nil {
				t.Errorf("event loop: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("event loop did not stop")
		}
	})
	select {
	case <-drawn:
	case <-time.After(5 * time.Second):
		t.Fatal("event loop did not start")
	}
	return drawn
}

func waitCommodityFuturesSettled(t *testing.T, app *App, drawn <-chan struct{}) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		app.dataMutex.RLock()
		settled := app.commodityFutures != nil && !app.commodityFutures.loading
		app.dataMutex.RUnlock()
		if settled {
			// Synchronize widget updates even if the loader's final redraw has
			// not yet entered the event queue after clearing loading.
			app.app.QueueUpdateDraw(func() { app.refreshCommodityFutureSelection() })
			return
		}
		select {
		case <-drawn:
		case <-deadline:
			t.Fatal("metadata refresh did not settle")
		}
	}
}

func TestCommodityCachedContractOpensWithoutDiscoveryAcrossAccounts(t *testing.T) {
	now := time.Now()
	contract := models.FutureContract{Symbol: "NGX6@RTSX", Name: "NG-11.26", Expiration: now.AddDate(0, 0, 30), Decimals: 3}
	var walks atomic.Int64
	client := &mockClient{
		CachedMOEXFutureFunc: func(mask string) (models.FutureContract, time.Time, bool) {
			if mask == "NG*@RTSX" {
				return contract, now, true
			}
			return models.FutureContract{}, time.Time{}, false
		},
		GetMOEXFutureFunc: func(string, string, int) (models.FutureContract, error) {
			walks.Add(1)
			return models.FutureContract{}, nil
		},
	}
	app := NewApp(client, []models.AccountInfo{{ID: "acc1"}, {ID: "acc2"}})
	t.Cleanup(app.Stop)
	app.ConfigureCommodities(config.CommoditiesConfig{Enabled: true, Commodities: map[string]config.Commodity{
		"NG": {Symbol: "NG@XNYM", Name: "Газ", MOEXMask: "NG*@RTSX", RollDays: 2, Decimals: 3},
	}})
	app.portfolioView.TabbedView.SetTab(TabCommodities)
	updateCommoditiesTable(app)
	app.ensureCommodityFutures(false)
	if got := app.activeCommodityOrderSymbol(); got != contract.Symbol {
		t.Fatalf("cached target=%q, want %q", got, contract.Symbol)
	}
	app.selectedIdx = 1
	app.ensureCommodityFutures(false)
	if got := app.commodityOrderSymbol("NG@XNYM"); got != contract.Symbol {
		t.Fatalf("another account failed to reuse cached target %q", got)
	}
	if walks.Load() != 0 {
		t.Fatal("fresh shared contract triggered metadata discovery")
	}
	app.selectedIdx = -1
	app.dataMutex.RLock()
	future, found := app.commodityFutureLocked("NG@XNYM", now)
	app.dataMutex.RUnlock()
	if !found || future.Symbol != contract.Symbol {
		t.Fatal("informational contract disappeared without an account")
	}
	updateCommoditiesTable(app)
	if got := app.portfolioView.TabbedView.Commodities.Native.GetCell(1, 0).Text; got != contract.Name {
		t.Fatalf("native details without an account=%q, want %q", got, contract.Name)
	}
	if got := app.commodityOrderSymbol("NG@XNYM"); got != "" {
		t.Fatalf("order target %q allowed without an account", got)
	}
	app.ensureCommodityFutures(true)
	if walks.Load() != 0 {
		t.Fatal("metadata discovery ran without a selected account")
	}
}

func TestCommodityMidnightRefreshRunsOffscreenAndRetainsFailedBinding(t *testing.T) {
	now := time.Now()
	yesterday := now.AddDate(0, 0, -1)
	old := models.FutureContract{Symbol: "NGV6@RTSX", Expiration: now.Add(time.Hour)}
	next := models.FutureContract{Symbol: "NGX6@RTSX", Expiration: now.AddDate(0, 0, 30)}
	var calls atomic.Int64
	client := &mockClient{
		CachedMOEXFutureFunc: func(string) (models.FutureContract, time.Time, bool) { return old, yesterday, true },
		GetMOEXFutureFunc: func(account, mask string, rollDays int) (models.FutureContract, error) {
			if account != "acc2" || mask != "NG*@RTSX" || rollDays != 2 {
				t.Errorf("metadata request account=%q mask=%q rollDays=%d", account, mask, rollDays)
			}
			if calls.Add(1) > 1 {
				return old, errors.New("metadata unavailable")
			}
			return next, nil
		},
	}
	app := NewApp(client, []models.AccountInfo{{ID: "acc1"}, {ID: "acc2"}})
	app.selectedIdx = 1
	app.ConfigureCommodities(config.CommoditiesConfig{Enabled: true, Commodities: map[string]config.Commodity{
		"NG": {Symbol: "NG@XNYM", Name: "Газ", MOEXMask: "NG*@RTSX", RollDays: 2},
	}})
	app.commodityLimited = true
	app.commodityMetadataLimitedAt = yesterday
	if app.commoditiesTabActive() {
		t.Fatal("fixture must keep Commodities offscreen")
	}
	drawn := runCommodityFuturesEventLoop(t, app)
	app.app.QueueUpdateDraw(func() { app.refreshCommodityFuturesDaily(now) })
	waitCommodityFuturesSettled(t, app, drawn)
	if got := app.commodityOrderSymbol("NG@XNYM"); got != next.Symbol {
		t.Fatalf("refreshed order target=%q, want %q", got, next.Symbol)
	}
	app.app.QueueUpdateDraw(func() {
		app.selectedIdx = 0
		app.refreshCommodityFuturesDaily(now)
	})
	if calls.Load() != 1 {
		t.Fatalf("same-date/account-switch requests=%d, want one", calls.Load())
	}
	app.app.QueueUpdateDraw(func() {
		app.selectedIdx = 1
		app.loadCommodityFutures(true, now)
	})
	waitCommodityFuturesSettled(t, app, drawn)
	app.dataMutex.RLock()
	state := app.commodityFutures
	retained := state.updatedAt["NG@XNYM"].Equal(now) && state.bindings["NG@XNYM"] == next && state.errors["NG@XNYM"] != ""
	app.dataMutex.RUnlock()
	if !retained {
		t.Fatal("failed refresh replaced the successful contract or timestamp")
	}
	app.app.QueueUpdateDraw(func() { app.refreshCommodityFuturesDaily(now) })
	if calls.Load() != 2 {
		t.Fatal("failed manual refresh was retried automatically on the same date")
	}
}

func TestCommodityThresholdRefreshDespiteSameDateAndNoRepeatedFailure(t *testing.T) {
	morning := time.Date(2026, time.October, 4, 9, 0, 0, 0, time.Local)
	threshold := morning.Add(time.Hour)
	old := models.FutureContract{Symbol: "NGV6@RTSX", Expiration: threshold.AddDate(0, 0, 2)}
	next := models.FutureContract{Symbol: "NGX6@RTSX", Expiration: morning.AddDate(0, 0, 30)}
	var calls atomic.Int64
	client := &mockClient{GetMOEXFutureFunc: func(account, mask string, rollDays int) (models.FutureContract, error) {
		if account != "acc1" || mask != "NG*@RTSX" || rollDays != 2 {
			t.Errorf("rollover request account=%q mask=%q rollDays=%d", account, mask, rollDays)
		}
		if calls.Add(1) == 1 {
			return old, errors.New("offline")
		}
		return next, nil
	}}
	app := NewApp(client, []models.AccountInfo{{ID: "acc1"}})
	app.ConfigureCommodities(config.CommoditiesConfig{Enabled: true, Commodities: map[string]config.Commodity{
		"NG": {Symbol: "NG@XNYM", Name: "Газ", MOEXMask: "NG*@RTSX", RollDays: 2},
	}})
	app.commodityFutures = &commodityFuturesState{
		bindings:  map[string]models.FutureContract{"NG@XNYM": old},
		updatedAt: map[string]time.Time{"NG@XNYM": morning},
		askedAt:   map[string]time.Time{"NG@XNYM": morning},
	}
	drawn := runCommodityFuturesEventLoop(t, app)
	app.app.QueueUpdateDraw(func() { app.loadCommodityFutures(false, morning) })
	if calls.Load() != 0 {
		t.Fatal("eligible same-date binding was refreshed")
	}
	app.app.QueueUpdateDraw(func() { app.loadCommodityFutures(false, threshold) })
	waitCommodityFuturesSettled(t, app, drawn)
	app.dataMutex.RLock()
	contract, live := app.commodityFutureLocked("NG@XNYM", threshold)
	retained := app.commodityFutures.updatedAt["NG@XNYM"].Equal(morning)
	app.dataMutex.RUnlock()
	if calls.Load() != 1 || !live || contract != old || !retained {
		t.Fatalf("failed threshold refresh calls=%d live=%v contract=%+v retained=%v", calls.Load(), live, contract, retained)
	}
	app.app.QueueUpdateDraw(func() { app.loadCommodityFutures(false, threshold.Add(time.Hour)) })
	if calls.Load() != 1 {
		t.Fatal("failed rollover repeatedly retried on the same local date")
	}
	tomorrow := threshold.AddDate(0, 0, 1)
	app.app.QueueUpdateDraw(func() { app.loadCommodityFutures(false, tomorrow) })
	waitCommodityFuturesSettled(t, app, drawn)
	app.dataMutex.RLock()
	contract, live = app.commodityFutureLocked("NG@XNYM", tomorrow)
	updated := app.commodityFutures.updatedAt["NG@XNYM"]
	app.dataMutex.RUnlock()
	if calls.Load() != 2 || !live || contract != next || !updated.Equal(tomorrow) {
		t.Fatalf("next-date rollover calls=%d live=%v contract=%+v updated=%v", calls.Load(), live, contract, updated)
	}
}

func TestCommodityExpiredBindingCannotDisplayTradeOrSubscribe(t *testing.T) {
	now := time.Now()
	expired := models.FutureContract{Symbol: "NGV6@RTSX", Expiration: now.Add(-time.Nanosecond)}
	app := NewApp(&mockClient{}, []models.AccountInfo{{ID: "acc1"}})
	t.Cleanup(app.Stop)
	app.ConfigureCommodities(config.CommoditiesConfig{Enabled: true, Commodities: map[string]config.Commodity{
		"NG": {Symbol: "NG@XNYM", Name: "Газ", MOEXMask: "NG*@RTSX", RollDays: 2},
	}})
	app.commodityFutures = &commodityFuturesState{bindings: map[string]models.FutureContract{"NG@XNYM": expired}}
	app.portfolioView.TabbedView.SetTab(TabCommodities)
	app.commodityQuotes[expired.Symbol] = &models.Quote{Last: "3.5"}
	if app.activeCommodityOrderSymbol() != "" {
		t.Fatal("expired contract remains an order target")
	}
	if err := app.SubmitOrder(OrderSubmission{Instrument: expired.Symbol, OrderType: models.OrderTypeLimit, Quantity: 1}); err == nil {
		t.Fatal("expired contract order was accepted")
	}
	app.dataMutex.RLock()
	_, live := app.commodityFutureLocked("NG@XNYM", expired.Expiration)
	symbols := app.commodityQuoteSymbolsLocked()
	app.dataMutex.RUnlock()
	if live {
		t.Fatal("binding remained live at its exact expiration")
	}
	for _, symbol := range symbols {
		if symbol == expired.Symbol {
			t.Fatal("expired binding remained subscribed")
		}
	}
	updateCommoditiesTable(app)
	if got := app.portfolioView.TabbedView.Commodities.Native.GetCell(1, 0).Text; got != noIndexData {
		t.Fatalf("expired binding shown as live: %q", got)
	}
}

func TestCommodityMetadataRateLimitStopsPassAndManualRefreshBypasses(t *testing.T) {
	now := time.Now()
	var calls atomic.Int64
	client := &mockClient{GetMOEXFutureFunc: func(string, string, int) (models.FutureContract, error) {
		calls.Add(1)
		return models.FutureContract{}, status.Error(codes.ResourceExhausted, "quota")
	}}
	app := NewApp(client, []models.AccountInfo{{ID: "acc1"}})
	app.ConfigureCommodities(config.CommoditiesConfig{Enabled: true, Order: []string{"NG", "ES"}, Commodities: map[string]config.Commodity{
		"NG": {Symbol: "NG@XNYM", Name: "Газ", MOEXMask: "NG*@RTSX"},
		"ES": {Symbol: "ES@XCME", Name: "S&P 500", MOEXMask: "SF*@RTSX"},
	}})
	drawn := runCommodityFuturesEventLoop(t, app)
	app.app.QueueUpdateDraw(func() { app.loadCommodityFutures(false, now) })
	waitCommodityFuturesSettled(t, app, drawn)
	app.app.QueueUpdateDraw(func() { app.loadCommodityFutures(false, now) })
	if calls.Load() != 1 {
		t.Fatalf("rate-limited automatic pass calls=%d, want one", calls.Load())
	}
	app.app.QueueUpdateDraw(func() { app.loadCommodityFutures(true, now) })
	waitCommodityFuturesSettled(t, app, drawn)
	if calls.Load() != 2 {
		t.Fatalf("manual pass calls=%d, want two", calls.Load())
	}
}
