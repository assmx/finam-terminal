package ui

import (
	"fmt"
	"strings"
	"time"

	"finam-terminal/analytics"
	"finam-terminal/models"
)

// renderMoneyScreen writes both columns of the Money sub-screen.
//
// Left: what moved during the chosen period. Right: the account's whole life,
// which is the only horizon on which a return can be computed honestly — the
// API carries no equity history, so a return over an arbitrary month would
// need an unrealised result at that month's start that nobody has.
func renderMoneyScreen(a *App, account models.AccountInfo, data analyticsAccountData,
	fifo analytics.FIFOResult, from, to time.Time, currency string) {

	view := a.analyticsView()

	if data.history == nil {
		view.MoneyPeriod.SetText("[gray]История ещё не загружена[-]")
		view.MoneySinceOpen.SetText("[gray]История ещё не загружена[-]")
		return
	}

	flow := analytics.Flows(data.history.Transactions, from, to, currency)
	stats := analytics.Stats(fifo.Closed, data.history.Trades, from, to, currency)

	view.MoneyPeriod.SetText(renderPeriodMoney(flow, stats[currency], currency, from, to))
	view.MoneySinceOpen.SetText(renderSinceOpen(a.sinceOpen(account, data), data))
}

// renderPeriodMoney writes the left column.
func renderPeriodMoney(flow analytics.CashFlow, stats analytics.TradeStats, currency string, from, to time.Time) string {
	var b strings.Builder

	fmt.Fprintf(&b, "[white]%s — %s[-]\n\n",
		from.Local().Format("02.01.2006"), to.Local().Format("02.01.2006"))

	c := flow.ByCurrency[currency]

	// The result over a period is the realised part only. Saying so beside the
	// number is the difference between a figure and a misleading one.
	result := stats.Total + c.Payouts + c.Costs
	fmt.Fprintf(&b, "[white]Результат по деньгам:[-] %s %s\n", colouredAmount(result), currency)
	fmt.Fprintf(&b, "[gray]без нереализованной части[-]\n\n")

	fmt.Fprintf(&b, "[white]Издержки:[-] %s\n", colouredAmount(c.Costs))
	fmt.Fprintf(&b, "[white]Выплаты получено:[-] %s\n", colouredAmount(c.Payouts))
	fmt.Fprintf(&b, "[white]Чистый ввод:[-] %s\n\n", colouredAmount(c.NetDeposit))

	b.WriteString("[white]По группам[-]\n")
	for _, group := range analytics.FlowGroupOrder {
		amount, ok := c.Groups[group]
		if !ok || amount == 0 {
			continue
		}
		fmt.Fprintf(&b, "  %-22s %s\n", group.Label(), colouredAmount(amount))
	}
	if flow.TransferQty != 0 {
		fmt.Fprintf(&b, "  %-22s %s шт.\n",
			analytics.GroupTransfer.Label(), formatNumber(flow.TransferQty, 0))
	}

	// Other currencies never join the sums above; they get their own lines.
	for _, other := range sortedFlowCurrencies(flow, currency) {
		o := flow.ByCurrency[other]
		fmt.Fprintf(&b, "\n[gray]%s: ввод %s, издержки %s[-]",
			other, formatAmount(o.NetDeposit), formatAmount(o.Costs))
	}

	return b.String()
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
func renderSinceOpen(s analytics.SinceOpen, data analyticsAccountData) string {
	var b strings.Builder

	writeSinceOpenBlock(&b, s)
	b.WriteString("\n")
	writeBenchmarkBlock(&b, s, data)

	return b.String()
}

// writeSinceOpenBlock is shared with the overview, so the two screens cannot
// disagree about the account's headline result.
func writeSinceOpenBlock(b *strings.Builder, s analytics.SinceOpen) {
	fmt.Fprintf(b, "[white]С открытия счёта[-]")
	if s.Days > 0 {
		fmt.Fprintf(b, " [gray](%d дн.)[-]", s.Days)
	}
	b.WriteString("\n")

	fmt.Fprintf(b, "[white]Чистый ввод:[-] %s %s\n", formatAmount(s.NetDeposit), s.Currency)
	fmt.Fprintf(b, "[white]Результат:[-] %s %s\n", colouredAmount(s.Result), s.Currency)
	fmt.Fprintf(b, "[white]Доходность:[-] %s\n", formatShareOrNA(s.SimpleReturn, s.SimpleValid))

	if s.XIRRStatus == analytics.XIRROK {
		fmt.Fprintf(b, "[white]XIRR (годовых):[-] %s\n", colouredPercent(s.XIRR))
	} else {
		fmt.Fprintf(b, "[white]XIRR (годовых):[-] Н/Д [gray](%s)[-]\n", s.XIRRStatus.Label())
	}

	for _, currency := range sortedExcluded(s.Excluded) {
		fmt.Fprintf(b, "[gray]не учтено: %s %s[-]\n", formatAmount(s.Excluded[currency]), currency)
	}
}

// writeBenchmarkBlock compares the account against the index over the same
// horizon.
func writeBenchmarkBlock(b *strings.Builder, s analytics.SinceOpen, data analyticsAccountData) {
	if !data.benchmarkOK {
		b.WriteString("[gray]IMOEX: нет данных[-]\n")
		return
	}

	bench := data.benchmark
	fmt.Fprintf(b, "[white]IMOEX за тот же период[-]\n")
	fmt.Fprintf(b, "[white]Накопленное:[-] %s\n", colouredPercent(bench.Cumulative))

	if !bench.AnnualValid {
		b.WriteString("[white]Годовых:[-] Н/Д [gray](период короче 30 дней)[-]\n")
	} else {
		fmt.Fprintf(b, "[white]Годовых:[-] %s\n", colouredPercent(bench.Annual))
		if s.XIRRStatus == analytics.XIRROK {
			diff := bench.DifferencePP(s.XIRR)
			fmt.Fprintf(b, "[white]Разница:[-] [%s]%s п.п.[-]\n", amountTag(diff), formatNumber(diff, 1))
		}
	}

	b.WriteString("[gray]индекс без дивидендов, результат счёта — с ними[-]\n")
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

// renderSinceOpenSummary is the overview's copy of the whole-life block.
//
// It reads the same cache the Money screen does and computes nothing of its
// own, so the two can never disagree. Before the history is loaded it says
// where to load it rather than showing an empty frame — the load is deliberate
// and expensive, and the overview must not start it behind the user's back.
func (a *App) renderSinceOpenSummary(account models.AccountInfo) string {
	a.dataMutex.RLock()
	data := a.analytics.byAccount[account.ID]
	var snapshot analyticsAccountData
	if data != nil {
		snapshot = *data
	}
	a.dataMutex.RUnlock()

	if snapshot.history == nil {
		return "[gray]Итог с открытия: откройте «Сделки» или «Деньги», чтобы загрузить историю[-]\n\n"
	}

	var b strings.Builder
	writeSinceOpenBlock(&b, a.sinceOpen(account, snapshot))
	writeBenchmarkBlock(&b, a.sinceOpen(account, snapshot), snapshot)
	b.WriteString("\n")
	return b.String()
}
