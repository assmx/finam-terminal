package ui

import (
	"fmt"
	"strings"

	"finam-terminal/analytics"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// AnalyticsScreen identifies a sub-screen of the Analytics tab.
type AnalyticsScreen int

const (
	AnalyticsOverview AnalyticsScreen = iota
	AnalyticsTrades
	AnalyticsMoney
	AnalyticsPayouts
	AnalyticsQuotas
)

// analyticsScreens is the single source of truth for the sub-screen bar: the
// header renders it and the digit keys index into it, so inserting a screen is
// one line here.
var analyticsScreens = []struct {
	Screen AnalyticsScreen
	Label  string
	Page   string
}{
	{AnalyticsOverview, "Обзор", "overview"},
	{AnalyticsTrades, "Сделки", "trades"},
	{AnalyticsMoney, "Деньги", "money"},
	{AnalyticsPayouts, "Выплаты", "payouts"},
	{AnalyticsQuotas, "API", "quotas"},
}

// AnalyticsView is the Analytics tab: a sub-screen bar over a Pages stack.
//
// Each sub-screen carries its own status line, so a failed quota load cannot
// print an error across the overview, and vice versa.
type AnalyticsView struct {
	*tview.Flex
	ActiveScreen AnalyticsScreen

	// Period is the window the Trades and Money screens are looked at
	// through. It lives here rather than per screen because it is one choice
	// about the account, not about a screen.
	Period analytics.Preset

	Header *tview.TextView
	Pages  *tview.Pages

	// Overview: two columns of text, structure on the left, margin and risk on
	// the right.
	Structure      *tview.TextView
	Risk           *tview.TextView
	OverviewStatus *tview.TextView
	overview       *tview.Flex

	// Trades: a block of headline figures over a per-instrument table.
	TradeStats  *tview.TextView
	TradesTable *tview.Table
	TradeStatus *tview.TextView
	trades      *tview.Flex

	// Money: two columns of text, the period on the left and the whole life of
	// the account on the right.
	MoneyPeriod    *tview.TextView
	MoneySinceOpen *tview.TextView
	MoneyStatus    *tview.TextView
	money          *tview.Flex

	// Payouts: two summary lines over a table by date.
	PayoutTotals *tview.TextView
	PayoutTable  *tview.Table
	PayoutStatus *tview.TextView
	payouts      *tview.Flex

	// API quotas.
	QuotaTable  *tview.Table
	QuotaStatus *tview.TextView
	quotas      *tview.Flex
}

// NewAnalyticsView builds the tab.
func NewAnalyticsView() *AnalyticsView {
	av := &AnalyticsView{
		Flex:           tview.NewFlex().SetDirection(tview.FlexRow),
		Period:         analytics.DefaultPreset,
		Header:         tview.NewTextView().SetDynamicColors(true),
		Pages:          tview.NewPages(),
		Structure:      createAnalyticsColumn(" Структура портфеля "),
		Risk:           createAnalyticsColumn(" Маржа и риск "),
		OverviewStatus: createAnalyticsStatus(),
		TradeStats:     createAnalyticsColumn(" Сделки за период "),
		TradesTable:    createAnalyticsTable(" По инструментам "),
		TradeStatus:    createAnalyticsStatus(),
		MoneyPeriod:    createAnalyticsColumn(" За период "),
		MoneySinceOpen: createAnalyticsColumn(" С открытия счёта "),
		MoneyStatus:    createAnalyticsStatus(),
		PayoutTotals:   createAnalyticsColumn(" Ожидаемые выплаты "),
		PayoutTable:    createAnalyticsTable(" По датам "),
		PayoutStatus:   createAnalyticsStatus(),
		QuotaTable:     createQuotaTable(),
		QuotaStatus:    createAnalyticsStatus(),
	}

	av.overview = tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(av.OverviewStatus, 1, 0, false).
		AddItem(tview.NewFlex().
			AddItem(av.Structure, 0, 1, false).
			AddItem(av.Risk, 0, 1, false), 0, 1, false)

	// The statistics block is a fixed height: it always holds the same rows,
	// and letting it grow would squeeze the table it sits above.
	av.trades = tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(av.TradeStatus, 1, 0, false).
		AddItem(av.TradeStats, tradeStatsHeight, 0, false).
		AddItem(av.TradesTable, 0, 1, true)

	av.money = tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(av.MoneyStatus, 1, 0, false).
		AddItem(tview.NewFlex().
			AddItem(av.MoneyPeriod, 0, 1, false).
			AddItem(av.MoneySinceOpen, 0, 1, false), 0, 1, false)

	av.payouts = tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(av.PayoutStatus, 1, 0, false).
		AddItem(av.PayoutTotals, payoutTotalsHeight, 0, false).
		AddItem(av.PayoutTable, 0, 1, true)

	av.quotas = tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(av.QuotaStatus, 1, 0, false).
		AddItem(av.QuotaTable, 0, 1, true)

	av.Pages.AddPage("overview", av.overview, true, true)
	av.Pages.AddPage("trades", av.trades, true, false)
	av.Pages.AddPage("money", av.money, true, false)
	av.Pages.AddPage("payouts", av.payouts, true, false)
	av.Pages.AddPage("quotas", av.quotas, true, false)

	av.AddItem(av.Header, 1, 0, false)
	av.AddItem(av.Pages, 0, 1, true)

	av.Header.SetBackgroundColor(tcell.ColorBlack)
	av.updateHeader()

	return av
}

// SetScreen switches the sub-screen and repaints the bar.
func (av *AnalyticsView) SetScreen(screen AnalyticsScreen) {
	for _, s := range analyticsScreens {
		if s.Screen == screen {
			av.ActiveScreen = screen
			av.Pages.SwitchToPage(s.Page)
			av.updateHeader()
			return
		}
	}
}

// Focusable returns the primitive that should hold focus on the current
// sub-screen. A table sub-screen takes focus itself so ↑/↓ scroll it; the
// overview has nothing to scroll and keeps focus on its container.
func (av *AnalyticsView) Focusable() tview.Primitive {
	switch av.ActiveScreen {
	case AnalyticsTrades:
		return av.TradesTable
	case AnalyticsMoney:
		return av.money
	case AnalyticsPayouts:
		return av.PayoutTable
	case AnalyticsQuotas:
		return av.QuotaTable
	default:
		return av.overview
	}
}

// SetPeriod changes the window the Trades and Money screens use and repaints
// the header. It touches no data: every preset is a filter over history the
// terminal already holds.
func (av *AnalyticsView) SetPeriod(period analytics.Preset) {
	av.Period = period
	av.updateHeader()
}

// updateHeader draws the sub-screen bar, highlighting the active screen the
// same way the tab bar above it does.
func (av *AnalyticsView) updateHeader() {
	var b strings.Builder
	for i, s := range analyticsScreens {
		if i > 0 {
			b.WriteString("  ")
		}
		label := fmt.Sprintf("[%d] %s", i+1, s.Label)
		if s.Screen == av.ActiveScreen {
			fmt.Fprintf(&b, "[black:yellow]%s[-]", label)
		} else {
			fmt.Fprintf(&b, "[white:black]%s[-]", label)
		}
	}
	fmt.Fprintf(&b, "        [white:black]Период:[-] [yellow]%s[-]", av.Period.Label())
	av.Header.SetText(b.String())
}

// createAnalyticsColumn builds one column of the overview.
func createAnalyticsColumn(title string) *tview.TextView {
	view := tview.NewTextView()
	view.SetDynamicColors(true)
	view.SetBorder(true)
	view.SetTitle(title)
	view.SetBackgroundColor(tcell.ColorBlack)
	return view
}

// createAnalyticsStatus builds a one-line status strip: loading in yellow,
// errors in red, quiet otherwise.
func createAnalyticsStatus() *tview.TextView {
	view := tview.NewTextView()
	view.SetDynamicColors(true)
	view.SetBackgroundColor(tcell.ColorBlack)
	return view
}

// createQuotaTable builds the API quota table.
func createQuotaTable() *tview.Table {
	table := tview.NewTable()
	table.SetBorder(true)
	table.SetTitle(" Квоты Trade API ")
	table.SetBackgroundColor(tcell.ColorBlack)
	table.SetSelectable(true, false)
	table.SetSelectedStyle(tcell.StyleDefault.Background(tcell.ColorYellow).Foreground(tcell.ColorBlack))
	// The real answer carries 39 rows, which scrolls on any normal terminal;
	// without this the column titles vanish with the first scroll.
	table.SetFixed(1, 0)
	return table
}

// tradeStatsHeight and payoutTotalsHeight are the fixed heights of the text
// blocks above their tables. Fixed rather than proportional because the content
// is a known number of lines, and a proportional block would steal room from
// the table on a short terminal.
const (
	tradeStatsHeight   = 11
	payoutTotalsHeight = 4
)

// createAnalyticsTable builds a table sub-screen in the shape the Index tab
// settled on: a pinned header row, so the column titles survive scrolling.
func createAnalyticsTable(title string) *tview.Table {
	table := tview.NewTable()
	table.SetBorder(true)
	table.SetTitle(title)
	table.SetBackgroundColor(tcell.ColorBlack)
	table.SetSelectable(true, false)
	table.SetSelectedStyle(tcell.StyleDefault.Background(tcell.ColorYellow).Foreground(tcell.ColorBlack))
	table.SetFixed(1, 0)
	return table
}
