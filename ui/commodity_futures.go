package ui

import (
	"log"
	"time"

	"finam-terminal/api"
	"finam-terminal/commodity"
	"finam-terminal/config"
	"finam-terminal/models"
)

type commodityFuturesState struct {
	bindings  map[string]models.FutureContract
	errors    map[string]string
	updatedAt map[string]time.Time
	askedAt   map[string]time.Time
	loading   bool
}

// All helpers suffixed Locked share the application's data lock.
// Selected contracts and market quotes are account-independent.
func (a *App) commodityFuturesLocked() *commodityFuturesState {
	return a.commodityFutures
}

func (a *App) commodityFutureLocked(source string, now time.Time) (models.FutureContract, bool) {
	state := a.commodityFuturesLocked()
	if state == nil {
		return models.FutureContract{}, false
	}
	c, ok := a.commodityForSymbol(source)
	if !ok || c.MOEXMask == "" {
		return models.FutureContract{}, false
	}
	// A failed rollover leaves the last selected contract usable until expiry.
	// roll_days only decides when to refresh, never substitutes a cached series.
	contract := [1]models.FutureContract{state.bindings[source]}
	return commodity.SelectFuture(contract[:], now, 0)
}

func (a *App) commodityOrderSymbol(source string) string {
	a.dataMutex.RLock()
	defer a.dataMutex.RUnlock()
	if a.selectedIdx < 0 || a.selectedIdx >= len(a.accounts) || a.accounts[a.selectedIdx].ID == "" {
		return ""
	}
	future, ok := a.commodityFutureLocked(source, time.Now())
	if !ok {
		return ""
	}
	return future.Symbol
}

func (a *App) refreshCommodityFutureSelection() {
	if !a.commoditiesTabActive() {
		return
	}
	// Bindings are replaced only by successful discovery. Re-declaring the
	// desired set also removes a contract that expired since the last tick.
	a.recomputeStreamSymbols()
	if a.profileOpen && a.profilePanel.readOnly {
		a.profilePanel.SetOrderSymbol(a.activeCommodityOrderSymbol())
	}
	updateCommoditiesTable(a)
	updateStatusBar(a)
}

// restoreCommodityFutures reads the last successful binding without network
// I/O. A stale nonexpired contract remains usable while its refresh runs.
// User configuration remains untouched.
func (a *App) restoreCommodityFutures() {
	if !a.commoditiesEnabled || a.client == nil {
		return
	}
	for _, c := range a.commodities {
		if c.MOEXMask == "" {
			continue
		}
		contract, updatedAt, found := a.client.CachedMOEXFuture(c.MOEXMask)
		if !found {
			continue
		}
		state := a.ensureCommodityFuturesLocked()
		state.bindings[c.Symbol] = contract
		state.updatedAt[c.Symbol] = updatedAt
	}
}

func (a *App) ensureCommodityFuturesLocked() *commodityFuturesState {
	state := a.commodityFutures
	if state == nil {
		state = &commodityFuturesState{}
		a.commodityFutures = state
	}
	if state.bindings == nil {
		state.bindings = make(map[string]models.FutureContract)
	}
	if state.errors == nil {
		state.errors = make(map[string]string)
	}
	if state.updatedAt == nil {
		state.updatedAt = make(map[string]time.Time)
	}
	if state.askedAt == nil {
		state.askedAt = make(map[string]time.Time)
	}
	return state
}

func (a *App) ensureCommodityFutures(manual bool) {
	if !a.commoditiesTabActive() {
		return
	}
	a.refreshCommodityFutureSelection()
	a.loadCommodityFutures(manual, time.Now())
}

// The normal application tick maintains globally used/cached contracts even
// when another tab is visible, using the selected account only for transport.
func (a *App) refreshCommodityFuturesDaily(now time.Time) {
	if !a.commoditiesEnabled {
		return
	}
	a.dataMutex.RLock()
	used := a.commodityFutures != nil
	a.dataMutex.RUnlock()
	if used {
		a.loadCommodityFutures(false, now)
	}
	a.refreshCommodityFutureSelection()
}

