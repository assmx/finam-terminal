package ui

import (
	"fmt"
	"strings"
	"time"

	"finam-terminal/analytics"
	"finam-terminal/models"
)

// Money screen geometry. Only the fixed columns are named here: the label and
// the amount. Everything left over goes to the bar, so the row reaches the edge
// of whatever panel it lands in.
const (
	moneyLabelWidth  = 22
	moneyAmountWidth = 14
	minFlowBar       = 4
)

// renderMoneyScreen installs the renderers for both columns of the Money
// sub-screen.
//
// Left: what moved during the chosen period. Right: the account's whole life,
// which is the only horizon on which a return can be computed honestly — the
// API carries no equity history, so a return over an arbitrary month would
// need an unrealised result at that month's start that nobody has.
func renderMoneyScreen(a *App, account models.AccountInfo, data analyticsAccountData,
	fifo analytics.FIFOResult, from, to time.Time, currency string) {

	view := a.analyticsView()

	if data.history == nil {
		view.MoneyPeriod.SetStatic(muted("История ещё не загружена"))
		view.MoneySinceOpen.SetStatic(muted("История ещё не загружена"))
		return
	}

	flow := analytics.Flows(data.history.Transactions, from, to, currency)
	stats := analytics.Stats(fifo.Closed, data.history.Trades, from, to, currency)
	since := a.sinceOpen(account, data)

	view.MoneyPeriod.SetRender(func(w int) string {
		return renderPeriodMoney(flow, stats[currency], currency, from, to, w)
	})
	view.MoneySinceOpen.SetRender(func(w int) string {
		return renderSinceOpen(since, data, w)
	})
}

// renderPeriodMoney writes the left column: the headline result, then where
// the money actually went.
//
// The groups are drawn as a bar chart against the largest of them. A column of
// signed numbers says which line is biggest only after the reader compares
// them one by one; a bar says it immediately.
func renderPeriodMoney(flow analytics.CashFlow, stats analytics.TradeStats, currency string, from, to time.Time, width int) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%s\n\n", muted(fmt.Sprintf("%s — %s",
		from.Local().Format("02.01.2006"), to.Local().Format("02.01.2006"))))

	c := flow.ByCurrency[currency]

	// The result over a period is the realised part only. Saying so beside the
	// number is the difference between a figure and a misleading one.
	result := stats.Total + c.Payouts + c.Costs
	b.WriteString(kpiRow(width, kpiTile{
		Caption: "Результат по деньгам",
		Value:   fmt.Sprintf("%s %s", colouredAmount(result), currency),
	}))
	fmt.Fprintf(&b, "%s\n\n", muted("без нереализованной части"))

	writeFlowGroups(&b, c, flow.TransferQty, width)

	fmt.Fprintf(&b, "%s\n", muted(strings.Repeat("─", width)))
	fmt.Fprintf(&b, "%s\n", leaderRow("Издержки", colouredAmount(c.Costs), width))
	fmt.Fprintf(&b, "%s\n", leaderRow("Выплаты получено", colouredAmount(c.Payouts), width))
	fmt.Fprintf(&b, "%s\n", leaderRow("Чистый ввод", colouredAmount(c.NetDeposit), width))

	// Other currencies never join the sums above; they get their own lines.
	for _, other := range sortedFlowCurrencies(flow, currency) {
		o := flow.ByCurrency[other]
		fmt.Fprintf(&b, "%s\n", muted(fmt.Sprintf("%s: ввод %s, издержки %s",
			other, formatAmount(o.NetDeposit), formatAmount(o.Costs))))
	}

	return b.String()
}

