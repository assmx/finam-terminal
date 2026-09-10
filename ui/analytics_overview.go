package ui

import (
	"fmt"
	"strings"
	"time"

	"finam-terminal/analytics"
	"finam-terminal/models"

	"github.com/rivo/tview"
)

// levelColor maps a risk level to a tview colour tag.
var levelColor = map[analytics.Level]string{
	analytics.LevelGood: "green",
	analytics.LevelWarn: "yellow",
	analytics.LevelBad:  "red",
}

// notAvailable is what a figure the account does not report looks like. It is
// grey rather than blank so the row still reads as a row, and it is never a
// zero: "not reported" and "zero roubles" are different facts.
const notAvailable = "[gray]Н/Д[-]"

// positionValue is the single place a position's market value is computed for
// display. The Positions tab's Value column and the Analytics overview both go
// through it, so the two screens cannot disagree about what a holding is
// worth.
//
// The face value stays 0, which keeps the plain price × quantity form. Turning
// on the percent-of-par formula for bonds is a change here and nowhere else —
// see analytics.PositionValue.
func positionValue(pos models.Position, quote *models.Quote) (float64, bool) {
	last := ""
	if quote != nil {
		last = quote.Last
	}
	return analytics.PositionValue(pos.Quantity, last, pos.CurrentPrice, 0)
}

// onAnalyticsTab reports whether the Analytics tab is the one on screen.
// Redraws on the five-second tick are gated on it: an unwatched tab costs
// nothing.
func (a *App) onAnalyticsTab() bool {
	return a.portfolioView.TabbedView.ActiveTab == TabAnalytics
}

// updateAnalyticsOverview redraws the seven overview panels.
//
// It reads only state already in memory — positions and quotes from the same
// five-second tick that feeds the Positions tab, the account's own valuation
// and margin report, the instrument types from the startup asset cache, the
// sectors from the index composition, the instruments' money facts from the
// caches the lot resolution fills, and the rates the overview keeps — and
// issues no request of its own. That is what makes it safe to call on every
// tick; fetching rates and face currencies is scheduled elsewhere.
//
// What it hands each panel is a function rather than a string: the panel lays
// its rows out to its own width, which tview only settles while drawing.
func updateAnalyticsOverview(app *App) {
	view := app.portfolioView.TabbedView.Analytics

	app.dataMutex.RLock()
	if app.selectedIdx < 0 || app.selectedIdx >= len(app.accounts) {
		app.dataMutex.RUnlock()
		view.setOverviewStatic(muted("Счёт не выбран"), "", "", "", "", "", "")
		return
	}

	account := app.accounts[app.selectedIdx]
	positions := app.positions[account.ID]
	quotes := app.quotes[account.ID]
	sectors, sectorsKnown, sectorNote := app.sectorMapLocked()
	rates := app.fxRatesLocked()
	fxStatus := app.fxStatusLocked()
	app.dataMutex.RUnlock()

	view.OverviewStatus.SetText(fxStatus)

	if account.LoadError != "" {
		// Once at the top of each column, where the eye starts.
		message := "[red]" + brokerDataError() + "[-]"
		view.setOverviewStatic(message, "", "", "", message, "", "")
		return
	}

	now := time.Now()
	instruments := instrumentMoney(app, positions)

	allocation := analytics.Structure(analytics.StructureInput{
		Positions:   positions,
		Quotes:      quotes,
		Cash:        account.Cash,
		Types:       instrumentTypes(app, positions),
		Sectors:     sectors,
		Instruments: instruments,
		Rates:       rates,
		Now:         now,
	})

	risk := analytics.RiskMetrics(analytics.RiskInput{
		Account:       account,
		GrossExposure: allocation.Exposure,
	})

	worth := analytics.Valuation(analytics.ValuationInput{
		Account:     account,
		Positions:   positions,
		Instruments: instruments,
		Rates:       rates,
	})

	view.setOverview(
		func(w int) string { return renderStructure(allocation, w) },
		func(w int) string { return renderCurrencies(allocation, now, w) },
		func(w int) string { return renderSectors(allocation, sectorsKnown, sectorNote, w) },
		app.sinceOpenSummaryRenderer(account),
		func(w int) string { return renderValuation(worth, w) },
		func(w int) string { return renderRisk(account, risk, allocation, worth.FortsMargin, w) },
		func(w int) string { return renderConcentration(allocation, w) },
	)
}

