package ui

import (
	"strings"
	"testing"
	"time"

	"finam-terminal/analytics"

	"github.com/gdamore/tcell/v2"
)

// drawAnalytics lays the tab out on a simulation screen. The panel heights are
// a layout decision tview only makes while drawing, so asking for them without
// a draw answers zeros.
func drawAnalytics(t *testing.T, view *AnalyticsView, width, height int) {
	t.Helper()

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("simulation screen init: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(width, height)

	view.SetRect(0, 0, width, height)
	view.Draw(screen)
}

func panelHeight(p *analyticsPanel) int {
	_, _, _, h := p.GetRect()
	return h
}

func TestCountLines(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{"empty is still a row", "", 1},
		{"one line", "один", 1},
		{"two lines", "один\nдва", 2},
		{"a trailing newline is not a row", "один\nдва\n", 2},
		{"a blank line in the middle counts", "один\n\nтри", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countLines(tt.text); got != tt.want {
				t.Errorf("countLines(%q) = %d, want %d", tt.text, got, tt.want)
			}
		})
	}
}

// A panel lays its rows out to its own width, which tview settles only while
// drawing. Before this, rows were built to a fixed 38 columns: on a wide
// terminal they stopped two thirds of the way across and left the panel looking
// half empty, and on a narrow one they ran off the edge.
func TestPanelRendersToItsOwnWidth(t *testing.T) {
	view := NewAnalyticsView()

	var widths []int
	view.setOverview(
		func(w int) string {
			widths = append(widths, w)
			return strings.Repeat("─", w)
		},
		func(int) string { return "валюты" },
		func(int) string { return "сектор" },
		func(int) string { return "итог" },
		func(int) string { return "оценка" },
		func(int) string { return "риск" },
		func(int) string { return "концентрация" },
	)

	drawAnalytics(t, view, 120, 40)
	wide := view.Structure.GetText(true)

	drawAnalytics(t, view, 60, 40)
	narrow := view.Structure.GetText(true)

	if len(wide) <= len(narrow) {
		t.Errorf("the panel did not re-lay out: %d columns wide vs %d narrow", len(wide), len(narrow))
	}
	if len(widths) < 2 {
		t.Fatalf("the renderer ran %d times, want one per width", len(widths))
	}

	// The width handed to the renderer must be the panel's inner width — what
	// is left after the border and the one column of padding on each side.
	_, _, w, _ := view.Structure.GetInnerRect()
	if got := widths[len(widths)-1]; got != w {
		t.Errorf("the renderer was given width %d, but the panel's inner width is %d", got, w)
	}
}

// A panel is drawn as tall as what it holds. The old layout stretched every
// panel to the column and framed three lines of text in a box twenty rows
// deep, which is the complaint the redesign started from.
func TestOverviewPanelsFitTheirContent(t *testing.T) {
	view := NewAnalyticsView()
	view.setOverviewStatic("одна\nдве\nтри", "валюты", "сектор", "итог", "оценка", "риск", "концентрация")

	drawAnalytics(t, view, 120, 40)

	if got := panelHeight(view.Structure); got != 5 {
		t.Errorf("a three-line panel is %d rows tall, want 5 (content plus two borders)", got)
	}
	if got := panelHeight(view.Sectors); got != 3 {
		t.Errorf("a one-line panel is %d rows tall, want 3", got)
	}
}

// The cap is what stops a long list pushing the panel below it off the screen.
func TestOverviewPanelsCapLongContent(t *testing.T) {
	view := NewAnalyticsView()
	view.setOverviewStatic(strings.Repeat("строка\n", 40), "валюты", "сектор", "итог", "оценка", "риск", "концентрация")

	drawAnalytics(t, view, 120, 60)

	if got := panelHeight(view.Structure); got != maxStructureLines+2 {
		t.Errorf("a forty-line panel is %d rows tall, want the cap of %d", got, maxStructureLines+2)
	}
}

// Panels are re-fitted on every redraw, so a portfolio that shrinks does not
// keep the frame built for the larger one.
func TestOverviewPanelsShrinkOnRedraw(t *testing.T) {
	view := NewAnalyticsView()

	view.setOverviewStatic("одна\nдве\nтри\nчетыре\nпять", "валюты", "сектор", "итог", "оценка", "риск", "концентрация")
	drawAnalytics(t, view, 120, 40)
	tall := panelHeight(view.Structure)

	view.setOverviewStatic("одна", "валюты", "сектор", "итог", "оценка", "риск", "концентрация")
	drawAnalytics(t, view, 120, 40)
	short := panelHeight(view.Structure)

	if short >= tall {
		t.Errorf("the panel kept its old height: %d rows for one line, %d for five", short, tall)
	}
	if short != 3 {
		t.Errorf("a one-line panel is %d rows tall, want 3", short)
	}
}

