package ui

import (
	"strings"
	"testing"

	"finam-terminal/models"

	"github.com/gdamore/tcell/v2"
)

// analyticsApp builds an app sitting on the Analytics tab with input handlers
// wired, which is the state every key test needs.
func analyticsApp(t *testing.T, mock *mockClient) (*App, func(*tcell.EventKey) *tcell.EventKey) {
	t.Helper()

	app := NewApp(mock, []models.AccountInfo{{ID: "acc1"}})
	setupInputHandlers(app)
	app.portfolioView.TabbedView.SetTab(TabAnalytics)
	app.app.SetFocus(app.portfolioView.TabbedView.Analytics.Focusable())

	return app, app.app.GetInputCapture()
}

// TestAnalyticsKeys_SwitchingCostsNoRequests is the budget promise: moving
// between sub-screens redraws from memory. Only the first entry to a screen
// backed by a loader fetches, and cycling through all four after that must not
// fetch again.
func TestAnalyticsKeys_SwitchingCostsNoRequests(t *testing.T) {
	mock := historyMock()
	app, capture := historyApp(t, mock)

	capture(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))
	waitHistory(t, app, mock, 1)

	for range 5 {
		for _, digit := range []rune{'1', '2', '3', '4'} {
			capture(tcell.NewEventKey(tcell.KeyRune, digit, tcell.ModNone))
		}
	}

	if got := mock.LoadHistoryCalls.Load(); got != 1 {
		t.Errorf("LoadHistory called %d times, want 1: re-entry must reuse the cached answer", got)
	}
	if got := mock.GetIndexConstituentsCalls.Load(); got > 1 {
		t.Errorf("GetIndexConstituents called %d times, want at most 1", got)
	}
}

// TestAnalyticsKeys_IgnoredOnOtherTabs keeps the digit keys from hijacking
// input elsewhere.
func TestAnalyticsKeys_IgnoredOnOtherTabs(t *testing.T) {
	mock := &mockClient{}
	app := NewApp(mock, []models.AccountInfo{{ID: "acc1"}})
	setupInputHandlers(app)
	app.portfolioView.TabbedView.SetTab(TabPositions)
	app.app.SetFocus(app.portfolioView.TabbedView.PositionsTable)

	event := tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone)
	if got := app.app.GetInputCapture()(event); got == nil {
		t.Error("the digit key was swallowed on the Positions tab")
	}
	if got := app.portfolioView.TabbedView.Analytics.ActiveScreen; got != AnalyticsOverview {
		t.Errorf("ActiveScreen = %v, want it untouched from another tab", got)
	}
}

// TestAnalyticsRefresh_OverviewDoesNotFetchHistory keeps R on the overview
// from spending the history pass that belongs to the other sub-screens.
func TestAnalyticsRefresh_OverviewDoesNotFetchHistory(t *testing.T) {
	mock := historyMock()
	_, capture := historyApp(t, mock)

	capture(tcell.NewEventKey(tcell.KeyRune, 'R', tcell.ModNone))

	if got := mock.LoadHistoryCalls.Load(); got != 0 {
		t.Errorf("LoadHistory called %d times from the overview, want 0", got)
	}
}

// TestStatusBar_NoAnalyticsShortcutsElsewhere keeps the hints off other tabs.
func TestStatusBar_NoAnalyticsShortcutsElsewhere(t *testing.T) {
	app := NewApp(&mockClient{}, []models.AccountInfo{{ID: "acc1"}})
	app.portfolioView.TabbedView.SetTab(TabPositions)
	app.app.SetFocus(app.portfolioView.TabbedView.PositionsTable)

	updateStatusBar(app)

	if text := app.statusBar.GetText(false); strings.Contains(text, "Экран") {
		t.Errorf("status bar %q shows the Analytics hints on the Positions tab", text)
	}
}