// setOverview installs a renderer in each of the seven panels — the left
// column top to bottom, then the right — and resizes them to what they now
// hold, so the frames follow the content rather than the other way round.
func (av *AnalyticsView) setOverview(structure, currencies, sectors, sinceOpen, valuation, risk, concentration func(int) string) {
	av.Structure.SetRender(structure)
	av.Currencies.SetRender(currencies)
	av.Sectors.SetRender(sectors)
	av.SinceOpen.SetRender(sinceOpen)
	av.Valuation.SetRender(valuation)
	av.Risk.SetRender(risk)
	av.Concentration.SetRender(concentration)
}

// setOverviewStatic is setOverview for the states with nothing to lay out: no
// account, or a broker that would not answer.
func (av *AnalyticsView) setOverviewStatic(structure, currencies, sectors, sinceOpen, valuation, risk, concentration string) {
	av.setOverview(
		func(int) string { return structure },
		func(int) string { return currencies },
		func(int) string { return sectors },
		func(int) string { return sinceOpen },
		func(int) string { return valuation },
		func(int) string { return risk },
		func(int) string { return concentration },
	)
}

// fxRatesLocked copies the rates the overview converts with. The caller must
// hold the read lock; the copy is what lets the calculation run after it is
// released.
func (a *App) fxRatesLocked() map[string]models.FXRate {
	rates := make(map[string]models.FXRate, len(a.analytics.fx.rates))
	for code, rate := range a.analytics.fx.rates {
		rates[code] = rate
	}
	return rates
}

// instrumentMoney gathers what the API caches know about each held
// instrument's money: its currency and a bond's face (GetAsset), the per-piece
// value (GetAssetParams), and a face currency a calendar named. All three are
// memory reads filled by requests the terminal already makes, so this stays
// free on a tick.
func instrumentMoney(app *App, positions []models.Position) map[string]analytics.Instrument {
	if app.client == nil || len(positions) == 0 {
		return nil
	}

	instruments := make(map[string]analytics.Instrument, len(positions))
	for _, p := range positions {
		var inst analytics.Instrument
		if cur, ok := app.client.GetInstrumentCurrency(p.Symbol); ok {
			inst.Quote = cur.Quote
			inst.FaceValue = cur.FaceValue
		}
		if unit, ok := app.client.GetUnitValue(p.Symbol); ok {
			inst.Unit = unit
		}
		if inst.FaceValue > 0 {
			if face, ok := app.client.BondFaceCurrencyCached(p.Symbol); ok {
				inst.Face = face
				inst.FaceChecked = true
			}
		}
		instruments[p.Symbol] = inst
	}
	return instruments
}

// sectorMapLocked builds the symbol → sector map from the index composition,
// and the note explaining its absence. The caller must hold the read lock.
//
// A nil map means "not loaded", which the renderer says out loud: an
// all-Прочее sector line would look like a real answer.
func (a *App) sectorMapLocked() (map[string]string, bool, string) {
	if len(a.indexConstituents) == 0 {
		if a.indexLoadErr != "" {
			return nil, false, "состав индекса недоступен"
		}
		if a.indexLoading {
			return nil, false, "состав индекса загружается…"
		}
		return nil, false, "состав индекса недоступен"
	}

	sectors := make(map[string]string, len(a.indexConstituents)*2)
	for _, c := range a.indexConstituents {
		if c.Sector == "" {
			continue
		}
		sectors[c.Symbol] = c.Sector
		sectors[c.Ticker] = c.Sector
	}
	return sectors, true, ""
}

