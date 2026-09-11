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
}

// Panel height caps. A panel is drawn as tall as the text it holds, and no
// taller — but a run of sectors or a long list of borrowed currencies must not
// push the panel below it off the screen, so each one has a ceiling.
const (
	maxStructureLines     = 12
	maxCurrencyLines      = 10
	maxSectorLines        = 12
	maxSinceOpenLines     = 12
	maxValuationLines     = 6
	maxRiskLines          = 14
	maxConcentrationLines = 6
	maxTradeStatsLines    = 12
	maxPayoutTotalsLines  = 4
	maxMoneyPanelLines    = 24
)

const (
	// minPanelHeight is a border, one row of content, and a border. A panel
	// that cannot have that much is given nothing: half a frame says less than
	// no frame and looks like a fault.
	minPanelHeight = 3

	// minTableHeight is what a stack keeps back for the table under its panel,
	// so a tall headline block cannot squeeze the table out of existence.
	minTableHeight = 3
)

// AnalyticsView is the Analytics tab: a framed area holding a sub-screen tab
// strip over a Pages stack.
//
// Each sub-screen carries its own status line, so a failed history load cannot
// print an error across the overview, and vice versa.
type AnalyticsView struct {
	*tview.Flex
	ActiveScreen AnalyticsScreen

	// Period is the window the Trades and Money screens are looked at
	// through. It lives here rather than per screen because it is one choice
	// about the account, not about a screen.
	Period analytics.Preset

	// Header is the sub-screen tab strip. It is width-aware for the same
	// reason the panels are: the period is anchored to the right edge.
	Header *analyticsPanel
	Pages  *tview.Pages

	// Overview: two columns of stacked panels. Left is what the portfolio is
	// made of, right is what it is worth, what it has made and what it risks.
	Structure      *analyticsPanel
	Currencies     *analyticsPanel
	Sectors        *analyticsPanel
	SinceOpen      *analyticsPanel
	Valuation      *analyticsPanel
	Risk           *analyticsPanel
	Concentration  *analyticsPanel
	OverviewStatus *tview.TextView
	overview       *tview.Flex

	// Trades: headline figures over a per-instrument table.
	TradeStats  *analyticsPanel
	TradesTable *tview.Table
	TradeStatus *tview.TextView
	trades      *analyticsStack

	// Money: the chosen period on the left, the whole life of the account on
	// the right.
	MoneyPeriod    *analyticsPanel
	MoneySinceOpen *analyticsPanel
	MoneyStatus    *tview.TextView
	money          *tview.Flex

	// Payouts: a compact summary strip over a table by date.
	PayoutTotals *analyticsPanel
	PayoutTable  *tview.Table
	PayoutStatus *tview.TextView
	payouts      *analyticsStack

	// LoadBar stands in for the whole Trades or Money screen while the active
	// account's history loads. historyLoading is whether it should: the
	// screens decide it from the account on screen at every redraw.
	LoadBar        *historyLoadBar
	historyLoading bool
}

// loadingPage is the Pages entry holding the history progress bar.
const loadingPage = "history_loading"

