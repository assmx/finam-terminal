package ui

import (
	"fmt"
	"strings"

	"finam-terminal/analytics"
	"finam-terminal/models"

	"github.com/rivo/tview"
)

// barWidth is how many cells a share bar occupies. Ten keeps a whole row
// inside 40 columns, which is what each overview column gets on an 80-column
// terminal.
const barWidth = 10

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

// updateAnalyticsOverview redraws both overview columns.
//
// It reads only state already in memory — positions and quotes from the same
// five-second tick that feeds the Positions tab, the account's own margin
// report, the instrument types from the startup asset cache and the sectors
// from the index composition — and issues no request of its own. That is what
// makes it safe to call on every tick.
func updateAnalyticsOverview(app *App) {
	view := app.portfolioView.TabbedView.Analytics

	app.dataMutex.RLock()
	if app.selectedIdx < 0 || app.selectedIdx >= len(app.accounts) {
		app.dataMutex.RUnlock()
		view.Structure.SetText("[gray]Счёт не выбран[-]")
		view.Risk.SetText("")
		return
	}

	account := app.accounts[app.selectedIdx]
	positions := app.positions[account.ID]
	quotes := app.quotes[account.ID]
	sectors, sectorsKnown, sectorNote := app.sectorMapLocked()
	app.dataMutex.RUnlock()

	if account.LoadError != "" {
		message := "[red]" + brokerDataError() + "[-]"
		view.Structure.SetText(message)
		view.Risk.SetText(message)
		return
	}

	allocation := analytics.Structure(analytics.StructureInput{
		Positions: positions,
		Quotes:    quotes,
		Cash:      account.Cash,
		Types:     instrumentTypes(app, positions),
		Sectors:   sectors,
	})

	risk := analytics.RiskMetrics(analytics.RiskInput{
		Account:       account,
		GrossExposure: allocation.Base - allocation.Cash,
	})

	view.Structure.SetText(renderStructure(allocation, sectorsKnown, sectorNote))
	view.Risk.SetText(app.renderSinceOpenSummary(account) + renderRisk(account, risk, allocation))
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

// renderStructure draws the left column: the breakdown by instrument type, the
// cash line, sectors, and the counters for anything left out.
func renderStructure(a analytics.Allocation, sectorsKnown bool, sectorNote string) string {
	var b strings.Builder

	if !a.Valid {
		b.WriteString(notAvailable + " — в портфеле нет оценённых активов\n")
	} else {
		for _, g := range a.Groups {
			writeShareRow(&b, g.Name, g.Value, g.Share)
		}
		if a.Cash > 0 || len(a.Groups) == 0 {
			writeShareRow(&b, analytics.GroupCash, a.Cash, a.CashShare)
		}
		fmt.Fprintf(&b, "\n[white]Итого[-]  %s %s\n", formatNumber(a.Base, 2), a.BaseCurrency)
	}

	b.WriteString("\n[white]Секторы[-]\n")
	switch {
	case !sectorsKnown:
		fmt.Fprintf(&b, "[gray]%s[-]\n", sectorNote)
	case len(a.Sectors) == 0:
		b.WriteString("[gray]нет данных по секторам[-]\n")
	default:
		for _, s := range a.Sectors {
			writeShareRow(&b, s.Name, s.Value, s.Share)
		}
	}

	if a.Skipped > 0 || a.ForeignCount > 0 {
		b.WriteString("\n")
		if a.Skipped > 0 {
			fmt.Fprintf(&b, "[yellow]без цены: %d[-]\n", a.Skipped)
		}
		if a.ForeignCount > 0 {
			fmt.Fprintf(&b, "[yellow]в других валютах: %d[-]\n", a.ForeignCount)
		}
	}

	return b.String()
}

// writeShareRow draws one breakdown row: name, money, share and a bar.
func writeShareRow(b *strings.Builder, name string, value, share float64) {
	fmt.Fprintf(b, "%-14s %14s %6s %s\n",
		tview.Escape(truncate(name, 14)),
		formatNumber(value, 0),
		formatPercent(share),
		shareBar(share))
}

// renderRisk draws the right column: what the account reports, what can be
// derived from it, and the concentration block.
func renderRisk(account models.AccountInfo, r analytics.Risk, a analytics.Allocation) string {
	var b strings.Builder

	if r.EquityValid {
		fmt.Fprintf(&b, "Эквити            %14s\n", formatNumber(r.Equity, 2))
	} else {
		fmt.Fprintf(&b, "Эквити            %14s\n", notAvailable)
	}

	if len(a.Borrowed) > 0 {
		for _, c := range a.Borrowed {
			fmt.Fprintf(&b, "[red]заём %-11s %14s[-]\n", tview.Escape(c.Currency), formatNumber(c.Amount, 2))
		}
	}

	if r.HasMarginData {
		fmt.Fprintf(&b, "Доступный кэш     %14s\n", formatNumber(account.AvailableCash, 2))
		switch r.Kind {
		case "MC":
			fmt.Fprintf(&b, "Начальная маржа   %14s\n", formatNumber(account.InitialMargin, 2))
			fmt.Fprintf(&b, "Мин. маржа        %14s\n", formatNumber(account.MaintenanceMargin, 2))
		case "FORTS":
			fmt.Fprintf(&b, "Резерв ГО         %14s\n", formatNumber(account.MoneyReserved, 2))
		}
	}

	b.WriteString("\n")
	writeMetric(&b, "Использование маржи", r.Utilization, analytics.UtilizationLevel)
	writeMetric(&b, "Запас до маржин-колла", r.Cushion, analytics.CushionLevel)
	writeLeverage(&b, r.Leverage)

	b.WriteString("\n[white]Концентрация[-]\n")
	if len(a.Top) == 0 {
		b.WriteString("[gray]нет позиций[-]\n")
	} else {
		for _, h := range a.Top {
			fmt.Fprintf(&b, "%-10s %14s %6s\n",
				tview.Escape(truncate(h.Ticker, 10)),
				formatNumber(h.Value, 0),
				formatPercent(h.Share))
		}
	}
	fmt.Fprintf(&b, "Позиций: %d\n", a.PositionCount)

	return b.String()
}

// writeMetric draws one coloured percentage, or Н/Д when the account does not
// report what it is computed from.
func writeMetric(b *strings.Builder, label string, m analytics.Metric, level func(float64) analytics.Level) {
	if !m.Valid {
		fmt.Fprintf(b, "%-22s %14s\n", label, notAvailable)
		return
	}
	fmt.Fprintf(b, "%-22s [%s]%13s[-]\n", label, levelColor[level(m.Value)], formatPercent(m.Value))
}

// writeLeverage draws the leverage multiple, which carries no colour: unlike
// margin use, there is no threshold that is right for every strategy.
func writeLeverage(b *strings.Builder, m analytics.Metric) {
	if !m.Valid {
		fmt.Fprintf(b, "%-22s %14s\n", "Плечо", notAvailable)
		return
	}
	fmt.Fprintf(b, "%-22s %13sx\n", "Плечо", fmt.Sprintf("%.2f", m.Value))
}

// formatPercent renders a 0..1 share as a percentage.
func formatPercent(share float64) string {
	return fmt.Sprintf("%.1f%%", share*100)
}

// shareBar draws a proportional bar. A share above 1 (which cannot happen for
// an allocation but can for a stray metric) is clamped rather than overflowing
// the row.
func shareBar(share float64) string {
	filled := int(share*barWidth + 0.5)
	if filled < 0 {
		filled = 0
	}
	if filled > barWidth {
		filled = barWidth
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
}
