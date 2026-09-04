package ui

import (
	"strings"
	"testing"

	"finam-terminal/models"

	"github.com/gdamore/tcell/v2"
)

// mustReach waits for the quota loader to have been called n times and to have
// finished. Waiting only for the counter is not enough: it is incremented
// inside the request, while the loader is still marked in flight, and a second
// R arriving in that window is deliberately dropped rather than stacking a
// duplicate request.
func mustReach(t *testing.T, app *App, mock *mockClient, n int64) {
	t.Helper()

	if !waitFor(func() bool { return mock.GetUsageMetricsCalls.Load() >= n }) {
		t.Fatalf("GetUsageMetrics called %d times, want %d", mock.GetUsageMetricsCalls.Load(), n)
	}
	if !waitFor(func() bool {
		app.dataMutex.RLock()
		defer app.dataMutex.RUnlock()
		return !app.analytics.quotasLoading
	}) {
		t.Fatal("the quota load never settled")
	}
}

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
// between sub-screens redraws from memory. Only the first entry to the API
// screen fetches, and going back and forth after that must not fetch again.
func TestAnalyticsKeys_SwitchingCostsNoRequests(t *testing.T) {
	mock := &mockClient{
		GetUsageMetricsFunc: func() ([]models.QuotaUsage, error) {
			return []models.QuotaUsage{{Name: "AccountsService.getAccount", Limit: 200, Remaining: 200}}, nil
		},
	}
	app, capture := analyticsApp(t, mock)

	capture(tcell.NewEventKey(tcell.KeyRune, '5', tcell.ModNone))
	mustReach(t, app, mock, 1)

	for range 5 {
		capture(tcell.NewEventKey(tcell.KeyRune, '1', tcell.ModNone))
		capture(tcell.NewEventKey(tcell.KeyRune, '5', tcell.ModNone))
	}

	if got := mock.GetUsageMetricsCalls.Load(); got != 1 {
		t.Errorf("GetUsageMetrics called %d times, want 1: re-entry must reuse the cached answer", got)
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

// TestAnalyticsRefresh_ReloadsQuotas checks R on the API sub-screen costs
// exactly one more request each time.
func TestAnalyticsRefresh_ReloadsQuotas(t *testing.T) {
	mock := &mockClient{
		GetUsageMetricsFunc: func() ([]models.QuotaUsage, error) {
			return []models.QuotaUsage{{Name: "AccountsService.getAccount", Limit: 200, Remaining: 200}}, nil
		},
	}
	app, capture := analyticsApp(t, mock)

	capture(tcell.NewEventKey(tcell.KeyRune, '5', tcell.ModNone))
	mustReach(t, app, mock, 1)

	capture(tcell.NewEventKey(tcell.KeyRune, 'R', tcell.ModNone))
	mustReach(t, app, mock, 2)

	// The Cyrillic key in the same physical position works too, so the shortcut
	// survives a Russian keyboard layout.
	capture(tcell.NewEventKey(tcell.KeyRune, 'К', tcell.ModNone))
	mustReach(t, app, mock, 3)
}

// TestAnalyticsRefresh_OverviewDoesNotFetchQuotas keeps R on the overview from
// spending the quota request that belongs to the other sub-screen.
func TestAnalyticsRefresh_OverviewDoesNotFetchQuotas(t *testing.T) {
	mock := &mockClient{}
	_, capture := analyticsApp(t, mock)

	capture(tcell.NewEventKey(tcell.KeyRune, 'R', tcell.ModNone))

	if got := mock.GetUsageMetricsCalls.Load(); got != 0 {
		t.Errorf("GetUsageMetrics called %d times from the overview, want 0", got)
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

// TestAnalyticsState_QuotaCacheIsSessionWide reflects what the API reports:
// quotas belong to the token, not to an account, so switching accounts must
// not throw the answer away and pay for it again.
func TestAnalyticsState_QuotaCacheIsSessionWide(t *testing.T) {
	mock := &mockClient{
		GetUsageMetricsFunc: func() ([]models.QuotaUsage, error) {
			return []models.QuotaUsage{{Name: "AccountsService.getAccount", Limit: 200, Remaining: 200}}, nil
		},
	}
	app := NewApp(mock, []models.AccountInfo{{ID: "acc1"}, {ID: "acc2"}})
	setupInputHandlers(app)
	app.portfolioView.TabbedView.SetTab(TabAnalytics)
	app.portfolioView.TabbedView.Analytics.SetScreen(AnalyticsQuotas)

	app.ensureQuotasLoaded()
	mustReach(t, app, mock, 1)

	app.dataMutex.Lock()
	app.selectedIdx = 1
	app.dataMutex.Unlock()

	app.ensureQuotasLoaded()

	if got := mock.GetUsageMetricsCalls.Load(); got != 1 {
		t.Errorf("GetUsageMetrics called %d times across two accounts, want 1", got)
	}
}
