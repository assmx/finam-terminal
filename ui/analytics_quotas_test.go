package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"finam-terminal/models"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func testQuotas() []models.QuotaUsage {
	reset := time.Now().Add(45 * time.Second)
	return []models.QuotaUsage{
		{Name: "MarketDataService.lastQuote", Limit: 200, Remaining: 100, ResetAt: reset},
		{Name: "AccountsService.getAccount", Limit: 200, Remaining: 12, ResetAt: reset},
		{Name: "ReportsService.createAccountReport", Limit: 3, Remaining: 3},
	}
}

func quotaApp(quotas []models.QuotaUsage, err error) (*App, *mockClient) {
	mock := &mockClient{
		GetUsageMetricsFunc: func() ([]models.QuotaUsage, error) {
			if err != nil {
				return nil, err
			}
			return quotas, nil
		},
	}
	app := NewApp(mock, []models.AccountInfo{{ID: "acc1"}})
	app.portfolioView.TabbedView.SetTab(TabAnalytics)
	app.portfolioView.TabbedView.Analytics.SetScreen(AnalyticsQuotas)
	return app, mock
}

func quotaTableText(app *App) string {
	table := app.portfolioView.TabbedView.Analytics.QuotaTable
	var b strings.Builder
	for row := range table.GetRowCount() {
		for col := range table.GetColumnCount() {
			if cell := table.GetCell(row, col); cell != nil {
				b.WriteString(cell.Text)
				b.WriteString("|")
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// TestQuotaTable_Headers pins the columns.
func TestQuotaTable_Headers(t *testing.T) {
	app, _ := quotaApp(testQuotas(), nil)
	app.analytics.quotas = testQuotas()
	app.analytics.quotasLoaded = true

	updateQuotaTable(app)

	table := app.portfolioView.TabbedView.Analytics.QuotaTable
	want := []string{"Метод", "Лимит", "Остаток", "Сброс"}
	for i, header := range want {
		if got := table.GetCell(0, i).Text; got != header {
			t.Errorf("header %d = %q, want %q", i, got, header)
		}
	}
}

// TestQuotaTable_SortedByRemainingShare puts the quota closest to running out
// at the top, regardless of the order the API sent.
func TestQuotaTable_SortedByRemainingShare(t *testing.T) {
	app, _ := quotaApp(testQuotas(), nil)
	app.analytics.quotas = testQuotas()
	app.analytics.quotasLoaded = true

	updateQuotaTable(app)

	table := app.portfolioView.TabbedView.Analytics.QuotaTable
	// 12/200 = 6%, then 100/200 = 50%, then the untouched 3/3.
	want := []string{
		"AccountsService.getAccount",
		"MarketDataService.lastQuote",
		"ReportsService.createAccountReport",
	}
	for i, name := range want {
		if got := table.GetCell(i+1, 0).Text; got != name {
			t.Errorf("row %d = %q, want %q", i+1, got, name)
		}
	}
}

// TestQuotaTable_ShowsResetCountdownAndDash is the reconnaissance finding
// reaching the screen: a quota untouched this window carries no reset time and
// must show a dash, not a countdown of decades from the Unix epoch.
func TestQuotaTable_ShowsResetCountdownAndDash(t *testing.T) {
	app, _ := quotaApp(testQuotas(), nil)
	app.analytics.quotas = testQuotas()
	app.analytics.quotasLoaded = true

	updateQuotaTable(app)

	text := quotaTableText(app)
	if !strings.Contains(text, "00:45") {
		t.Errorf("quota table %q does not show the reset countdown", text)
	}
	if !strings.Contains(text, "—") {
		t.Errorf("quota table %q does not show a dash for the quota with no reset time", text)
	}
	for _, bad := range []string{"1970", "-"} {
		if strings.Contains(text, bad) && bad == "1970" {
			t.Errorf("quota table %q leaked the epoch", text)
		}
	}
}

// TestQuotaTable_Empty says so plainly instead of showing an empty grid.
func TestQuotaTable_Empty(t *testing.T) {
	app, _ := quotaApp(nil, nil)
	app.analytics.quotas = nil
	app.analytics.quotasLoaded = true

	updateQuotaTable(app)
	updateQuotaStatus(app)

	status := app.portfolioView.TabbedView.Analytics.QuotaStatus.GetText(false)
	if !strings.Contains(status, "квоты не получены") {
		t.Errorf("status %q does not report the empty answer", status)
	}
}

// TestQuotaStatus_Loading shows progress while the request is in flight.
func TestQuotaStatus_Loading(t *testing.T) {
	app, _ := quotaApp(testQuotas(), nil)
	app.analytics.quotasLoading = true

	updateQuotaStatus(app)

	if got := app.portfolioView.TabbedView.Analytics.QuotaStatus.GetText(false); !strings.Contains(got, "Загрузка") {
		t.Errorf("status %q does not show the loading state", got)
	}
}

// TestQuotaStatus_Error shows the failure with the retry hint and no automatic
// retry behind it.
func TestQuotaStatus_Error(t *testing.T) {
	app, mock := quotaApp(nil, errors.New("boom"))

	app.ensureQuotasLoaded()
	if !waitFor(func() bool { return mock.GetUsageMetricsCalls.Load() == 1 }) {
		t.Fatal("the loader never ran")
	}
	if !waitFor(func() bool {
		app.dataMutex.RLock()
		defer app.dataMutex.RUnlock()
		return app.analytics.quotasErr != ""
	}) {
		t.Fatal("the error was never recorded")
	}

	updateQuotaStatus(app)

	got := app.portfolioView.TabbedView.Analytics.QuotaStatus.GetText(false)
	if !strings.Contains(got, "R") {
		t.Errorf("status %q does not offer the retry key", got)
	}

	// Nothing retries on its own.
	time.Sleep(100 * time.Millisecond)
	if n := mock.GetUsageMetricsCalls.Load(); n != 1 {
		t.Errorf("GetUsageMetrics called %d times, want 1: a failure must not retry itself", n)
	}
}

// TestQuotaStatus_RateLimited names the one failure the user can act on.
func TestQuotaStatus_RateLimited(t *testing.T) {
	app, mock := quotaApp(nil, status.Error(codes.ResourceExhausted, "quota exceeded"))

	app.ensureQuotasLoaded()
	if !waitFor(func() bool { return mock.GetUsageMetricsCalls.Load() == 1 }) {
		t.Fatal("the loader never ran")
	}
	if !waitFor(func() bool {
		app.dataMutex.RLock()
		defer app.dataMutex.RUnlock()
		return app.analytics.quotasErr != ""
	}) {
		t.Fatal("the error was never recorded")
	}

	updateQuotaStatus(app)

	if got := app.portfolioView.TabbedView.Analytics.QuotaStatus.GetText(false); !strings.Contains(got, "лимит API") {
		t.Errorf("status %q does not mark the rate-limited failure", got)
	}
}

// TestQuotaStatus_ShowsRequestTime tells the user how old the numbers are —
// nothing refreshes them on its own.
func TestQuotaStatus_ShowsRequestTime(t *testing.T) {
	app, mock := quotaApp(testQuotas(), nil)

	app.ensureQuotasLoaded()
	if !waitFor(func() bool { return mock.GetUsageMetricsCalls.Load() == 1 }) {
		t.Fatal("the loader never ran")
	}
	if !waitFor(func() bool {
		app.dataMutex.RLock()
		defer app.dataMutex.RUnlock()
		return app.analytics.quotasLoaded
	}) {
		t.Fatal("the quotas never landed")
	}

	updateQuotaStatus(app)

	got := app.portfolioView.TabbedView.Analytics.QuotaStatus.GetText(false)
	if !strings.Contains(got, "запрос") {
		t.Errorf("status %q does not show when the answer was fetched", got)
	}
}

// TestQuotaTable_ZeroLimitRowLast keeps a quota with no limit out of the
// ranking and off the colour scale.
func TestQuotaTable_ZeroLimitRowLast(t *testing.T) {
	app, _ := quotaApp(nil, nil)
	app.analytics.quotas = []models.QuotaUsage{
		{Name: "no-limit", Limit: 0, Remaining: 0},
		{Name: "nearly-gone", Limit: 200, Remaining: 5},
	}
	app.analytics.quotasLoaded = true

	updateQuotaTable(app)

	table := app.portfolioView.TabbedView.Analytics.QuotaTable
	if got := table.GetCell(1, 0).Text; got != "nearly-gone" {
		t.Errorf("row 1 = %q, want nearly-gone", got)
	}
	if got := table.GetCell(2, 0).Text; got != "no-limit" {
		t.Errorf("row 2 = %q, want no-limit", got)
	}
}

// TestQuotaTable_TickCostsNothing is the budget promise for this sub-screen:
// the five-second tick never fetches quotas.
func TestQuotaTable_TickCostsNothing(t *testing.T) {
	app, mock := quotaApp(testQuotas(), nil)

	app.ensureQuotasLoaded()
	if !waitFor(func() bool { return mock.GetUsageMetricsCalls.Load() == 1 }) {
		t.Fatal("the loader never ran")
	}

	for range 10 {
		app.applyAccountData("acc1", nil, nil, &models.AccountInfo{ID: "acc1", Equity: "1000"})
		updateQuotaTable(app)
		updateQuotaStatus(app)
	}

	if got := mock.GetUsageMetricsCalls.Load(); got != 1 {
		t.Errorf("GetUsageMetrics called %d times across ten ticks, want 1", got)
	}
}
