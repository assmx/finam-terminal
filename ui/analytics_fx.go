package ui

import (
	"log"
	"time"

	"finam-terminal/analytics"
	"finam-terminal/api"
	"finam-terminal/models"
)

// fxRateTTL is how long a rate — or a failed attempt at one, and a bond's face
// currency lookup likewise — stands before the schedule asks again. Rates move
// about a percent a day, and the broker itself values a dollar balance all
// evening at the day's last MOEX trade, so ten minutes is ample (spec,
// decision 4). A variable so tests can move it.
var fxRateTTL = 10 * time.Minute

// fxState is what the overview converts foreign holdings with: the rates, and
// the bookkeeping that keeps asking for them within budget.
//
// It belongs to no account. A rate is a rate whichever account asks, so a
// second account holding dollars reuses the first one's.
type fxState struct {
	// rates are keyed by currency code, as GetFXRates returns them. A rate is
	// replaced by a newer one and never dropped: an old rate on screen is
	// marked stale, which says more than no rate.
	rates map[string]models.FXRate

	// fetchedAt is the last successful answer per currency, askedAt the last
	// attempt, answered or not. The schedule waits a TTL after an attempt, so a
	// failure is not retried at once; R waits only for a success.
	fetchedAt map[string]time.Time
	askedAt   map[string]time.Time
	loading   bool

	// faceAskedAt is the last calendar lookup per bond, for the same reason.
	faceAskedAt map[string]time.Time
	faceLoading bool

	// limited latches for the session once the broker refuses a rate or a
	// calendar for the rate limit. The schedule stops; R still works.
	limited bool
}

func newFXState() fxState {
	return fxState{
		rates:       make(map[string]models.FXRate),
		fetchedAt:   make(map[string]time.Time),
		askedAt:     make(map[string]time.Time),
		faceAskedAt: make(map[string]time.Time),
	}
}

// due reports whether something last touched at last is due again at now.
func due(last, now time.Time) bool {
	return last.IsZero() || now.Sub(last) >= fxRateTTL
}

// overviewOnScreen reports whether the overview is what the user is looking at.
func (a *App) overviewOnScreen() bool {
	return a.onAnalyticsTab() && a.analyticsView().ActiveScreen == AnalyticsOverview
}

// ensureCurrencyData is the scheduled path, run on entering the overview and on
// every tick while it is on screen. It asks for what the active account's
// currency figures are missing and nothing else: a rate the account needs and
// nobody asked for within fxRateTTL, a bond face only a calendar can settle
// and not looked up within it — and, once the broker has refused for the rate
// limit, nothing at all.
func (a *App) ensureCurrencyData() {
	if !a.overviewOnScreen() {
		return
	}
	a.loadCurrencyData(false)
}

// refreshCurrencyData is R on the overview. It asks again for any rate that
// has not been fetched successfully within fxRateTTL, and for every bond whose
// face currency is still open, whatever the schedule and the latch say. A
// fresh rate is not asked for again: R is a way past a failure, not a way to
// spend the budget.
func (a *App) refreshCurrencyData() {
	a.loadCurrencyData(true)
}

// loadCurrencyData decides what is due and starts the loaders for it.
func (a *App) loadCurrencyData(manual bool) {
	in, ok := a.currencyInput()
	if !ok {
		return
	}
	currencies := analytics.RatesToFetch(in)
	bonds := analytics.BondsNeedingFace(in)
	if len(currencies) == 0 && len(bonds) == 0 {
		return
	}

	now := time.Now()

	a.dataMutex.Lock()
	fx := &a.analytics.fx
	if fx.limited && !manual {
		a.dataMutex.Unlock()
		return
	}

	var dueRates []string
	if !fx.loading {
		for _, c := range currencies {
			last := fx.askedAt[c]
			if manual {
				last = fx.fetchedAt[c]
			}
			if due(last, now) {
				dueRates = append(dueRates, c)
				fx.askedAt[c] = now
			}
		}
		fx.loading = len(dueRates) > 0
	}

	var dueBonds []string
	if !fx.faceLoading {
		for _, s := range bonds {
			if manual || due(fx.faceAskedAt[s], now) {
				dueBonds = append(dueBonds, s)
				fx.faceAskedAt[s] = now
			}
		}
		fx.faceLoading = len(dueBonds) > 0
	}
	a.dataMutex.Unlock()

	if len(dueRates) > 0 {
		go a.fetchRates(dueRates)
	}
	if len(dueBonds) > 0 {
		go a.fetchFaceCurrencies(dueBonds)
	}
}