// writeFlowGroups draws one bar per non-empty group, all measured against the
// largest of them.
func writeFlowGroups(b *strings.Builder, c analytics.CurrencyFlow, transferQty float64, width int) {
	scale := largestFlow(c)
	if scale == 0 && transferQty == 0 {
		fmt.Fprintf(b, "%s\n\n", muted("За период движения денег не было"))
		return
	}

	labelWidth := moneyLabelWidth
	if max := width - moneyAmountWidth - minFlowBar - 1; labelWidth > max {
		labelWidth = max
	}
	if labelWidth < 8 {
		labelWidth = 8
	}

	fmt.Fprintf(b, "%s\n", sectionTitle("По группам"))
	for _, group := range analytics.FlowGroupOrder {
		amount, ok := c.Groups[group]
		if !ok || amount == 0 {
			continue
		}
		fmt.Fprintf(b, "%s\n", flowRow(group.Label(), colouredAmount(amount),
			signedBar(amount, scale, flowBarWidth(labelWidth, width)), labelWidth))
	}

	// Securities moving in or out carry a quantity, not money, so they never
	// get a bar measured against a rouble scale.
	if transferQty != 0 {
		fmt.Fprintf(b, "%s\n", flowRow(analytics.GroupTransfer.Label(),
			formatNumber(transferQty, 0)+" шт.", "", labelWidth))
	}
	b.WriteString("\n")
}

// flowRow is one group: name, amount, and the bar that reaches the panel edge.
func flowRow(label, amount, bar string, labelWidth int) string {
	row := padTaggedRight(label, labelWidth) + padTagged(amount, moneyAmountWidth)
	if bar == "" {
		return row
	}
	return row + " " + bar
}

// flowBarWidth is what the fixed columns leave for the bar.
func flowBarWidth(labelWidth, width int) int {
	bar := width - labelWidth - moneyAmountWidth - 1
	if bar < minFlowBar {
		return 0
	}
	return bar
}

// largestFlow is the biggest magnitude among the groups, which is the scale
// every bar in the block is drawn against.
func largestFlow(c analytics.CurrencyFlow) float64 {
	var max float64
	for _, amount := range c.Groups {
		if amount < 0 {
			amount = -amount
		}
		if amount > max {
			max = amount
		}
	}
	return max
}

// sinceOpen computes the whole-life block for an account.
func (a *App) sinceOpen(account models.AccountInfo, data analyticsAccountData) analytics.SinceOpen {
	if data.history == nil {
		return analytics.SinceOpen{}
	}

	equity, _ := analytics.ParseNumber(account.Equity)
	from := account.FirstTradeDate
	if from.IsZero() || (!account.FirstNonTradeDate.IsZero() && account.FirstNonTradeDate.Before(from)) {
		from = account.FirstNonTradeDate
	}
	// A truncated pass only knows the account from its boundary onwards, so
	// that is where the horizon starts. Measuring a partial history against the
	// full equity would credit the account with money it cannot account for.
	if !data.history.Boundary.IsZero() && data.history.Boundary.After(from) {
		from = data.history.Boundary
	}

	return analytics.SinceOpenResult(data.history.Transactions, equity,
		analyticsBaseCurrency(account), from, time.Now())
}

// renderSinceOpen writes the right column: the account's result over its whole
// life, and the index over the same horizon.
func renderSinceOpen(s analytics.SinceOpen, data analyticsAccountData, width int) string {
	var b strings.Builder

	writeSinceOpenBlock(&b, s, width)
	b.WriteString("\n")
	writeBenchmarkBlock(&b, s, data, width)

	return b.String()
}

// writeSinceOpenBlock is shared with the overview, so the two screens cannot
// disagree about the account's headline result.
func writeSinceOpenBlock(b *strings.Builder, s analytics.SinceOpen, width int) {
	// No heading: both panels that show this block already name it in their
	// border title, and repeating it inside cost a row to say nothing. The
	// horizon is the one thing the title cannot carry.
	if s.Days > 0 {
		fmt.Fprintf(b, "%s\n", muted(fmt.Sprintf("за %d дн.", s.Days)))
	}

	fmt.Fprintf(b, "%s\n", leaderRow("Чистый ввод",
		fmt.Sprintf("%s %s", formatAmount(s.NetDeposit), s.Currency), width))
	fmt.Fprintf(b, "%s\n", leaderRow("Результат",
		fmt.Sprintf("%s %s", colouredAmount(s.Result), s.Currency), width))
	fmt.Fprintf(b, "%s\n", leaderRow("Доходность",
		formatShareOrNA(s.SimpleReturn, s.SimpleValid), width))

	if s.XIRRStatus == analytics.XIRROK {
		fmt.Fprintf(b, "%s\n", leaderRow("XIRR (годовых)", colouredPercent(s.XIRR), width))
	} else {
		// The reason goes on its own line. Appended to the row it ran past the
		// panel and wrapped, turning one row into two ragged ones.
		fmt.Fprintf(b, "%s\n%s\n",
			leaderRow("XIRR (годовых)", notAvailable, width),
			muted("  "+s.XIRRStatus.Label()))
	}

	for _, currency := range sortedExcluded(s.Excluded) {
		fmt.Fprintf(b, "%s\n", muted(fmt.Sprintf("не учтено: %s %s",
			formatAmount(s.Excluded[currency]), currency)))
	}
}