// The two-column overview has to survive the narrow terminal the tab was
// originally sized for. The accounts sidebar takes about 28 columns, so an
// 80-column window leaves each column around 26 — enough for a frame and a
// row, and the layout must still put every panel somewhere rather than
// dropping one.
func TestOverviewSurvivesANarrowTerminal(t *testing.T) {
	view := NewAnalyticsView()
	view.setOverviewStatic("Акции  18 200  10.2%", "RUB  178 200  100.0%", "Энергетика  44 120", "за 11 дн.", "Текущая  500 000.00", "Доступный кэш  120 000.00", "LKOH  25 920")

	drawAnalytics(t, view, 52, 24)

	panels := map[string]*analyticsPanel{
		"Структура":    view.Structure,
		"Валюты":       view.Currencies,
		"Секторы":      view.Sectors,
		"Итог":         view.SinceOpen,
		"Оценка":       view.Valuation,
		"Риск":         view.Risk,
		"Концентрация": view.Concentration,
	}
	for name, panel := range panels {
		x, _, w, h := panel.GetRect()
		if w <= 0 || h <= 0 {
			t.Errorf("panel %s was not laid out at 52 columns: %dx%d", name, w, h)
		}
		if x+w > 52 {
			t.Errorf("panel %s runs past the right edge: x=%d w=%d", name, x, w)
		}
	}
}

func TestDistributeHeights(t *testing.T) {
	tests := []struct {
		name      string
		desired   []int
		available int
		want      []int
	}{
		{"everyone fits", []int{5, 4, 6}, 20, []int{5, 4, 6}},
		{"exactly fits", []int{5, 4, 6}, 15, []int{5, 4, 6}},
		{"the last one takes what is left", []int{5, 4, 6}, 12, []int{5, 4, 3}},
		{"a stump is refused", []int{5, 4, 6}, 11, []int{5, 4, 0}},
		{"nothing at all", []int{5, 4, 6}, 2, []int{0, 0, 0}},
		{"a negative column asks for nothing", []int{5}, -4, []int{0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := distributeHeights(tt.desired, tt.available)
			if len(got) != len(tt.want) {
				t.Fatalf("distributeHeights returned %d heights, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("distributeHeights(%v, %d) = %v, want %v", tt.desired, tt.available, got, tt.want)
					break
				}
			}
			var total int
			for _, h := range got {
				total += h
			}
			if total > tt.available && tt.available > 0 {
				t.Errorf("handed out %d rows from a column of %d", total, tt.available)
			}
		})
	}
}

// The reason the heights are decided at draw time: a column whose panels want
// more rows than it has must not let the last of them carry on drawing past the
// bottom and paint over the frame around the tab.
func TestPanelsNeverOverflowTheFrame(t *testing.T) {
	view := NewAnalyticsView()
	tall := strings.Repeat("строка\n", 12)
	view.setOverviewStatic(tall, tall, tall, tall, tall, tall, tall)

	const height = 18
	drawAnalytics(t, view, 118, height)

	// The frame's own bottom row is the last thing the tab may touch.
	_, innerY, _, innerHeight := view.GetInnerRect()
	bottom := innerY + innerHeight

	for name, panel := range map[string]*analyticsPanel{
		"Структура":    view.Structure,
		"Валюты":       view.Currencies,
		"Секторы":      view.Sectors,
		"Итог":         view.SinceOpen,
		"Оценка":       view.Valuation,
		"Риск":         view.Risk,
		"Концентрация": view.Concentration,
	} {
		_, y, _, h := panel.GetRect()
		if h == 0 {
			continue // dropped for want of room, which is the allowed outcome
		}
		if y+h > bottom {
			t.Errorf("panel %s ends at row %d, past the frame's last row %d", name, y+h, bottom)
		}
	}
}

// Panels must not wrap. panelRows sizes a panel from its logical line count, so
// a row folded onto a second screen row would be height the panel never
// reserved and the bottom of the content would be clipped instead — quite
// apart from what folding does to a column of aligned figures.
func TestOverviewPanelsDoNotWrap(t *testing.T) {
	view := NewAnalyticsView()

	long := "Очень длинная строка, которая заведомо не помещается в узкую панель"
	view.setOverviewStatic(long, "валюты", "сектор", "итог", "оценка", "риск", "концентрация")
	drawAnalytics(t, view, 52, 24)

	if got := panelHeight(view.Structure); got != 3 {
		t.Errorf("a one-line panel grew to %d rows at 52 columns — the row wrapped", got)
	}
}

