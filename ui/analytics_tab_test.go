package ui

import (
	"reflect"
	"strings"
	"testing"

	"finam-terminal/models"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// TestTabbedView_AnalyticsIsTheFifthTab verifies the tab exists as a page of
// its own and that switching to it shows the analytics view.
func TestTabbedView_AnalyticsIsTheFifthTab(t *testing.T) {
	tv := NewTabbedView()

	if tv.Analytics == nil {
		t.Fatal("TabbedView.Analytics is nil")
	}

	tv.SetTab(TabAnalytics)

	if tv.ActiveTab != TabAnalytics {
		t.Errorf("ActiveTab = %v, want TabAnalytics", tv.ActiveTab)
	}
	name, _ := tv.Content.GetFrontPage()
	if name != "analytics" {
		t.Errorf("front page = %q, want \"analytics\"", name)
	}
}

// TestTabCount_IncludesAnalytics guards the single source of truth the ←/→
// cycle wraps on.
func TestTabCount_IncludesAnalytics(t *testing.T) {
	if got := TabCount(); got != 5 {
		t.Errorf("TabCount() = %d, want 5", got)
	}
	if int(TabAnalytics) != 4 {
		t.Errorf("TabAnalytics = %d, want 4 (last in the cycle)", TabAnalytics)
	}
}

// TestTabbedView_HeaderShowsAnalyticsTab verifies the header renders all five
// tabs and highlights Analytics when it is active.
func TestTabbedView_HeaderShowsAnalyticsTab(t *testing.T) {
	tv := NewTabbedView()
	tv.SetTab(TabAnalytics)

	header := tv.Header.GetText(false)
	for _, tab := range []string{" Positions ", " History ", " Orders ", " Index ", " Analytics "} {
		if !strings.Contains(header, tab) {
			t.Errorf("header %q does not contain %q", header, tab)
		}
	}
	if !strings.Contains(header, "[black:yellow] Analytics [-]") {
		t.Errorf("header %q does not highlight the Analytics tab", header)
	}
}

// TestAnalyticsView_SubScreenHeader checks the sub-screen bar. This track ships
// two screens; the second track inserts the rest between them.
func TestAnalyticsView_SubScreenHeader(t *testing.T) {
	view := NewAnalyticsView()

	header := view.Header.GetText(false)
	for _, want := range []string{"[1] Обзор", "[2] Сделки", "[3] Деньги", "[4] Выплаты", "[5] API"} {
		if !strings.Contains(header, want) {
			t.Errorf("sub-screen header %q does not contain %q", header, want)
		}
	}
}

// TestAnalyticsView_HighlightsActiveSubScreen verifies the bar marks which
// sub-screen is on.
func TestAnalyticsView_HighlightsActiveSubScreen(t *testing.T) {
	view := NewAnalyticsView()

	view.SetScreen(AnalyticsQuotas)
	if name, _ := view.Pages.GetFrontPage(); name != "quotas" {
		t.Errorf("front page = %q, want \"quotas\"", name)
	}
	if header := view.Header.GetText(false); !strings.Contains(header, "[black:yellow][5] API[-]") {
		t.Errorf("header %q does not highlight the API sub-screen", header)
	}

	view.SetScreen(AnalyticsOverview)
	if name, _ := view.Pages.GetFrontPage(); name != "overview" {
		t.Errorf("front page = %q, want \"overview\"", name)
	}
	if header := view.Header.GetText(false); !strings.Contains(header, "[black:yellow][1] Обзор[-]") {
		t.Errorf("header %q does not highlight the overview sub-screen", header)
	}
}

// TestAnalyticsView_OverviewHasTwoColumns pins the layout the renderer writes
// into: structure on the left, margin and risk on the right.
func TestAnalyticsView_OverviewHasTwoColumns(t *testing.T) {
	view := NewAnalyticsView()

	if view.Structure == nil {
		t.Error("AnalyticsView.Structure is nil")
	}
	if view.Risk == nil {
		t.Error("AnalyticsView.Risk is nil")
	}
	if view.Structure == view.Risk {
		t.Error("the two overview columns must be separate views")
	}
}

// TestAnalyticsView_QuotaTableHasFixedHeader keeps the column titles on screen
// once the quota list scrolls — tview drops unfixed rows as they scroll away.
func TestAnalyticsView_QuotaTableHasFixedHeader(t *testing.T) {
	view := NewAnalyticsView()

	if view.QuotaTable == nil {
		t.Fatal("AnalyticsView.QuotaTable is nil")
	}
	// tview exposes no getter for this, and the effect only shows on a table
	// long enough to scroll, so the field is read directly rather than left
	// untested.
	if got := fixedRows(view.QuotaTable); got != 1 {
		t.Errorf("QuotaTable fixed rows = %d, want 1", got)
	}
}

// TestAnalyticsView_StatusLines gives each sub-screen its own status line, so a
// quota load error cannot appear over the overview.
func TestAnalyticsView_StatusLines(t *testing.T) {
	view := NewAnalyticsView()

	if view.OverviewStatus == nil {
		t.Error("AnalyticsView.OverviewStatus is nil")
	}
	if view.QuotaStatus == nil {
		t.Error("AnalyticsView.QuotaStatus is nil")
	}
	if view.OverviewStatus == view.QuotaStatus {
		t.Error("the two status lines must be separate views")
	}
}

// TestInputHandler_TabCycleIncludesAnalytics verifies the forward cycle visits
// all five tabs and wraps back to Positions, with focus following the tab.
func TestInputHandler_TabCycleIncludesAnalytics(t *testing.T) {
	app := NewApp(&mockClient{}, nil)
	setupInputHandlers(app)
	app.app.SetFocus(app.portfolioView.TabbedView.PositionsTable)
	capture := app.app.GetInputCapture()

	tv := app.portfolioView.TabbedView
	want := []struct {
		tab   TabType
		focus tview.Primitive
	}{
		{TabHistory, tv.HistoryTable},
		{TabOrders, tv.OrdersTable},
		{TabIndex, tv.IndexTable},
		{TabAnalytics, tv.Analytics.Focusable()},
		{TabPositions, tv.PositionsTable},
	}

	for i, step := range want {
		capture(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone))
		if tv.ActiveTab != step.tab {
			t.Fatalf("step %d: ActiveTab = %v, want %v", i, tv.ActiveTab, step.tab)
		}
		if app.app.GetFocus() != step.focus {
			t.Errorf("step %d: focus did not follow the %v tab", i, step.tab)
		}
	}
}

