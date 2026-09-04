package ui

import (
	"fmt"
	"sort"
	"strings"

	"finam-terminal/analytics"
	"finam-terminal/models"

	"github.com/gdamore/tcell/v2"
)

// analyticsBaseCurrency is the currency the account's figures are reported in.
//
// It follows the rule the overview settled on: roubles when the account holds
// them, otherwise the first cash line, otherwise roubles. A base currency is
// needed even for an account with no cash at all, because trades and
// transactions arrive with the field sometimes blank and have to land
// somewhere.
func analyticsBaseCurrency(account models.AccountInfo) string {
	for _, cash := range account.Cash {
		if strings.EqualFold(cash.Currency, "RUB") {
			return "RUB"
		}
	}
	if len(account.Cash) > 0 && account.Cash[0].Currency != "" {
		return account.Cash[0].Currency
	}
	return "RUB"
}

// amountColour is green above zero, red below, plain at zero. A flat result is
// not good news and not bad news, and colouring it either way would say
// something the number does not.
func amountColour(v float64) tcell.Color {
	switch {
	case v > 0:
		return tcell.ColorGreen
	case v < 0:
		return tcell.ColorRed
	default:
		return tcell.ColorWhite
	}
}

// amountTag is amountColour as a tview colour tag.
func amountTag(v float64) string {
	switch {
	case v > 0:
		return "green"
	case v < 0:
		return "red"
	default:
		return "white"
	}
}

// colouredAmount renders a signed amount in its own colour.
func colouredAmount(v float64) string {
	return fmt.Sprintf("[%s]%s[-]", amountTag(v), formatAmount(v))
}

// formatAmount renders money with two decimals and thousands separators.
func formatAmount(v float64) string { return formatNumber(v, 2) }

// formatShareOrNA renders a 0..1 share as a percentage, or "Н/Д" when the
// share could not be computed. An uncomputable ratio must never reach the
// screen as 0%.
func formatShareOrNA(share float64, valid bool) string {
	if !valid {
		return "Н/Д"
	}
	return formatPercent(share)
}

// formatProfitFactor renders the ratio, "∞" when there were gains and no
// losses, and "Н/Д" when there is nothing on either side.
func formatProfitFactor(s analytics.TradeStats) string {
	switch {
	case !s.ProfitFactorValid:
		return "Н/Д"
	case s.ProfitFactorInfinite:
		return "∞"
	default:
		return formatNumber(s.ProfitFactor, 2)
	}
}

// formatBestWorst names a trade and its result, or reports there was none.
func formatBestWorst(t analytics.ClosedTrade, valid bool) string {
	if !valid {
		return "—"
	}
	return fmt.Sprintf("%s %s", tickerOf(t.Symbol), colouredAmount(t.PnL))
}

// formatPositionQty renders the open position, marking an instrument whose
// history is missing an entry price so the row's result is read as partial.
func formatPositionQty(qty, unmatched float64) string {
	text := formatNumber(qty, 0)
	if qty == 0 {
		text = "—"
	}
	if unmatched > 0 {
		text += " [yellow]*[-]"
	}
	return text
}

// tickerOf is the bare ticker of a full symbol.
func tickerOf(symbol string) string {
	if i := strings.Index(symbol, "@"); i > 0 {
		return symbol[:i]
	}
	return symbol
}

// sortStrings orders a slice in place, so a rendered list is stable between
// redraws.
func sortStrings(s []string) { sort.Strings(s) }