// A bordered table drawn full height with only its column titles in it says
// the screen is broken rather than empty, so it is collapsed instead.
func TestEmptyTradesTableIsCollapsed(t *testing.T) {
	view := NewAnalyticsView()
	view.SetScreen(AnalyticsTrades)

	hasRows := renderTradeTable(view.TradesTable, analytics.FIFOResult{}, time.Now().AddDate(0, -1, 0), time.Now())
	if hasRows {
		t.Fatal("an empty FIFO result reported rows")
	}
	view.fitTradesTable(hasRows)

	drawAnalytics(t, view, 120, 40)

	if _, _, _, h := view.TradesTable.GetRect(); h != 0 {
		t.Errorf("the empty trades table is %d rows tall, want 0", h)
	}
	if got := view.TradesTable.GetRowCount(); got != 0 {
		t.Errorf("the empty trades table kept %d rows, want none — not even a header", got)
	}
}

func TestEmptyPayoutTableIsCollapsed(t *testing.T) {
	view := NewAnalyticsView()
	view.SetScreen(AnalyticsPayouts)

	hasRows := renderPayoutTable(view.PayoutTable, nil)
	if hasRows {
		t.Fatal("an empty payout list reported rows")
	}
	view.fitPayoutTable(hasRows)

	drawAnalytics(t, view, 120, 40)

	if _, _, _, h := view.PayoutTable.GetRect(); h != 0 {
		t.Errorf("the empty payout table is %d rows tall, want 0", h)
	}
}

// The other half of the rule: a table with rows in it takes the rest of the
// screen, because the table is the part that grows.
func TestPopulatedPayoutTableTakesTheRest(t *testing.T) {
	view := NewAnalyticsView()
	view.SetScreen(AnalyticsPayouts)

	payouts := []analytics.Payout{{
		Symbol: "SBER@MISX", Ticker: "SBER", Kind: analytics.PayoutDividend,
		When: time.Now().AddDate(0, 0, 10), Quantity: 10,
		PerUnit: 3.5, Amount: 35, AmountValid: true, Currency: "RUB",
	}}
	view.fitPayoutTable(renderPayoutTable(view.PayoutTable, payouts))
	view.PayoutTotals.SetStatic("30 дней  35.00 RUB")

	drawAnalytics(t, view, 120, 40)

	if _, _, _, h := view.PayoutTable.GetRect(); h < 10 {
		t.Errorf("the populated payout table is only %d rows tall", h)
	}
}

// The money groups are drawn as bars against the largest of them, so the row
// that dominates the period is seen rather than worked out.
func TestMoneyGroupsDrawScaledBars(t *testing.T) {
	flow := analytics.CashFlow{ByCurrency: map[string]analytics.CurrencyFlow{
		"RUB": {
			Currency: "RUB",
			Groups: map[analytics.FlowGroup]float64{
				analytics.GroupDeposit:    50000,
				analytics.GroupCommission: -500,
			},
			NetDeposit: 50000,
			Costs:      -500,
		},
	}}

	from := time.Now().AddDate(0, -1, 0)
	text := renderPeriodMoney(flow, analytics.TradeStats{Currency: "RUB"}, "RUB", from, time.Now(), 48)

	if !strings.Contains(text, "Ввод") || !strings.Contains(text, "Комиссии") {
		t.Fatalf("the group block is missing its rows:\n%s", text)
	}
	// The largest group fills the bar out to the panel edge; the small one
	// must not. The expected width is computed rather than hardcoded, because
	// the whole point of the bar is that it takes whatever the fixed columns
	// leave.
	full := flowBarWidth(moneyLabelWidth, 48)
	if full < minFlowBar {
		t.Fatalf("the test width leaves no room for a bar: %d", full)
	}
	if !strings.Contains(text, "[green]"+strings.Repeat("█", full)+"[-]") {
		t.Errorf("the largest group did not fill the bar to %d cells:\n%s", full, text)
	}
	if strings.Contains(text, "[red]"+strings.Repeat("█", full)+"[-]") {
		t.Errorf("a group a hundredth of the largest filled the bar too:\n%s", text)
	}
}

// A period with no movement says so instead of drawing an empty group block.
func TestMoneyGroupsEmptyPeriod(t *testing.T) {
	flow := analytics.CashFlow{ByCurrency: map[string]analytics.CurrencyFlow{
		"RUB": {Currency: "RUB", Groups: map[analytics.FlowGroup]float64{}},
	}}

	from := time.Now().AddDate(0, -1, 0)
	text := renderPeriodMoney(flow, analytics.TradeStats{Currency: "RUB"}, "RUB", from, time.Now(), 48)

	if !strings.Contains(text, "движения денег не было") {
		t.Errorf("an empty period does not say so:\n%s", text)
	}
}
