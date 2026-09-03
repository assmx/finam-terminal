package ui

import (
	"log"
	"time"

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

	byAccount map[string]*analyticsAccountData
}

// analyticsAccountData is the per-account slot the second track fills.
type analyticsAccountData struct{}

func newAnalyticsState() *analyticsState {
	return &analyticsState{byAccount: make(map[string]*analyticsAccountData)}
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