// currencyInput is the active account as the currency rules read it. The
// instruments come from the free caches, as on the overview.
func (a *App) currencyInput() (analytics.StructureInput, bool) {
	account, ok := a.activeAccount()
	if !ok || account.LoadError != "" || a.client == nil {
		return analytics.StructureInput{}, false
	}

	a.dataMutex.RLock()
	positions := append([]models.Position(nil), a.positions[account.ID]...)
	quotes := a.quotes[account.ID]
	a.dataMutex.RUnlock()

	return analytics.StructureInput{
		Positions:   positions,
		Quotes:      quotes,
		Cash:        account.Cash,
		Instruments: instrumentMoney(a, positions),
	}, true
}

// fetchRates runs one GetFXRates off the event loop and files the answer. A
// result that arrives after the application stopped is written nowhere.
func (a *App) fetchRates(currencies []string) {
	rates, err := a.client.GetFXRates(currencies)
	if a.ctx.Err() != nil {
		return
	}

	now := time.Now()
	limited := api.IsRateLimited(err)

	a.dataMutex.Lock()
	fx := &a.analytics.fx
	fx.loading = false
	for code, rate := range rates {
		fx.rates[code] = rate
		fx.fetchedAt[code] = now
	}
	if limited {
		fx.limited = true
	}
	a.dataMutex.Unlock()

	switch {
	case limited:
		log.Printf("[WARN] Exchange rates stopped for the session: rate limited")
	case err != nil:
		log.Printf("[WARN] Exchange rates not loaded: %v", err)
	}

	a.queueDraw(a.redrawOverviewIfShown)
}

// fetchFaceCurrencies looks up bond face currencies one at a time, paced like
// the payout calendars, off the event loop. The answers land in the API
// layer's cache, where the next redraw reads them; a rate limit ends the walk
// and latches the schedule.
func (a *App) fetchFaceCurrencies(symbols []string) {
	var limited bool
	for i, symbol := range symbols {
		if i > 0 && payoutPace > 0 {
			select {
			case <-a.ctx.Done():
				return
			case <-time.After(payoutPace):
			}
		}
		if a.ctx.Err() != nil {
			return
		}

		if _, err := a.client.GetBondFaceCurrency(symbol); err != nil {
			if api.IsRateLimited(err) {
				limited = true
				log.Printf("[WARN] Bond face currencies stopped at %s: rate limited", symbol)
				break
			}
			log.Printf("[WARN] Face currency of %s not known yet: %v", symbol, err)
		}
	}
	if a.ctx.Err() != nil {
		return
	}

	a.dataMutex.Lock()
	a.analytics.fx.faceLoading = false
	if limited {
		a.analytics.fx.limited = true
	}
	a.dataMutex.Unlock()

	a.queueDraw(a.redrawOverviewIfShown)
}

// redrawOverviewIfShown redraws the overview when a loader lands, and only if
// anyone is looking at it.
func (a *App) redrawOverviewIfShown() {
	if a.overviewOnScreen() {
		updateAnalyticsOverview(a)
	}
}

// fxStatusLocked is the overview's status line about its currency data. The
// caller holds the read lock.
func (a *App) fxStatusLocked() string {
	fx := a.analytics.fx
	switch {
	case fx.limited:
		return "[red]лимит API: курсы не обновляются, R — вручную[-]"
	case fx.loading || fx.faceLoading:
		return "[yellow]курсы: загрузка…[-]"
	}
	return ""
}