// instrumentTypes resolves the type of every held instrument from the asset
// cache. GetInstrumentType is a memory read, so this stays free on a tick.
func instrumentTypes(app *App, positions []models.Position) map[string]string {
	if app.client == nil || len(positions) == 0 {
		return nil
	}

	types := make(map[string]string, len(positions))
	for _, p := range positions {
		if t := app.client.GetInstrumentType(p.Symbol); t != "" {
			types[p.Symbol] = t
		}
	}
	return types
}

// renderStructure draws the breakdown by instrument type, the cash line, the
// total, and the counters for anything left out.
func renderStructure(a analytics.Allocation, width int) string {
	var b strings.Builder

	if !a.Valid {
		b.WriteString(notAvailable + " — в портфеле нет оценённых активов\n")
		return b.String()
	}

	for _, g := range a.Groups {
		fmt.Fprintf(&b, "%s\n", shareRow(g.Name, g.Value, g.Share, width))
	}
	if a.Cash > 0 || len(a.Groups) == 0 {
		fmt.Fprintf(&b, "%s\n", shareRow(analytics.GroupCash, a.Cash, a.CashShare, width))
	}

	fmt.Fprintf(&b, "%s\n", muted(strings.Repeat("─", width)))
	fmt.Fprintf(&b, "[white::b]%s[-:-:-]\n",
		totalRow("Итого", formatNumber(a.Base, 0)+" "+a.BaseCurrency, width))

	if a.Skipped > 0 {
		fmt.Fprintf(&b, "[yellow]без цены: %d[-]\n", a.Skipped)
	}
	if a.NoRateCount > 0 {
		fmt.Fprintf(&b, "[yellow]в других валютах: %d[-]\n", a.NoRateCount)
	}

	return b.String()
}

// unresolvedFaceLabel names the row of bonds whose face is in another
// currency than they settle in, while the calendar that names it is awaited.
const unresolvedFaceLabel = "номинал в валюте"

// renderCurrencies draws the breakdown by currency: one row per currency with
// its share of the same base the structure uses, then a grey line for each
// foreign currency with its amount in its own money, the rate and when the rate
// was quoted, then the currencies without a rate in yellow, and the counters of
// anything whose currency is not settled.
func renderCurrencies(a analytics.Allocation, now time.Time, width int) string {
	if len(a.Currencies) == 0 {
		return muted("нет активов") + "\n"
	}

	var b strings.Builder

	// The share rows first, as one block, so their bars line up.
	for _, row := range a.Currencies {
		switch {
		case row.Unresolved:
			fmt.Fprintf(&b, "%s\n", shareRow(unresolvedFaceLabel, row.Value, row.Share, width))
		case row.HasRate:
			fmt.Fprintf(&b, "%s\n", shareRow(row.Currency, row.Value, row.Share, width))
		}
	}

	for _, row := range a.Currencies {
		switch {
		case row.Unresolved:
			b.WriteString(muted("валюта номинала уточняется") + "\n")
		case !row.HasRate:
			fmt.Fprintf(&b, "[yellow]%s %s — нет курса[-]\n", tview.Escape(row.Currency), formatNumber(row.Native, 2))
		case row.Currency != a.BaseCurrency:
			b.WriteString(currencyDetail(row, now) + "\n")
		}
	}

	if a.UnknownCurrencyCount > 0 {
		fmt.Fprintf(&b, "[yellow]валюта неизвестна: %d[-]\n", a.UnknownCurrencyCount)
	}
	if a.FaceUncheckedCount > 0 {
		fmt.Fprintf(&b, "[yellow]валюта номинала не проверена: %d[-]\n", a.FaceUncheckedCount)
	}

	return b.String()
}

// currencyDetail is the grey line under the rows for one foreign currency: the
// amount in its own money, the rate that converted it and the rate's time.
func currencyDetail(row analytics.CurrencyRow, now time.Time) string {
	line := muted(fmt.Sprintf("%s %s × %.4f", tview.Escape(row.Currency), formatNumber(row.Native, 2), row.Rate.Value))
	if label := rateTimeLabel(row.Rate.At, now); label != "" {
		line += muted(" · ") + label
	}
	return line
}

