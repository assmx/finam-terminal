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
// The name absorbs nearly all of it because company names are the only
// genuinely variable-length field; the numeric columns are as wide as their
// content and no wider.
//
// The expansion is applied to every cell, header and data alike. tview derives
// a column's width from the rows currently on screen, so expansion living only
// on the header collapses the table the moment the header scrolls away — the
// lesson the Index tab paid for.
var tradeColumnExpansion = []int{1, 6, 1, 2, 1, 1}

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
// cache. It issues no request: the whole point of loading a full pass once is
// that every period afterwards is a filter over memory.
func updateAnalyticsHistoryScreens(a *App) {
	account, data, ok := a.analyticsAccountSnapshot()
	if !ok {
		return
	}

	from, to := a.analyticsWindow(account)
	currency := analyticsBaseCurrency(account)

	var fifo analytics.FIFOResult
	if data.history != nil {
		fifo = analytics.MatchFIFO(data.history.Trades)
	}

	renderTradeStats(a.analyticsView(), data.history, fifo, from, to, currency)
	renderTradeTable(a.analyticsView().TradesTable, fifo, from, to)
}

// renderTradeStats writes the headline block.
func renderTradeStats(view *AnalyticsView, bundle *api.HistoryBundle, fifo analytics.FIFOResult, from, to time.Time, currency string) {
	if bundle == nil {
		view.TradeStats.SetText("[gray]История ещё не загружена[-]")
		return
	}

	stats := analytics.Stats(fifo.Closed, bundle.Trades, from, to, currency)
	base, hasBase := stats[currency]
	if !hasBase && len(stats) == 0 {
		view.TradeStats.SetText("[gray]За выбранный период сделок нет[-]")
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[white]Период:[-] %s — %s\n\n",
		from.Local().Format("02.01.2006"), to.Local().Format("02.01.2006"))

	writeTradeStatsBlock(&b, base)

	// Currencies other than the account's own get one summary line each: the
	// Trade API carries no exchange rates, so they can never be added in.
	for _, other := range sortedCurrencies(stats, currency) {
		s := stats[other]
		fmt.Fprintf(&b, "\n[gray]%s: %d сделок, результат %s[-]",
			other, s.ClosedCount, colouredAmount(s.Total))
	}

	if unmatched := totalUnmatched(fifo); unmatched > 0 {
		fmt.Fprintf(&b, "\n[yellow]%s шт. продано без цены входа — результат неполный[-]",
			formatNumber(unmatched, 0))
	}
	if fifo.Skipped > 0 {
		fmt.Fprintf(&b, "\n[yellow]%d записей не прочитано[-]", fifo.Skipped)
	}

	view.TradeStats.SetText(b.String())
}

// writeTradeStatsBlock writes the base-currency figures.
func writeTradeStatsBlock(b *strings.Builder, s analytics.TradeStats) {
	fmt.Fprintf(b, "[white]Закрытых сделок:[-] %d   [white]всего сделок:[-] %d\n",
		s.ClosedCount, s.RawCount)
	fmt.Fprintf(b, "[white]Результат:[-] %s %s\n", colouredAmount(s.Total), s.Currency)
	fmt.Fprintf(b, "[white]Прибыльных:[-] %s   [white]Профит-фактор:[-] %s\n",
		formatShareOrNA(s.WinRate, s.WinRateValid), formatProfitFactor(s))
	fmt.Fprintf(b, "[white]Валовая прибыль:[-] %s   [white]убыток:[-] %s\n",
		formatAmount(s.GrossProfit), formatAmount(s.GrossLoss))
	fmt.Fprintf(b, "[white]Средняя прибыльная:[-] %s   [white]убыточная:[-] %s\n",
		formatAmount(s.AverageWin), formatAmount(s.AverageLoss))
	fmt.Fprintf(b, "[white]Матожидание:[-] %s   [white]Оборот:[-] %s\n",
		formatAmount(s.Expectancy), formatAmount(s.Turnover))
	fmt.Fprintf(b, "[white]Лучшая:[-] %s   [white]Худшая:[-] %s\n",
		formatBestWorst(s.Best, s.BestValid), formatBestWorst(s.Worst, s.WorstValid))
}

// renderTradeTable fills the per-instrument table.
func renderTradeTable(table *tview.Table, fifo analytics.FIFOResult, from, to time.Time) {
	table.Clear()

	for col, title := range tradeColumns {
		table.SetCell(0, col, headerCell(title, tradeColumnExpansion[col]))
	}

	rows := analytics.PerInstrument(fifo, from, to)
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
			if col == 3 {
				cell.SetTextColor(colour)
			}
			table.SetCell(i+1, col, cell)
		}
	}

	if len(rows) > 0 {
		table.Select(1, 0)
	}
}

// headerCell builds a non-selectable heading with the column's expansion, so
// every cell in the column agrees about how the width is shared.
func headerCell(title string, expansion int) *tview.TableCell {
	return tview.NewTableCell(title).
		SetTextColor(tcell.ColorYellow).
		SetExpansion(expansion).
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
