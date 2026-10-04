package ui

import (
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"finam-terminal/api"
	"finam-terminal/commodity"
	"finam-terminal/config"
	"finam-terminal/models"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// ConfigureCommodities applies startup settings before Run. A disabled section
// has neither rows nor subscribed symbols and is omitted from tab navigation.
func (a *App) ConfigureCommodities(cfg config.CommoditiesConfig) {
	a.commoditiesEnabled = cfg.Enabled
	a.commodities = nil
	a.commodityFutures = nil
	if cfg.Enabled {
		a.commodities = cfg.Items()
	}
	a.portfolioView.TabbedView.SetCommoditiesEnabled(cfg.Enabled)
	a.restoreCommodityFutures()
	a.portfolioView.TabbedView.CommoditiesTable.SetSelectionChangedFunc(func(_, _ int) {
		if !a.portfolioView.TabbedView.Commodities.rebuilding {
			updateCommodityDetails(a)
			updateStatusBar(a)
		}
	})
}

func (a *App) commodityForSymbol(symbol string) (config.CommodityItem, bool) {
	for _, c := range a.commodities {
		if c.Symbol == symbol {
			return c, true
		}
	}
	return config.CommodityItem{}, false
}

func (a *App) activeCommodityOrderSymbol() string {
	source := a.selectedCommoditySymbol()
	if a.profileOpen {
		source = a.profileSymbol
	}
	return a.commodityOrderSymbol(source)
}

// Never open an order on the international series or an unrelated edited
// ticker. Only the selected source's current nonexpired MOEX binding is allowed.
func (a *App) commodityOrderTarget(symbol string) string {
	target := a.activeCommodityOrderSymbol()
	if target == "" {
		return ""
	}
	source := a.selectedCommoditySymbol()
	if a.profileOpen {
		source = a.profileSymbol
	}
	if symbol == source || symbol == target {
		return target
	}
	return ""
}

func (a *App) commoditiesTabActive() bool {
	return a.commoditiesEnabled && a.portfolioView.TabbedView.ActiveTab == TabCommodities
}

func (a *App) selectedCommoditySymbol() string {
	row, _ := a.portfolioView.TabbedView.CommoditiesTable.GetSelection()
	if row <= 0 || row > len(a.commodities) {
		return ""
	}
	return a.commodities[row-1].Symbol
}

func (a *App) pollCommodityQuotesAsync(manual bool) {
	active := a.commoditiesTabActive()
	if !a.commoditiesEnabled || len(a.commodities) == 0 || (!manual && !active) {
		return
	}
	a.ensureCommodityFutures(manual)
	go func() {
		if a.sweepCommodityQuotes(manual, active) && a.ctx.Err() == nil {
			a.app.QueueUpdateDraw(func() {
				if a.commoditiesTabActive() {
					updateCommoditiesTable(a)
				}
			})
		}
	}()
}

// Fallback is paced and only fills symbols without a live stream. Failures keep
// the previous quote; a rate limit stops automation until an explicit R.
func (a *App) sweepCommodityQuotes(manual, active bool) bool {
	if a.client == nil || !a.commoditiesEnabled || a.ctx.Err() != nil {
		return false
	}
	a.dataMutex.Lock()
	if a.commodityLoading || (!manual && !shouldPollIndexQuotes(active, a.commodityLimited, a.commodityLastPoll, time.Now())) {
		a.dataMutex.Unlock()
		return false
	}
	a.commodityLoading = true
	a.commodityLastPoll = time.Now()
	symbols := a.commodityQuoteSymbolsLocked()
	a.dataMutex.Unlock()
	defer func() { a.dataMutex.Lock(); a.commodityLoading = false; a.dataMutex.Unlock() }()
	live := make(map[string]bool)
	for _, symbol := range a.client.SubscribedSymbols() {
		live[symbol] = true
	}
	attempted := false
	for _, symbol := range symbols {
		if live[symbol] {
			continue
		}
		if attempted {
			timer := time.NewTimer(indexSweepDelay)
			select {
			case <-a.ctx.Done():
				timer.Stop()
				return attempted
			case <-timer.C:
			}
		}
		if a.ctx.Err() != nil {
			return attempted
		}
		attempted = true
		quotes, err := a.client.GetMarketQuotes([]string{symbol})
		if a.ctx.Err() != nil {
			return attempted
		}
		a.dataMutex.Lock()
		if q := quotes[symbol]; q != nil {
			a.commodityQuotes[symbol] = q
		}
		a.dataMutex.Unlock()
		if err != nil {
			log.Printf("[WARN] Commodity comparison quote %s: %v", symbol, err)
			if api.IsRateLimited(err) {
				a.dataMutex.Lock()
				a.commodityLimited = true
				a.dataMutex.Unlock()
				break
			}
		}
	}
	return attempted
}

func updateCommoditiesTable(a *App) {
	view := a.portfolioView.TabbedView.Commodities
	view.rebuilding = true
	table := a.portfolioView.TabbedView.CommoditiesTable
	selected, _ := table.GetSelection()
	table.Clear()
	setCommodityHeaders(table)
	a.dataMutex.RLock()
	title := " Commodities "
	if a.commodityLimited {
		title += "· лимит API, R — обновить "
	}
	if state := a.commodityFuturesLocked(); state == nil {
		if a.selectedIdx < 0 || a.selectedIdx >= len(a.accounts) || a.accounts[a.selectedIdx].ID == "" {
			title += "· нет счёта для MOEX "
		} else {
			title += "· MOEX: ожидается загрузка "
		}
	} else if state.loading {
		title += "· MOEX: загрузка… "
	} else if len(state.errors) > 0 {
		title += fmt.Sprintf("· MOEX: ошибки %d, R — обновить ", len(state.errors))
	}
	table.SetTitle(title)
	for i, c := range a.commodities {
		setCommodityQuoteRow(table, i+1, c.Code, c.Name, a.commodityQuotes[c.Symbol], c.Decimals)
	}
	if len(a.commodities) == 0 {
		table.SetCell(1, 0, tview.NewTableCell("В commodities.json не настроены товары").SetSelectable(false).SetTextColor(tcell.ColorGray))
	}
	a.dataMutex.RUnlock()
	if selected <= 0 || selected > len(a.commodities) {
		selected = 1
	}
	// Select may invoke the details callback, which acquires the data lock.
	table.Select(selected, 0)
	view.rebuilding = false
	updateCommodityDetails(a)
}

func setCommodityQuoteRow(table *tview.Table, row int, ticker, name string, q *models.Quote, decimals int) {
	values := indexRowFromQuote(q)
	if q != nil {
		if price, valid := commodityQuotePrice(q); valid {
			values.price = formatNumber(price, decimals)
		}
		if change, err := parseFloat(q.Change); err == nil && !math.IsNaN(change) && !math.IsInf(change, 0) {
			values.change = formatSignedNumber(change, decimals)
		}
	}
	texts := [...]string{ticker, name, values.price, values.change, values.changePct, values.volume}
	bg := tcell.ColorBlack
	if row%2 == 1 {
		bg = tcell.ColorDarkGray
	}
	for col, text := range texts {
		colour := tcell.ColorWhite
		if col == 0 {
			colour = tcell.ColorLightYellow
		}
		if col == 3 || col == 4 {
			colour = values.colour
		}
		align := tview.AlignRight
		if col < 2 {
			align = tview.AlignLeft
		}
		table.SetCell(row, col, tview.NewTableCell(text).SetAlign(align).
			SetStyle(tcell.StyleDefault.Background(bg).Foreground(colour)))
	}
}

// Details are a memory-only view of the selected source and native stream
// quote. Neither moving the selection nor drawing spends an API request.
func updateCommodityDetails(a *App) {
	view := a.portfolioView.TabbedView.Commodities
	view.Native.Clear()
	setCommodityHeaders(view.Native)
	a.dataMutex.RLock()
	defer a.dataMutex.RUnlock()
	row, _ := view.Sources.GetSelection()
	if row <= 0 || row > len(a.commodities) {
		view.setDetails(commodityDetailData{
			Contract: noIndexData, Expiry: noIndexData, Converted: noIndexData, Delta: noIndexData,
			SourceUpdated: noIndexData, NativeUpdated: noIndexData,
			Status: "Выберите международный инструмент",
		})
		return
	}
	c := a.commodities[row-1]
	now := time.Now()
	future, eligible := a.commodityFutureLocked(c.Symbol, now)
	ticker := noIndexData
	var native *models.Quote
	decimals := c.Decimals
	if eligible {
		// Use the same catalogue display name as History, with metadata as
		// fallback when the startup catalogue does not know the series.
		if a.client != nil {
			ticker = strings.TrimSpace(a.client.GetInstrumentName(future.Symbol))
		}
		if ticker == "" || ticker == noIndexData {
			ticker = strings.TrimSpace(future.Name)
		}
		if ticker == "" {
			ticker = strings.SplitN(future.Symbol, "@", 2)[0]
		}
		native = a.commodityQuotes[future.Symbol]
		decimals = future.Decimals
	}
	setCommodityQuoteRow(view.Native, 1, ticker, c.Name, native, decimals)
	source := a.commodityQuotes[c.Symbol]
	converted, delta := noIndexData, noIndexData
	if nativePrice, nativeOK := commodityQuotePrice(native); eligible && nativeOK {
		fx, _ := commodityQuotePrice(a.commodityQuotes[c.Conversion.FXSymbol])
		if value, valid := commodity.ConvertPrice(nativePrice, c.Conversion, fx); valid {
			converted = formatNumber(value, c.Decimals)
			if sourcePrice, sourceOK := commodityQuotePrice(source); sourceOK {
				difference := value - sourcePrice
				if !math.IsNaN(difference) && !math.IsInf(difference, 0) {
					delta = formatSignedNumber(difference, c.Decimals)
				}
			}
		}
	}
	status := "MOEX: контракт не найден"
	if c.MOEXMask == "" {
		status = "MOEX: связь не настроена"
	} else if eligible {
		status = ""
	} else if state := a.commodityFuturesLocked(); state != nil {
		if message := state.errors[c.Symbol]; message != "" {
			status = "MOEX: " + message
		} else if state.loading {
			status = "MOEX: загрузка…"
		}
	} else {
		status = "MOEX: нет данных"
	}
	contract, expiry := noIndexData, noIndexData
	if eligible {
		contract = tview.Escape(future.Symbol)
		expiry = commodityExpiryDate(future.Expiration, now, c.ExpiryWarningDays)
	}
	view.setDetails(commodityDetailData{
		Contract: contract, Expiry: expiry, Converted: converted, Delta: delta,
		SourceUpdated: commodityQuoteUpdated(source), NativeUpdated: commodityQuoteUpdated(native),
		Status: tview.Escape(status),
	})
}

func commodityExpiryDate(expiry, now time.Time, warningDays int) string {
	if expiry.IsZero() {
		return noIndexData
	}
	colour := "white"
	if warningDays > 0 && expiry.After(now) && expiry.Before(now.AddDate(0, 0, warningDays)) {
		colour = "red"
	}
	return "[" + colour + "]" + expiry.Local().Format("02.01.2006") + "[-]"
}

func commodityQuoteUpdated(q *models.Quote) string {
	if q == nil || q.Timestamp.IsZero() {
		return noIndexData
	}
	return q.Timestamp.Local().Format("02.01.2006 15:04:05")
}

// Commodity profiles need market data only; asset and trading-parameter RPCs
// require an account, while these continuous series do not.
func (a *App) loadCommodityProfileSync(c config.CommodityItem, timeframeIdx int) *models.InstrumentProfile {
	profile := &models.InstrumentProfile{Symbol: c.Symbol, Details: &models.AssetDetails{
		Ticker: c.Code, Name: c.Name, MIC: strings.SplitN(c.Symbol, "@", 2)[1], Type: "FUTURES",
	}}
	var wg sync.WaitGroup
	wg.Go(func() {
		quotes, err := a.client.GetMarketQuotes([]string{c.Symbol})
		if err != nil {
			log.Printf("[WARN] Commodity profile quote %s: %v", c.Symbol, err)
		}
		profile.Quote = quotes[c.Symbol]
	})
	wg.Go(func() {
		now := time.Now()
		bars, err := a.client.GetBars("", c.Symbol, profileTimeframeEnums[timeframeIdx], now.Add(-profileTimeframeDurations[timeframeIdx]), now)
		if err != nil {
			log.Printf("[WARN] Commodity profile bars %s: %v", c.Symbol, err)
		}
		profile.Bars = bars
	})
	wg.Wait()
	return profile
}

func commodityQuotePrice(q *models.Quote) (float64, bool) {
	if q == nil {
		return 0, false
	}
	price, err := parseFloat(q.Last)
	return price, err == nil && price > 0 && !math.IsNaN(price) && !math.IsInf(price, 0)
}