// rateTimeLabel says when a rate was quoted: the time alone for today, with the
// date for another day, and in yellow as "курс на …" once it is too old to
// pass for current — a weekend rate must not read as this morning's. A rate
// without a time gets no label.
func rateTimeLabel(at, now time.Time) string {
	if at.IsZero() {
		return ""
	}
	local := at.Local()
	if analytics.RateIsStale(at, now) {
		return "[yellow]курс на " + local.Format("02.01 15:04") + "[-]"
	}
	y1, m1, d1 := local.Date()
	y2, m2, d2 := now.Local().Date()
	if y1 == y2 && m1 == m2 && d1 == d2 {
		return muted(local.Format("15:04"))
	}
	return muted(local.Format("02.01 15:04"))
}

// totalRow is a label on the left and its figure hard against the right edge —
// the shape a sum under a rule wants, with no leader between them.
func totalRow(label, value string, width int) string {
	gap := width - tview.TaggedStringWidth(label) - tview.TaggedStringWidth(value)
	if gap < 1 {
		return label + " " + value
	}
	return label + strings.Repeat(" ", gap) + value
}

// renderSectors draws the sector breakdown, or says why there is none.
func renderSectors(a analytics.Allocation, sectorsKnown bool, sectorNote string, width int) string {
	switch {
	case !sectorsKnown:
		return muted(sectorNote)
	case len(a.Sectors) == 0:
		return muted("нет данных по секторам")
	}

	var b strings.Builder
	for _, s := range a.Sectors {
		fmt.Fprintf(&b, "%s\n", shareRow(s.Name, s.Value, s.Share, width))
	}
	return b.String()
}

// valuationShareWidth is the column the percentages of the two result rows
// share, so they line up under each other whatever the amounts beside them.
const valuationShareWidth = 8

// renderValuation draws what the account is worth and what it has made: the
// value at the start of the day and now, the day's result, and the result on
// the positions still open.
func renderValuation(v analytics.Worth, width int) string {
	var b strings.Builder

	opening := notAvailable
	if v.Opening.Valid {
		opening = formatAmount(v.Opening.Value)
	}
	current := notAvailable
	if v.Current.Valid {
		current = fmt.Sprintf("[white::b]%s[-:-:-]", formatAmount(v.Current.Value))
	}

	fmt.Fprintf(&b, "%s\n", leaderRow("На начало дня", opening, width))
	fmt.Fprintf(&b, "%s\n", leaderRow("Текущая", current, width))
	fmt.Fprintf(&b, "%s\n", leaderRow("Прибыль за день", resultWithShare(v.Daily, v.DailyShare), width))
	fmt.Fprintf(&b, "%s\n", leaderRow("Прибыль по позициям", resultWithShare(v.Unrealized, v.UnrealizedShare), width))

	// Said out loud, like "без цены" in the structure panel: a day's result
	// that silently left positions out would read as the whole day.
	if v.DailyUnreported > 0 {
		fmt.Fprintf(&b, "[yellow]без дневного P&L: %d[-]\n", v.DailyUnreported)
	}

	return b.String()
}

// resultWithShare is a signed amount followed by its percentage in a
// fixed-width column. A result the account does not report is one Н/Д; a
// result without a base keeps its amount and marks the percentage alone.
func resultWithShare(amount, share analytics.Metric) string {
	if !amount.Valid {
		return notAvailable
	}
	percent := notAvailable
	if share.Valid {
		percent = fmt.Sprintf("[%s]%s[-]", amountTag(share.Value), signedPercent(share.Value))
	}
	return signedAmount(amount.Value) + " " + padTagged(percent, valuationShareWidth)
}