// NewAnalyticsView builds the tab.
func NewAnalyticsView() *AnalyticsView {
	av := &AnalyticsView{
		Flex:           tview.NewFlex().SetDirection(tview.FlexRow),
		Period:         analytics.DefaultPreset,
		Header:         createAnalyticsBar(),
		Pages:          tview.NewPages(),
		Structure:      createAnalyticsPanel(" Структура портфеля "),
		Currencies:     createAnalyticsPanel(" Валюты "),
		Sectors:        createAnalyticsPanel(" Секторы "),
		SinceOpen:      createAnalyticsPanel(" Итог с открытия "),
		Valuation:      createAnalyticsPanel(" Оценка "),
		Risk:           createAnalyticsPanel(" Маржа и риск "),
		Concentration:  createAnalyticsPanel(" Концентрация "),
		OverviewStatus: createAnalyticsStatus(),
		TradeStats:     createAnalyticsPanel(" Сделки за период "),
		TradesTable:    createAnalyticsTable(" По инструментам "),
		TradeStatus:    createAnalyticsStatus(),
		MoneyPeriod:    createAnalyticsPanel(" За период "),
		MoneySinceOpen: createAnalyticsPanel(" С открытия счёта "),
		MoneyStatus:    createAnalyticsStatus(),
		PayoutTotals:   createAnalyticsPanel(" Ожидаемые выплаты "),
		PayoutTable:    createAnalyticsTable(" По датам "),
		PayoutStatus:   createAnalyticsStatus(),
		LoadBar:        newHistoryLoadBar(),
	}

	// The currency breakdown sits under the one by type: both cut the same
	// base, and the eye reads them as one account of what the portfolio is.
	// The since-open summary stays last, the first to give way when the column
	// runs short.
	overviewLeft := newAnalyticsStack().
		AddPanel(av.Structure, maxStructureLines).
		AddPanel(av.Currencies, maxCurrencyLines).
		AddPanel(av.Sectors, maxSectorLines).
		AddPanel(av.SinceOpen, maxSinceOpenLines).
		AddSpacer()

	// The valuation leads the column: it is the figure the broker's own
	// terminal opens its account summary with, and it is short.
	overviewRight := newAnalyticsStack().
		AddPanel(av.Valuation, maxValuationLines).
		AddPanel(av.Risk, maxRiskLines).
		AddPanel(av.Concentration, maxConcentrationLines).
		AddSpacer()

	av.overview = tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(av.OverviewStatus, 1, 0, false).
		AddItem(tview.NewFlex().
			AddItem(overviewLeft, 0, 1, false).
			AddItem(overviewRight, 0, 1, false), 0, 1, false)

	av.trades = newAnalyticsStack().
		AddFixed(av.TradeStatus, 1).
		AddPanel(av.TradeStats, maxTradeStatsLines).
		AddTable(av.TradesTable)

	moneyLeft := newAnalyticsStack().
		AddPanel(av.MoneyPeriod, maxMoneyPanelLines).
		AddSpacer()

	moneyRight := newAnalyticsStack().
		AddPanel(av.MoneySinceOpen, maxMoneyPanelLines).
		AddSpacer()

	av.money = tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(av.MoneyStatus, 1, 0, false).
		AddItem(tview.NewFlex().
			AddItem(moneyLeft, 0, 1, false).
			AddItem(moneyRight, 0, 1, false), 0, 1, false)

	av.payouts = newAnalyticsStack().
		AddFixed(av.PayoutStatus, 1).
		AddPanel(av.PayoutTotals, maxPayoutTotalsLines).
		AddTable(av.PayoutTable)

	av.Pages.AddPage("overview", av.overview, true, true)
	av.Pages.AddPage("trades", av.trades, true, false)
	av.Pages.AddPage("money", av.money, true, false)
	av.Pages.AddPage("payouts", av.payouts, true, false)
	av.Pages.AddPage(loadingPage, av.LoadBar, true, false)

	// The tab as a whole is framed like every other section of the terminal —
	// same border, same title, and the same double rule when it holds focus.
	// Without it the sub-screen bar floated at the top-left of the screen with
	// nothing to belong to, and the panels below started hard against the top
	// edge of the terminal.
	av.SetBorder(true)
	av.SetTitle(" Analytics ")
	// Centred, unlike the panels inside it: this title names the whole section
	// rather than labelling the block under it, and centring is what separates
	// the two roles at a glance.
	av.SetTitleAlign(tview.AlignCenter)
	av.SetTitleColor(analyticsTitleColour)
	av.SetBorderPadding(0, 0, 1, 1)
	av.SetBackgroundColor(tcell.ColorBlack)

	av.AddItem(av.Header, 1, 0, false)
	av.AddItem(analyticsRule(), 1, 0, false)
	av.AddItem(av.Pages, 0, 1, true)

	av.updateHeader()

	return av
}

// analyticsStack is a column that sizes its panels at draw time.
//
// The height has to be decided then and not before, because only the draw pass
// knows how much room the column actually got. Sizing panels ahead of time
// worked until the sum of their content exceeded the space: the last panel
// carried on drawing past the bottom of the column and painted over the frame
// around the tab.
type analyticsStack struct {
	*tview.Flex

	panels []stackPanel

	// lead is the height of the fixed rows above the panels — a status line.
	lead int

	// reserve is what is kept back for the flexible item below them, so a tall
	// headline block cannot squeeze a table out of existence. It is zero while
	// that item is collapsed.
	reserve int
}