// Failures wait until the next local date or explicit R. A successful binding
// is refreshed daily, or sooner if it reaches its configured roll threshold.
func (a *App) loadCommodityFutures(manual bool, now time.Time) {
	if a.client == nil || a.ctx.Err() != nil {
		return
	}
	a.dataMutex.Lock()
	if a.selectedIdx < 0 || a.selectedIdx >= len(a.accounts) || a.accounts[a.selectedIdx].ID == "" {
		a.dataMutex.Unlock()
		return
	}
	accountID := a.accounts[a.selectedIdx].ID
	state := a.ensureCommodityFuturesLocked()
	if state.loading || (!manual && config.SameLocalDate(a.commodityMetadataLimitedAt, now)) {
		a.dataMutex.Unlock()
		return
	}
	var pending []config.CommodityItem
	for _, c := range a.commodities {
		if c.MOEXMask == "" {
			continue
		}
		if !manual {
			contract := [1]models.FutureContract{state.bindings[c.Symbol]}
			_, eligible := commodity.SelectFuture(contract[:], now, c.RollDays)
			asked := state.askedAt[c.Symbol]
			if config.SameLocalDate(asked, now) {
				// An earlier daily attempt must not hide a threshold crossed
				// later today. Once attempted past that threshold, wait for R
				// or the next local date.
				_, eligibleWhenAsked := commodity.SelectFuture(contract[:], asked, c.RollDays)
				if eligible || !eligibleWhenAsked {
					continue
				}
			}
			if config.SameLocalDate(state.updatedAt[c.Symbol], now) && eligible {
				continue
			}
		}
		pending = append(pending, c)
		state.askedAt[c.Symbol] = now
	}
	if len(pending) == 0 {
		a.dataMutex.Unlock()
		return
	}
	state.loading = true
	a.dataMutex.Unlock()
	go func() {
		defer func() {
			a.dataMutex.Lock()
			state.loading = false
			a.dataMutex.Unlock()
			a.redrawCommodityFutures()
		}()
		for _, c := range pending {
			if a.ctx.Err() != nil {
				return
			}
			contract, err := a.client.GetMOEXFuture(accountID, c.MOEXMask, c.RollDays)
			if a.ctx.Err() != nil {
				return
			}
			a.dataMutex.Lock()
			if err == nil {
				state.bindings[c.Symbol] = contract
			}
			if err != nil {
				state.errors[c.Symbol] = err.Error()
			} else {
				delete(state.errors, c.Symbol)
				state.updatedAt[c.Symbol] = now
			}
			limited := api.IsRateLimited(err)
			if limited {
				a.commodityLimited = true
				a.commodityMetadataLimitedAt = now
			}
			a.dataMutex.Unlock()
			if err != nil {
				log.Printf("[WARN] Commodity %s MOEX contract %s: %v", c.Code, c.MOEXMask, err)
			}
			a.redrawCommodityFutures()
			if limited {
				return
			}
		}
	}()
}

func (a *App) redrawCommodityFutures() {
	if a.ctx.Err() != nil {
		return
	}
	a.app.QueueUpdateDraw(func() {
		if a.ctx.Err() != nil || !a.commoditiesTabActive() {
			return
		}
		a.refreshCommodityFutureSelection()
		updateCommoditiesTable(a)
		updateStatusBar(a)
	})
}

// commodityQuoteSymbolsLocked returns the comparison's legs in priority order:
// all international sources, then their selected MOEX contracts, then FX.
func (a *App) commodityQuoteSymbolsLocked() []string {
	symbols := make([]string, 0, 2*len(a.commodities)+1)
	seen := make(map[string]bool, cap(symbols))
	add := func(symbol string) {
		if symbol != "" && !seen[symbol] {
			symbols = append(symbols, symbol)
			seen[symbol] = true
		}
	}
	for _, c := range a.commodities {
		add(c.Symbol)
	}
	now := time.Now()
	for _, c := range a.commodities {
		if future, ok := a.commodityFutureLocked(c.Symbol, now); ok {
			add(future.Symbol)
		}
	}
	for _, c := range a.commodities {
		if _, ok := a.commodityFutureLocked(c.Symbol, now); ok {
			add(c.Conversion.FXSymbol)
		}
	}
	return symbols
}