// renderRisk draws what the account reports about its money and margin, and
// the three figures derived from it. The account's value itself is in the
// valuation panel above; printing it here too would put the same number in
// two frames a few rows apart.
func renderRisk(account models.AccountInfo, r analytics.Risk, a analytics.Allocation, forts analytics.Metric, width int) string {
	var b strings.Builder

	// Two columns are given up to the level bullets below, so the plain rows
	// indent to match and every value in the panel ends in the same place.
	rowWidth := width - 2

	// A balance in another currency is shown once, in the currency panel; a
	// loan in one stays here, because it is a risk rather than a holding.
	for _, c := range a.Borrowed {
		fmt.Fprintf(&b, "  %s\n", leaderRow(
			fmt.Sprintf("[red]заём %s[-]", tview.Escape(c.Currency)),
			fmt.Sprintf("[red]%s[-]", formatNumber(c.Amount, 2)),
			rowWidth))
	}

	if r.HasMarginData {
		fmt.Fprintf(&b, "  %s\n", leaderRow("Доступный кэш", formatNumber(account.AvailableCash, 2), rowWidth))
		switch r.Kind {
		case "MC":
			fmt.Fprintf(&b, "  %s\n", leaderRow("Начальная маржа", formatNumber(account.InitialMargin, 2), rowWidth))
			fmt.Fprintf(&b, "  %s\n", leaderRow("Мин. маржа", formatNumber(account.MaintenanceMargin, 2), rowWidth))
			// A unified account's own report has no line for the collateral
			// its FORTS positions tie up; the positions carry it themselves.
			if forts.Valid {
				fmt.Fprintf(&b, "  %s\n", leaderRow("ГО FORTS", formatNumber(forts.Value, 2), rowWidth))
			}
		case "FORTS":
			fmt.Fprintf(&b, "  %s\n", leaderRow("Резерв ГО", formatNumber(account.MoneyReserved, 2), rowWidth))
		}
	}

	// The blank line separates reported figures from derived ones, so there is
	// nothing to separate when the account reported none.
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	writeMetric(&b, "Использование маржи", r.Utilization, analytics.UtilizationLevel, rowWidth)
	writeMetric(&b, "Запас до маржин-колла", r.Cushion, analytics.CushionLevel, rowWidth)
	writeLeverage(&b, r.Leverage, rowWidth)

	return b.String()
}

// renderConcentration draws the largest holdings and how many positions there
// are in total.
func renderConcentration(a analytics.Allocation, width int) string {
	var b strings.Builder

	if len(a.Top) == 0 {
		b.WriteString(muted("нет позиций") + "\n")
	} else {
		for _, h := range a.Top {
			fmt.Fprintf(&b, "%s\n", shareRow(h.Ticker, h.Value, h.Share, width))
		}
	}

	fmt.Fprintf(&b, "%s\n", muted(fmt.Sprintf("Позиций: %d", a.PositionCount)))
	return b.String()
}

// writeMetric draws one coloured percentage, or Н/Д when the account does not
// report what it is computed from.
//
// The level is carried by a bullet as well as by the colour, so the reading
// survives a monochrome terminal and a red-green colour blindness.
func writeMetric(b *strings.Builder, label string, m analytics.Metric, level func(float64) analytics.Level, width int) {
	if !m.Valid {
		fmt.Fprintf(b, "  %s\n", leaderRow(label, notAvailable, width))
		return
	}
	tag := levelColor[level(m.Value)]
	fmt.Fprintf(b, "[%s]●[-] %s\n", tag,
		leaderRow(label, fmt.Sprintf("[%s::b]%s[-:-:-]", tag, formatPercent(m.Value)), width))
}

// writeLeverage draws the leverage multiple, which carries no bullet and no
// colour: unlike margin use, there is no threshold that is right for every
// strategy.
func writeLeverage(b *strings.Builder, m analytics.Metric, width int) {
	if !m.Valid {
		fmt.Fprintf(b, "  %s\n", leaderRow("Плечо", notAvailable, width))
		return
	}
	fmt.Fprintf(b, "  %s\n", leaderRow("Плечо", fmt.Sprintf("%.2fx", m.Value), width))
}

// formatPercent renders a 0..1 share as a percentage.
func formatPercent(share float64) string {
	return fmt.Sprintf("%.1f%%", share*100)
}
