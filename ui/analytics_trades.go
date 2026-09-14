package ui

import (
	"fmt"
	"strings"
	"time"

	"finam-terminal/analytics"
	"finam-terminal/api"
	"finam-terminal/models"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// tradeColumns are the per-instrument table's headings.
var tradeColumns = []string{"Тикер", "Название", "Сделок", "Результат", "Прибыльных", "Позиция"}

// tradeColumnExpansion distributes the width left over after the content.
//
// The name absorbs the slack because company names are the only genuinely
// variable-length field, but not all of it: at an expansion of 6 it swallowed
// half a wide terminal and pushed every number to the far right, which is the
// opposite of what a table of figures is for.
//
// The expansion is applied to every cell, header and data alike. tview derives
// a column's width from the rows currently on screen, so expansion living only
// on the header collapses the table the moment the header scrolls away — the
// lesson the Index tab paid for.
var tradeColumnExpansion = []int{1, 3, 1, 2, 1, 1}

// tradeColumnAlign matches each heading to its data. A right-aligned column of
// numbers under a left-aligned heading leaves the title stranded at the far
// side of the column, which is worse than not aligning the numbers at all.
var tradeColumnAlign = []int{
	tview.AlignLeft, tview.AlignLeft,
	tview.AlignRight, tview.AlignRight, tview.AlignRight, tview.AlignRight,
}

// analyticsWindow is the period the Trades and Money screens are looked at
// through, resolved against the account's own start.
func (a *App) analyticsWindow(account models.AccountInfo) (time.Time, time.Time) {
	now := time.Now()

	since := account.FirstTradeDate
	if since.IsZero() || (!account.FirstNonTradeDate.IsZero() && account.FirstNonTradeDate.Before(since)) {
		since = account.FirstNonTradeDate
	}

	return a.AnalyticsPeriod().Range(now, since)
}

// updateAnalyticsHistoryScreens redraws everything that reads the history
// cache, from one snapshot of the account on screen: the progress bar while
// that account's pass runs, the status line and both screens otherwise.
//
// Every redraw goes through here — entering a screen, a progress report, the
// end of a pass, a period change — so what the screens show is always decided
// by the account they are showing: a report from another account's pass cannot
// put its bar over this one. It issues no request: the whole point of loading a
// full pass once is that every period afterwards is a filter over memory.
func updateAnalyticsHistoryScreens(a *App) {
	account, data, ok := a.analyticsAccountSnapshot()
	if !ok {
		a.setHistoryStatus("")
		a.showHistoryLoad(false, 0)
		return
	}
	if data.loading {
		// The bar is the whole screen; what is behind it waits for the pass.
		a.showHistoryLoad(true, historyLoadFraction(data.load))
		return
	}

	a.setHistoryStatus(historyStatusLine(account, data))

	from, to := a.analyticsWindow(account)
	currency := analyticsBaseCurrency(account)

	var fifo analytics.FIFOResult
	if data.history != nil {
		fifo = analytics.MatchFIFO(data.history.Trades)
	}

	renderTradeStats(a.analyticsView(), data.history, fifo, from, to, currency)
	a.analyticsView().fitTradesTable(renderTradeTable(a.analyticsView().TradesTable, fifo, from, to))
	renderMoneyScreen(a, account, data, fifo, from, to, currency)

	a.showHistoryLoad(false, 0)
}

// Detail and tile geometry.
//
// Unlike the rows of a breakdown, a headline block does not want the whole
// width: three figures spread across 120 columns stop being a group and become
// three unrelated numbers. So these blocks fill the panel only up to a point.
const (
	minTradeDetailWidth = 24
	maxTradeDetailWidth = 44
	maxKPIColumn        = 30
)

// renderTradeStats writes the headline block: three figures the eye lands on
// first, and the detail underneath.
//
// It replaces a column of seven "Подпись: значение" pairs. The pairs carried
// the same information, but finding the result meant reading every label; a
// row of tiles is read at a glance.
func renderTradeStats(view *AnalyticsView, bundle *api.HistoryBundle, fifo analytics.FIFOResult, from, to time.Time, currency string) {
	if bundle == nil {
		view.TradeStats.SetStatic(muted("История ещё не загружена"))
		return
	}

	stats := analytics.Stats(fifo.Closed, bundle.Trades, from, to, currency)
	base, hasBase := stats[currency]
	if !hasBase && len(stats) == 0 {
		view.TradeStats.SetStatic(muted("За выбранный период сделок нет"))
		return
	}

	view.TradeStats.SetRender(func(width int) string {
		var b strings.Builder
		fmt.Fprintf(&b, "%s\n\n", muted(fmt.Sprintf("%s — %s   ·   всего сделок %d, закрытых %d",
			from.Local().Format("02.01.2006"), to.Local().Format("02.01.2006"),
			base.RawCount, base.ClosedCount)))

		writeTradeStatsBlock(&b, base, width)

		// Currencies other than the account's own get one summary line each:
		// the Trade API carries no exchange rates, so they can never be added
		// in.
		for _, other := range sortedCurrencies(stats, currency) {
			s := stats[other]
			fmt.Fprintf(&b, "%s\n", muted(fmt.Sprintf("%s: %d сделок, результат %s",
				other, s.ClosedCount, formatAmount(s.Total))))
		}

		if unmatched := totalUnmatched(fifo); unmatched > 0 {
			fmt.Fprintf(&b, "[yellow]%s шт. продано без цены входа — результат неполный[-]\n",
				formatNumber(unmatched, 0))
		}
		if fifo.Skipped > 0 {
			fmt.Fprintf(&b, "[yellow]%d записей не прочитано[-]\n", fifo.Skipped)
		}

		return b.String()
	})
}

// writeTradeStatsBlock writes the base-currency figures: the tile row, then
// the detail in two aligned columns.
func writeTradeStatsBlock(b *strings.Builder, s analytics.TradeStats, width int) {
	// Half the panel each, capped so the two halves stay a pair; and when half
	// a panel is too little to hold a figure at all, they stack rather than
	// collide.
	column := width / 2
	if column > maxTradeDetailWidth {
		column = maxTradeDetailWidth
	}
	if column < minTradeDetailWidth {
		column = width
	}

	tiles := []kpiTile{
		{
			Caption: "Результат",
			Value:   fmt.Sprintf("%s %s", colouredAmount(s.Total), s.Currency),
		},
		{
			Caption: "Прибыльных",
			Value: fmt.Sprintf("%s %s", formatShareOrNA(s.WinRate, s.WinRateValid),
				muted(fmt.Sprintf("(%d из %d)", s.Wins, s.ClosedCount))),
		},
		{
			Caption: "Профит-фактор",
			Value:   formatProfitFactor(s),
		},
	}

	tileRow := width
	if capped := len(tiles) * maxKPIColumn; tileRow > capped {
		tileRow = capped
	}

	b.WriteString(kpiRow(tileRow, tiles...))
	b.WriteString("\n")

	writeTradeDetail(b, column,
		fmt.Sprintf("[white]Валовая[-] %s / %s", formatAmount(s.GrossProfit), formatAmount(s.GrossLoss)),
		fmt.Sprintf("[white]Средняя[-] %s / %s", formatAmount(s.AverageWin), formatAmount(s.AverageLoss)))
	writeTradeDetail(b, column,
		fmt.Sprintf("[white]Матожидание[-] %s", formatAmount(s.Expectancy)),
		fmt.Sprintf("[white]Оборот[-] %s", formatAmount(s.Turnover)))
	writeTradeDetail(b, column,
		fmt.Sprintf("[white]Лучшая[-] %s", formatBestWorst(s.Best, s.BestValid)),
		fmt.Sprintf("[white]Худшая[-] %s", formatBestWorst(s.Worst, s.WorstValid)))
}

// writeTradeDetail writes one two-column detail row. A column as wide as the
// whole panel pushes the right half onto its own line, which is what a panel
// too narrow for two columns needs.
func writeTradeDetail(b *strings.Builder, column int, left, right string) {
	fmt.Fprintf(b, "%s%s\n", padTaggedRight(left, column), right)
}

// renderTradeTable fills the per-instrument table and reports whether it ended
// up with any instruments in it.
func renderTradeTable(table *tview.Table, fifo analytics.FIFOResult, from, to time.Time) bool {
	table.Clear()

	rows := analytics.PerInstrument(fifo, from, to)
	if len(rows) == 0 {
		// No header either: the caller collapses the table, and column titles
		// over nothing would be the only thing left on the screen.
		return false
	}

	for col, title := range tradeColumns {
		table.SetCell(0, col, headerCell(title, tradeColumnExpansion[col], tradeColumnAlign[col]))
	}

	for i, row := range rows {
		values := []string{
			tickerOf(row.Symbol),
			row.Name,
			fmt.Sprintf("%d", row.ClosedCount),
			formatAmount(row.PnL),
			formatShareOrNA(row.WinRate, row.WinRateValid),
			formatPositionQty(row.OpenQty, row.Unmatched),
		}
		colour := amountColour(row.PnL)
		for col, text := range values {
			cell := tview.NewTableCell(text).
				SetExpansion(tradeColumnExpansion[col]).
				SetTextColor(tcell.ColorWhite)
			switch col {
			case 0:
				// The ticker is the row's identity; the rest is its detail.
				cell.SetTextColor(tcell.ColorAqua)
			case 1:
				cell.SetTextColor(tcell.ColorSilver)
			case 3:
				cell.SetTextColor(colour)
			}
			// Numbers read as a column only when they end in the same place.
			cell.SetAlign(tradeColumnAlign[col])
			table.SetCell(i+1, col, cell)
		}
	}

	table.Select(1, 0)
	return true
}

// headerCell builds a non-selectable heading with the column's expansion and
// alignment, so every cell in the column agrees both about how the width is
// shared and about which edge the content sits against.
func headerCell(title string, expansion, align int) *tview.TableCell {
	return tview.NewTableCell(title).
		SetTextColor(tcell.ColorYellow).
		SetExpansion(expansion).
		SetAlign(align).
		SetSelectable(false)
}

// selectedTradeSymbol is the full symbol of the highlighted row, resolved
// through the rendered order so the instrument that opens is the one the user
// is looking at. The header row and a stale selection answer "".
func (a *App) selectedTradeSymbol() string {
	row, _ := a.analyticsView().TradesTable.GetSelection()
	if row <= 0 {
		return ""
	}

	account, data, ok := a.analyticsAccountSnapshot()
	if !ok || data.history == nil {
		return ""
	}

	from, to := a.analyticsWindow(account)
	rows := analytics.PerInstrument(analytics.MatchFIFO(data.history.Trades), from, to)

	idx := row - 1
	if idx >= len(rows) {
		return ""
	}
	return rows[idx].Symbol
}

// totalUnmatched is how much was sold across the account with no entry price
// behind it.
func totalUnmatched(fifo analytics.FIFOResult) float64 {
	var total float64
	for _, qty := range fifo.Unmatched {
		total += qty
	}
	return total
}

// sortedCurrencies lists every currency in the stats except the base one, in a
// stable order.
func sortedCurrencies(stats map[string]analytics.TradeStats, base string) []string {
	out := make([]string, 0, len(stats))
	for currency := range stats {
		if currency != base {
			out = append(out, currency)
		}
	}
	sortStrings(out)
	return out
}
