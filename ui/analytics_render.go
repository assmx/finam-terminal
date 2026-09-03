package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// AnalyticsScreen identifies a sub-screen of the Analytics tab.
//
// The set is deliberately larger than this track ships: the second track adds
// Сделки, Деньги and Выплаты between the overview and the quota table, and
// numbering them from the start means that change moves labels rather than
// rewriting the navigation.
type AnalyticsScreen int

const (
	AnalyticsOverview AnalyticsScreen = iota
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
	{AnalyticsQuotas, "API", "quotas"},
}

// AnalyticsScreenCount is how many sub-screens the digit keys can reach.
func AnalyticsScreenCount() int { return len(analyticsScreens) }

// AnalyticsView is the Analytics tab: a sub-screen bar over a Pages stack.
//
// Each sub-screen carries its own status line, so a failed quota load cannot
// print an error across the overview, and vice versa.
type AnalyticsView struct {
	*tview.Flex
	ActiveScreen AnalyticsScreen

	Header *tview.TextView
	Pages  *tview.Pages

	// Overview: two columns of text, structure on the left, margin and risk on
	// the right.
	Structure      *tview.TextView
	Risk           *tview.TextView
	OverviewStatus *tview.TextView
	overview       *tview.Flex

	// API quotas.
	QuotaTable  *tview.Table
	QuotaStatus *tview.TextView
	quotas      *tview.Flex
}

// NewAnalyticsView builds the tab.
func NewAnalyticsView() *AnalyticsView {
	av := &AnalyticsView{
		Flex:           tview.NewFlex().SetDirection(tview.FlexRow),
		Header:         tview.NewTextView().SetDynamicColors(true),
		Pages:          tview.NewPages(),
		Structure:      createAnalyticsColumn(" Структура портфеля "),
		Risk:           createAnalyticsColumn(" Маржа и риск "),
		OverviewStatus: createAnalyticsStatus(),
		QuotaTable:     createQuotaTable(),
		QuotaStatus:    createAnalyticsStatus(),
	}

	av.overview = tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(av.OverviewStatus, 1, 0, false).
		AddItem(tview.NewFlex().
			AddItem(av.Structure, 0, 1, false).
			AddItem(av.Risk, 0, 1, false), 0, 1, false)

	av.quotas = tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(av.QuotaStatus, 1, 0, false).
		AddItem(av.QuotaTable, 0, 1, true)

	av.Pages.AddPage("overview", av.overview, true, true)
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
	if av.ActiveScreen == AnalyticsQuotas {
		return av.QuotaTable
	}
	return av.overview
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

// updateAnalyticsOverview redraws the overview from state already in memory.
// Filled in by the next task; the tab's navigation is wired against it now.
func updateAnalyticsOverview(_ *App) {}

// updateQuotaTable redraws the API quota table from the cached answer.
func updateQuotaTable(_ *App) {}

// updateQuotaStatus redraws the API sub-screen's status line.
func updateQuotaStatus(_ *App) {}