// stackPanel is one panel in a stack and the ceiling on its height.
type stackPanel struct {
	panel *analyticsPanel
	max   int
}

func newAnalyticsStack() *analyticsStack {
	return &analyticsStack{Flex: tview.NewFlex().SetDirection(tview.FlexRow)}
}

// AddFixed adds a row of a known height above the panels.
func (s *analyticsStack) AddFixed(item tview.Primitive, height int) *analyticsStack {
	s.AddItem(item, height, 0, false)
	s.lead += height
	return s
}

// AddPanel adds a panel to be sized to its content, up to maxLines.
func (s *analyticsStack) AddPanel(panel *analyticsPanel, maxLines int) *analyticsStack {
	s.AddItem(panel, minPanelHeight, 0, false)
	s.panels = append(s.panels, stackPanel{panel: panel, max: maxLines})
	return s
}

// AddSpacer closes the stack with the unframed remainder. A border drawn around
// empty space reads as a broken panel; the same emptiness with no border round
// it reads as margin.
func (s *analyticsStack) AddSpacer() *analyticsStack {
	s.AddItem(analyticsSpacer(), 0, 1, false)
	return s
}

// AddTable closes the stack with a table that takes the rest of the column.
func (s *analyticsStack) AddTable(table *tview.Table) *analyticsStack {
	s.AddItem(table, 0, 1, true)
	s.reserve = minTableHeight
	return s
}

// showTable gives the stack's table the rest of the column, or removes it.
//
// A bordered table drawn full height with only its column titles in it is a
// frame around an absence, and it says the screen is broken rather than empty.
// The block above already carries the explanation, so the table simply goes —
// and the panels above get back the rows that were held for it.
func (s *analyticsStack) showTable(table *tview.Table, hasRows bool) {
	if hasRows {
		s.reserve = minTableHeight
		s.ResizeItem(table, 0, 1)
		return
	}
	s.reserve = 0
	s.ResizeItem(table, 0, 0)
}

// Draw hands each panel the height its content asks for, as far as the column
// can pay, and then draws.
func (s *analyticsStack) Draw(screen tcell.Screen) {
	_, _, _, height := s.GetInnerRect()

	desired := make([]int, len(s.panels))
	for i, sp := range s.panels {
		desired[i] = panelRows(sp.panel, sp.max)
	}

	for i, granted := range distributeHeights(desired, height-s.lead-s.reserve) {
		s.ResizeItem(s.panels[i].panel, granted, 0)
	}

	s.Flex.Draw(screen)
}

// panelRows is how tall a panel wants to be: its content plus the two border
// rows, capped.
func panelRows(panel *analyticsPanel, maxLines int) int {
	lines := countLines(panel.GetText(true))
	if lines > maxLines {
		lines = maxLines
	}
	if lines < 1 {
		lines = 1
	}
	return lines + 2
}

// distributeHeights hands out the column's rows in order, first come first
// served.
//
// The order is priority order — a portfolio's composition matters more than the
// summary under it — and a panel that cannot be given a frame, a row and a
// frame is given nothing at all rather than a stump.
func distributeHeights(desired []int, available int) []int {
	granted := make([]int, len(desired))

	left := available
	for i, want := range desired {
		if left < minPanelHeight {
			break
		}
		if want > left {
			want = left
		}
		granted[i] = want
		left -= want
	}
	return granted
}

// countLines is how many rows a block of text occupies. A trailing newline is
// not a row of its own.
func countLines(text string) int {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return 1
	}
	return strings.Count(text, "\n") + 1
}

// SetScreen switches the sub-screen and repaints the bar.
func (av *AnalyticsView) SetScreen(screen AnalyticsScreen) {
	for _, s := range analyticsScreens {
		if s.Screen == screen {
			av.ActiveScreen = screen
			av.showActivePage()
			av.updateHeader()
			return
		}
	}
}

