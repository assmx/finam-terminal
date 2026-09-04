package ui

import (
	"strings"
	"testing"

	"finam-terminal/analytics"
	"finam-terminal/models"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// TestAnalyticsHeader_FinalScreens pins the five sub-screens and their order.
func TestAnalyticsHeader_FinalScreens(t *testing.T) {
	want := []struct {
		screen AnalyticsScreen
		label  string
	}{
		{AnalyticsOverview, "Обзор"},
		{AnalyticsTrades, "Сделки"},
		{AnalyticsMoney, "Деньги"},
		{AnalyticsPayouts, "Выплаты"},
		{AnalyticsQuotas, "API"},
	}

	if len(analyticsScreens) != len(want) {
		t.Fatalf("got %d sub-screens, want %d", len(analyticsScreens), len(want))
	}
	for i, w := range want {
		if analyticsScreens[i].Screen != w.screen || analyticsScreens[i].Label != w.label {
			t.Errorf("screen %d = %v %q, want %v %q",
				i, analyticsScreens[i].Screen, analyticsScreens[i].Label, w.screen, w.label)
		}
	}

	view := NewAnalyticsView()
	header := view.Header.GetText(true)
	for i, w := range want {
		if !strings.Contains(header, w.label) {
			t.Errorf("header %q does not mention %q", header, w.label)
		}
		if !strings.Contains(header, itoaUI(i+1)) {
			t.Errorf("header %q does not carry the digit %d", header, i+1)
		}
	}
}

// TestAnalyticsHeader_ShowsPeriod puts the active period in the header, since
// every number on two of the sub-screens depends on it.
func TestAnalyticsHeader_ShowsPeriod(t *testing.T) {
	view := NewAnalyticsView()

	view.SetPeriod(analytics.PresetQuarter)
	if header := view.Header.GetText(true); !strings.Contains(header, "3М") {
		t.Errorf("header %q does not show the period", header)
	}

	view.SetPeriod(analytics.PresetYTD)
	header := view.Header.GetText(true)
	if !strings.Contains(header, "YTD") {
		t.Errorf("header %q does not show the new period", header)
	}
	if strings.Contains(header, "3М") {
		t.Errorf("header %q still shows the old period", header)
	}
}

// TestAnalyticsKeys_AllFiveDigits reaches every sub-screen by its number.
func TestAnalyticsKeys_AllFiveDigits(t *testing.T) {
	app, capture := analyticsApp(t, &mockClient{})
	view := app.portfolioView.TabbedView.Analytics

	for i, want := range []AnalyticsScreen{
		AnalyticsOverview, AnalyticsTrades, AnalyticsMoney, AnalyticsPayouts, AnalyticsQuotas,
	} {
		capture(tcell.NewEventKey(tcell.KeyRune, rune('1'+i), tcell.ModNone))
		if view.ActiveScreen != want {
			t.Errorf("digit %d selected %v, want %v", i+1, view.ActiveScreen, want)
		}
	}

	// Six is past the end and must leave the tab where it was.
	before := view.ActiveScreen
	capture(tcell.NewEventKey(tcell.KeyRune, '6', tcell.ModNone))
	if view.ActiveScreen != before {
		t.Errorf("ActiveScreen = %v after an out-of-range digit, want it unchanged", view.ActiveScreen)
	}
}

// TestAnalyticsKeys_PeriodCycle walks the preset order with the P key, in both
// keyboard layouts.
func TestAnalyticsKeys_PeriodCycle(t *testing.T) {
	app, capture := analyticsApp(t, &mockClient{})

	if got := app.AnalyticsPeriod(); got != analytics.DefaultPreset {
		t.Errorf("initial period = %s, want the default %s", got.Label(), analytics.DefaultPreset.Label())
	}

	want := []analytics.Preset{
		analytics.PresetYear, analytics.PresetYTD, analytics.PresetAll,
		analytics.PresetMonth, analytics.PresetQuarter,
	}
	for i, w := range want {
		capture(tcell.NewEventKey(tcell.KeyRune, 'P', tcell.ModNone))
		if got := app.AnalyticsPeriod(); got != w {
			t.Fatalf("press %d: period = %s, want %s", i+1, got.Label(), w.Label())
		}
	}

	// The Cyrillic key in the same physical position.
	capture(tcell.NewEventKey(tcell.KeyRune, 'з', tcell.ModNone))
	if got := app.AnalyticsPeriod(); got != analytics.PresetYear {
		t.Errorf("period = %s after 'з', want the cycle to continue", got.Label())
	}
}

// TestAnalyticsKeys_PeriodCostsNoRequests is the budget promise for the period
// control: every preset is a filter over history already in memory.
func TestAnalyticsKeys_PeriodCostsNoRequests(t *testing.T) {
	mock := &mockClient{}
	app, capture := analyticsApp(t, mock)

	capture(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))
	before := mock.LoadHistoryCalls.Load()

	for range 10 {
		capture(tcell.NewEventKey(tcell.KeyRune, 'P', tcell.ModNone))
	}

	if got := mock.LoadHistoryCalls.Load(); got != before {
		t.Errorf("LoadHistory called %d times across ten period changes, want %d", got, before)
	}
	if got := mock.GetUsageMetricsCalls.Load(); got != 0 {
		t.Errorf("GetUsageMetrics called %d times from the period key, want 0", got)
	}
	_ = app
}