// TestInputHandler_PrevTabReachesAnalyticsFromPositions verifies the backward
// cycle now wraps to Analytics rather than to Index.
func TestInputHandler_PrevTabReachesAnalyticsFromPositions(t *testing.T) {
	app := NewApp(&mockClient{}, nil)
	setupInputHandlers(app)
	app.app.SetFocus(app.portfolioView.TabbedView.PositionsTable)
	capture := app.app.GetInputCapture()

	capture(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone))

	if got := app.portfolioView.TabbedView.ActiveTab; got != TabAnalytics {
		t.Errorf("ActiveTab = %v, want TabAnalytics after wrapping backwards", got)
	}
}

// TestActiveTabTable_FollowsAnalyticsSubScreen is what returns focus after a
// profile or a modal closes: the tab decides which primitive is focusable, and
// on Analytics that depends on the sub-screen.
func TestActiveTabTable_FollowsAnalyticsSubScreen(t *testing.T) {
	app := NewApp(&mockClient{}, []models.AccountInfo{{ID: "acc1"}})
	tv := app.portfolioView.TabbedView

	tv.SetTab(TabAnalytics)

	tv.Analytics.SetScreen(AnalyticsQuotas)
	if got := app.activeTabTable(); got != tview.Primitive(tv.Analytics.QuotaTable) {
		t.Error("on the API sub-screen focus must go to the quota table")
	}

	tv.Analytics.SetScreen(AnalyticsOverview)
	if got := app.activeTabTable(); got == tview.Primitive(tv.Analytics.QuotaTable) {
		t.Error("on the overview focus must not stay on the quota table")
	}

	// The other tabs keep their existing behaviour.
	tv.SetTab(TabPositions)
	if got := app.activeTabTable(); got != tview.Primitive(tv.PositionsTable) {
		t.Error("the Positions tab must still focus the positions table")
	}
}

// fixedRows reads tview.Table's unexported fixedRows, which SetFixed writes and
// nothing exposes.
func fixedRows(table *tview.Table) int {
	return int(reflect.ValueOf(table).Elem().FieldByName("fixedRows").Int())
}