// SetHistoryLoad puts the progress bar in place of the Trades and Money
// screens while the account on screen loads its history, or puts the screens
// back. The other two sub-screens are not the history's to cover.
func (av *AnalyticsView) SetHistoryLoad(loading bool, fraction float64) {
	av.historyLoading = loading
	av.LoadBar.SetFraction(fraction)
	av.showActivePage()
}

// ShowsLoadBar reports whether the progress bar is what the tab shows now.
func (av *AnalyticsView) ShowsLoadBar() bool {
	return av.historyLoading &&
		(av.ActiveScreen == AnalyticsTrades || av.ActiveScreen == AnalyticsMoney)
}

// showActivePage brings up the active sub-screen, or the bar standing in for
// it.
func (av *AnalyticsView) showActivePage() {
	if av.ShowsLoadBar() {
		av.Pages.SwitchToPage(loadingPage)
		return
	}
	for _, s := range analyticsScreens {
		if s.Screen == av.ActiveScreen {
			av.Pages.SwitchToPage(s.Page)
			return
		}
	}
}

// Focusable returns the primitive that should hold focus on the current
// sub-screen. A table sub-screen takes focus itself so ↑/↓ scroll it; the
// overview has nothing to scroll and keeps focus on its container.
//
// While the progress bar stands in for a screen, the bar takes focus: the
// table behind it is hidden but can still be full — during R it is — and
// Enter or A reaching it would act on a row nobody can see.
func (av *AnalyticsView) Focusable() tview.Primitive {
	if av.ShowsLoadBar() {
		return av.LoadBar
	}
	switch av.ActiveScreen {
	case AnalyticsTrades:
		return av.TradesTable
	case AnalyticsMoney:
		return av.money
	case AnalyticsPayouts:
		return av.PayoutTable
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

// fitTradesTable and fitPayoutTable collapse their table when it has nothing
// to show.
func (av *AnalyticsView) fitTradesTable(hasRows bool) {
	av.trades.showTable(av.TradesTable, hasRows)
}

func (av *AnalyticsView) fitPayoutTable(hasRows bool) {
	av.payouts.showTable(av.PayoutTable, hasRows)
}

// updateHeader draws the sub-screen tab strip.
func (av *AnalyticsView) updateHeader() {
	active, period := av.ActiveScreen, av.Period
	av.Header.SetRender(func(width int) string { return renderSubTabs(active, period, width) })
}

// renderSubTabs lays the tabs out along the strip with the period against the
// right edge.
//
// The labels are padded inside their highlight and separated by a dim rule:
// with the yellow background butted straight against the next label the blocks
// merged into one another. The period is anchored to the far edge rather than
// to a fixed column, so it neither drifts into the middle of a wide terminal
// nor collides with the tabs on a narrow one.
func renderSubTabs(active AnalyticsScreen, period analytics.Preset, width int) string {
	var b strings.Builder
	for i, s := range analyticsScreens {
		if i > 0 {
			fmt.Fprintf(&b, "[%s]│[-]", analyticsMutedTag)
		}
		label := fmt.Sprintf(" %d %s ", i+1, s.Label)
		if s.Screen == active {
			fmt.Fprintf(&b, "[black:yellow::b]%s[-:-:-]", label)
		} else {
			fmt.Fprintf(&b, "[white:black]%s[-:-]", label)
		}
	}

	tabs := b.String()
	label := fmt.Sprintf("[white]Период[-] [yellow::b]%s[-:-:-]", period.Label())

	gap := width - tview.TaggedStringWidth(tabs) - tview.TaggedStringWidth(label)
	if gap < 3 {
		gap = 3
	}
	return tabs + strings.Repeat(" ", gap) + label
}

// createAnalyticsBar builds the tab strip: a borderless, width-aware line.
func createAnalyticsBar() *analyticsPanel {
	view := tview.NewTextView()
	view.SetDynamicColors(true)
	view.SetWrap(false)
	view.SetBackgroundColor(tcell.ColorBlack)
	return &analyticsPanel{TextView: view}
}

// analyticsRule is the horizontal line under the tab strip, separating the
// tabs from the sub-screen they select. It is drawn rather than typed because
// only the draw pass knows how wide the frame is.
func analyticsRule() *tview.Box {
	box := tview.NewBox().SetBackgroundColor(tcell.ColorBlack)
	box.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		style := tcell.StyleDefault.
			Background(tcell.ColorBlack).
			Foreground(analyticsBorderColour)
		for i := range width {
			screen.SetContent(x+i, y, tview.Borders.Horizontal, nil, style)
		}
		return x, y, width, height
	})
	return box
}

