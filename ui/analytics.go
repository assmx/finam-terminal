package ui

import (
	"log"
	"time"

	"finam-terminal/analytics"
	"finam-terminal/api"
	"finam-terminal/models"
)

// analyticsState is everything the Analytics tab remembers between draws.
//
// The quota cache is session-wide rather than per account: the API reports
// quotas for the token, not for an account, so switching accounts must not
// throw the answer away and pay for it again. byAccount is the opposite case
// and is empty in this track — it exists because the second track's
// sub-screens (trades, cash flows, payouts) are per account, and adding them
// should extend this struct rather than reshape it.
type analyticsState struct {
	quotas        []models.QuotaUsage
	quotasLoaded  bool
	quotasLoading bool
	quotasErr     string
	quotasAt      time.Time

	// period is the window the Trades and Money screens use. One choice per
	// session rather than per account: it describes how the user wants to
	// look, not what they are looking at.
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
// only the API screen's first visit costs a request.
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
	case AnalyticsQuotas:
		a.ensureQuotasLoaded()
		updateQuotaTable(a)
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
// so pressing it on the overview cannot spend the quota request that belongs
// to the API screen.
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
	case AnalyticsQuotas:
		a.loadQuotasAsync()
	}
}

// ensureQuotasLoaded fetches the quota table on the first visit only. Later
// visits reuse the answer: quotas do not move on their own between two glances
// at the screen, and R is there for when the user wants a fresh one.
func (a *App) ensureQuotasLoaded() {
	a.dataMutex.RLock()
	loaded := a.analytics.quotasLoaded || a.analytics.quotasLoading
	a.dataMutex.RUnlock()

	if loaded {
		return
	}
	a.loadQuotasAsync()
}

// loadQuotasAsync fetches the quota table off the event loop.
//
// Nothing retries on its own: a failure leaves the error on the sub-screen
// with the R hint. An automatic retry on a rate-limited call is exactly the
// wrong reflex.
func (a *App) loadQuotasAsync() {
	a.dataMutex.Lock()
	if a.analytics.quotasLoading {
		a.dataMutex.Unlock()
		return
	}
	a.analytics.quotasLoading = true
	a.analytics.quotasErr = ""
	a.dataMutex.Unlock()

	updateQuotaStatus(a)

	go func() {
		quotas, err := a.client.GetUsageMetrics()

		a.dataMutex.Lock()
		a.analytics.quotasLoading = false
		a.analytics.quotasAt = time.Now()
		if err != nil {
			a.analytics.quotasErr = quotaErrorText(err)
		} else {
			a.analytics.quotas = quotas
			a.analytics.quotasLoaded = true
			a.analytics.quotasErr = ""
		}
		a.dataMutex.Unlock()

		if err != nil {
			log.Printf("[WARN] Failed to load API quotas: %v", err)
		} else {
			log.Printf("[INFO] Loaded %d API quotas", len(quotas))
		}

		a.queueDraw(func() {
			updateQuotaStatus(a)
			updateQuotaTable(a)
		})
	}()
}

// quotaErrorText turns a failed load into the line shown on the sub-screen.
// A rate-limited refusal is named as such: it is the one failure the user can
// do something about, by waiting.
func quotaErrorText(err error) string {
	if api.IsRateLimited(err) {
		return "лимит API — попробуйте позже, R повторить"
	}
	return "не удалось загрузить квоты, R повторить"
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