// writeBenchmarkBlock compares the account against the index over the same
// horizon.
func writeBenchmarkBlock(b *strings.Builder, s analytics.SinceOpen, data analyticsAccountData, width int) {
	if !data.benchmarkOK {
		fmt.Fprintf(b, "%s\n", muted("IMOEX: нет данных"))
		return
	}

	bench := data.benchmark
	fmt.Fprintf(b, "%s\n", sectionTitle("IMOEX за тот же период"))
	fmt.Fprintf(b, "%s\n", leaderRow("Накопленное", colouredPercent(bench.Cumulative), width))

	if !bench.AnnualValid {
		fmt.Fprintf(b, "%s\n%s\n",
			leaderRow("Годовых", notAvailable, width),
			muted("  период короче 30 дней"))
	} else {
		fmt.Fprintf(b, "%s\n", leaderRow("Годовых", colouredPercent(bench.Annual), width))
		if s.XIRRStatus == analytics.XIRROK {
			diff := bench.DifferencePP(s.XIRR)
			fmt.Fprintf(b, "%s\n", leaderRow("Разница",
				fmt.Sprintf("[%s]%s п.п.[-]", amountTag(diff), formatNumber(diff, 1)), width))
		}
	}

	fmt.Fprintf(b, "%s\n", muted("индекс без дивидендов, результат счёта — с ними"))
}

// colouredPercent renders a rate in its own colour.
func colouredPercent(v float64) string {
	return fmt.Sprintf("[%s]%s[-]", amountTag(v), formatPercent(v))
}

// sortedFlowCurrencies lists the non-base currencies of a flow, in a stable
// order.
func sortedFlowCurrencies(flow analytics.CashFlow, base string) []string {
	out := make([]string, 0, len(flow.ByCurrency))
	for currency := range flow.ByCurrency {
		if currency != base {
			out = append(out, currency)
		}
	}
	sortStrings(out)
	return out
}

// sortedExcluded lists the currencies the since-open block had to leave out.
func sortedExcluded(excluded map[string]float64) []string {
	out := make([]string, 0, len(excluded))
	for currency, amount := range excluded {
		if amount != 0 {
			out = append(out, currency)
		}
	}
	sortStrings(out)
	return out
}

// sinceOpenSummaryRenderer is the overview's copy of the whole-life block.
//
// It reads the same cache the Money screen does and computes nothing of its
// own, so the two can never disagree. Before the history is loaded it says
// where to load it rather than showing an empty frame — the load is deliberate
// and expensive, and the overview must not start it behind the user's back.
func (a *App) sinceOpenSummaryRenderer(account models.AccountInfo) func(int) string {
	a.dataMutex.RLock()
	data := a.analytics.byAccount[account.ID]
	var snapshot analyticsAccountData
	if data != nil {
		snapshot = *data
	}
	a.dataMutex.RUnlock()

	if snapshot.history == nil {
		return func(int) string {
			return muted("Откройте «Сделки» или «Деньги»,\nчтобы загрузить историю счёта")
		}
	}

	since := a.sinceOpen(account, snapshot)
	return func(w int) string {
		var b strings.Builder
		writeSinceOpenBlock(&b, since, w)
		writeBenchmarkBlock(&b, since, snapshot, w)
		return strings.TrimRight(b.String(), "\n")
	}
}