// historyLoadBar is what the Trades and Money screens show while the account's
// history loads: one row as wide as its area, in the middle of it.
//
// It replaced a line of text over empty frames. The text had to be read, and
// the eye went to the frames instead, which looked like a broken screen; a bar
// is taken in at a glance and the emptiness around it reads as waiting.
type historyLoadBar struct {
	*tview.Box
	fraction float64
}

func newHistoryLoadBar() *historyLoadBar {
	return &historyLoadBar{Box: tview.NewBox().SetBackgroundColor(tcell.ColorBlack)}
}

// SetFraction sets how much of the pass is done, 0..1.
func (b *historyLoadBar) SetFraction(fraction float64) {
	b.fraction = fraction
}

// Draw paints the background, then the bar across the middle row. The width
// and the row are the draw pass's to decide: only it knows how much room the
// sub-screen got.
func (b *historyLoadBar) Draw(screen tcell.Screen) {
	b.DrawForSubclass(screen, b)

	x, y, width, height := b.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}
	row := y + (height-1)/2
	for i, c := range progressBarCells(b.fraction, width) {
		screen.SetContent(x+i, row, c.Char, nil, tcell.StyleDefault.Foreground(c.Fg).Background(c.Bg))
	}
}

// createAnalyticsPanel builds one bordered panel of a sub-screen.
//
// The border is dimmer than the numbers inside it and the title carries the
// accent colour, so the frame stays furniture and the content stays the point.
// The one column of padding on each side keeps text off the border.
func createAnalyticsPanel(title string) *analyticsPanel {
	view := tview.NewTextView()
	view.SetDynamicColors(true)
	// No wrapping. These panels hold rows of aligned figures, and a row too
	// wide for a narrow terminal reads far better cut off at the edge than
	// folded onto a second line, where it destroys the column it belongs to.
	// It also keeps panelRows honest: countLines counts logical lines, so a
	// wrapped row would have occupied two screen rows the height never
	// accounted for, and the bottom of the panel would be clipped.
	view.SetWrap(false)
	view.SetBorder(true)
	view.SetTitle(title)
	view.SetTitleAlign(tview.AlignLeft)
	view.SetTitleColor(analyticsTitleColour)
	view.SetBorderColor(analyticsBorderColour)
	view.SetBorderPadding(0, 0, 1, 1)
	view.SetBackgroundColor(tcell.ColorBlack)
	return &analyticsPanel{TextView: view}
}

// analyticsSpacer is the unframed remainder at the bottom of a column. It is
// what lets every panel above it be exactly as tall as its content.
func analyticsSpacer() *tview.Box {
	return tview.NewBox().SetBackgroundColor(tcell.ColorBlack)
}

// createAnalyticsStatus builds a one-line status strip: loading in yellow,
// errors in red, quiet otherwise.
func createAnalyticsStatus() *tview.TextView {
	view := tview.NewTextView()
	view.SetDynamicColors(true)
	view.SetBackgroundColor(tcell.ColorBlack)
	return view
}

// createAnalyticsTable builds a table sub-screen in the shape the Index tab
// settled on: a pinned header row, so the column titles survive scrolling.
func createAnalyticsTable(title string) *tview.Table {
	table := tview.NewTable()
	table.SetBorder(true)
	table.SetTitle(title)
	table.SetTitleAlign(tview.AlignLeft)
	table.SetTitleColor(analyticsTitleColour)
	table.SetBorderColor(analyticsBorderColour)
	table.SetBorderPadding(0, 0, 1, 1)
	table.SetBackgroundColor(tcell.ColorBlack)
	table.SetSelectable(true, false)
	table.SetSelectedStyle(tcell.StyleDefault.Background(tcell.ColorYellow).Foreground(tcell.ColorBlack))
	table.SetFixed(1, 0)
	return table
}