// TestAnalyticsKeys_PeriodIgnoredOnOtherTabs keeps P from hijacking input
// elsewhere.
func TestAnalyticsKeys_PeriodIgnoredOnOtherTabs(t *testing.T) {
	mock := &mockClient{}
	app := NewApp(mock, []models.AccountInfo{{ID: "acc1"}})
	setupInputHandlers(app)
	app.portfolioView.TabbedView.SetTab(TabPositions)
	app.app.SetFocus(app.portfolioView.TabbedView.PositionsTable)

	event := tcell.NewEventKey(tcell.KeyRune, 'P', tcell.ModNone)
	if got := app.app.GetInputCapture()(event); got == nil {
		t.Error("the P key was swallowed on the Positions tab")
	}
	if got := app.AnalyticsPeriod(); got != analytics.DefaultPreset {
		t.Errorf("period = %s, want it untouched from another tab", got.Label())
	}
}

// TestAnalyticsFocus_PerScreen: a sub-screen with a table gives it focus so
// ↑/↓ scroll, and one without keeps focus on its container.
func TestAnalyticsFocus_PerScreen(t *testing.T) {
	view := NewAnalyticsView()

	cases := []struct {
		screen AnalyticsScreen
		want   tview.Primitive
	}{
		{AnalyticsOverview, view.overview},
		{AnalyticsTrades, view.TradesTable},
		{AnalyticsMoney, view.money},
		{AnalyticsPayouts, view.PayoutTable},
		{AnalyticsQuotas, view.QuotaTable},
	}
	for _, c := range cases {
		view.SetScreen(c.screen)
		if got := view.Focusable(); got != c.want {
			t.Errorf("screen %v focuses %T, want %T", c.screen, got, c.want)
		}
	}
}

// TestStatusBar_AnalyticsFinalShortcuts shows the full set of hints.
func TestStatusBar_AnalyticsFinalShortcuts(t *testing.T) {
	app := NewApp(&mockClient{}, []models.AccountInfo{{ID: "acc1"}})
	app.portfolioView.TabbedView.SetTab(TabAnalytics)
	app.app.SetFocus(app.portfolioView.TabbedView.Analytics.Focusable())

	updateStatusBar(app)

	text := app.statusBar.GetText(false)
	for _, want := range []string{"1-5", "Экран", "P", "Период", "R", "Обновить", "Enter", "Профиль"} {
		if !strings.Contains(text, want) {
			t.Errorf("status bar %q does not mention %q", text, want)
		}
	}
}

// itoaUI is a tiny helper so the header test can look for a digit without
// pulling in strconv for one call.
func itoaUI(v int) string {
	return string(rune('0' + v))
}
