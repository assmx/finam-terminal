package ui

import (
	"time"

	"finam-terminal/analytics"
	"finam-terminal/api"
)

// analyticsState is everything the Analytics tab remembers between draws.
//
// Everything the tab shows is per account and lives in byAccount. The one
// exception is the period: it describes how the user wants to look, not what
// they are looking at, so it is chosen once per session and survives switching
// accounts.
type analyticsState struct {
	// period is the window the Trades and Money screens use.
	period analytics.Preset

	byAccount map[string]*analyticsAccountData
}

// analyticsAccountData is everything the tab remembers for one account.
//
// History is loaded once per account per session, and every period is a filter
// over it — this cache is what makes the period control free.
type analyticsAccountData struct {
	history    *api.HistoryBundle
	historyAt  time.Time
	historyErr string
	loading    bool
	progress   string

	benchmark   analytics.Benchmark
	benchmarkOK bool

	// Payouts are built from the calendars, which are themselves cached per
	// symbol for a day — so a refresh here is nearly always free.
	payouts        []analytics.Payout
	payoutTotals   analytics.PayoutTotals
	payoutsAt      time.Time
	payoutsErr     string
	payoutsLoading bool
}

func newAnalyticsState() *analyticsState {
	return &analyticsState{
		period:    analytics.DefaultPreset,
		byAccount: make(map[string]*analyticsAccountData),
	}
}

// AnalyticsPeriod is the window the Trades and Money screens currently use.
func (a *App) AnalyticsPeriod() analytics.Preset {
	a.dataMutex.RLock()
	defer a.dataMutex.RUnlock()
	return a.analytics.period
}

// NextAnalyticsPeriod is the P key: step to the next preset and redraw.
//
// It makes no request. Every preset is a filter over the history already in
// memory, which is the whole reason the loader pays for a full pass once
// rather than a window at a time.
func (a *App) NextAnalyticsPeriod() {
	a.dataMutex.Lock()
	a.analytics.period = a.analytics.period.Next()
	period := a.analytics.period
	a.dataMutex.Unlock()

	view := a.analyticsView()
	view.SetPeriod(period)

	switch view.ActiveScreen {
	case AnalyticsOverview:
		updateAnalyticsOverview(a)
	case AnalyticsTrades, AnalyticsMoney:
		updateAnalyticsHistoryScreens(a)
	}
	updateStatusBar(a)
}

// analyticsView is a shorthand for the tab's widgets.
func (a *App) analyticsView() *AnalyticsView {
	return a.portfolioView.TabbedView.Analytics
}

// SetAnalyticsScreen switches sub-screen and does whatever entering it
// requires. Switching itself is free — the overview redraws from memory — and
// only the first visit to a screen backed by history or calendars costs a
// request.
func (a *App) SetAnalyticsScreen(screen AnalyticsScreen) {
	view := a.analyticsView()
	view.SetScreen(screen)

	switch screen {
	case AnalyticsOverview:
		a.ensureIndexLoaded()
		updateAnalyticsOverview(a)
	case AnalyticsTrades, AnalyticsMoney:
		a.ensureHistoryLoaded()
		a.updateAnalyticsHistoryStatus()
		updateAnalyticsHistoryScreens(a)
	case AnalyticsPayouts:
		a.ensurePayoutsLoaded()
		updatePayoutScreen(a)
	}

	a.app.SetFocus(view.Focusable())
	updateStatusBar(a)
}

// EnterAnalyticsTab is what switching to the tab runs: the sub-screen the user
// last left it on, set up again.
func (a *App) EnterAnalyticsTab() {
	a.SetAnalyticsScreen(a.analyticsView().ActiveScreen)
}

// analyticsScreenForDigit maps a digit key to a sub-screen. An out-of-range
// digit answers false, so a stray key leaves the tab alone rather than
// blanking it.
func analyticsScreenForDigit(r rune) (AnalyticsScreen, bool) {
	index := int(r - '1')
	if index < 0 || index >= len(analyticsScreens) {
		return 0, false
	}
	return analyticsScreens[index].Screen, true
}

// RefreshAnalytics is the R key. Each sub-screen refreshes only its own data,
// so pressing it on the overview cannot spend the history pass that belongs to
// the Trades and Money screens.
func (a *App) RefreshAnalytics() {
	switch a.analyticsView().ActiveScreen {
	case AnalyticsOverview:
		// The overview draws from memory; the only thing it can be missing is
		// the index composition behind the sector line.
		a.reloadIndexForSectors()
		updateAnalyticsOverview(a)
	case AnalyticsTrades, AnalyticsMoney:
		a.refreshHistoryTail()
	case AnalyticsPayouts:
		a.refreshPayouts()
	}
}

// queueDraw marshals a UI update onto the event loop, dropping it once the app
// has stopped so a late goroutine cannot block on a closed application.
func (a *App) queueDraw(fn func()) {
	select {
	case <-a.stopChan:
		return
	default:
	}

	go a.app.QueueUpdateDraw(fn)
}

// reloadIndexForSectors refetches the index composition behind the sector
// line. It is the only thing R can usefully do on the overview, and only when
// the composition is not already there: everything else on that screen is
// recomputed from memory on the next tick anyway.
func (a *App) reloadIndexForSectors() {
	a.dataMutex.RLock()
	loaded := a.indexLoaded
	a.dataMutex.RUnlock()

	if loaded {
		return
	}
	a.loadIndexAsync()
}
