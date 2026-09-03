//go:build integration

package api

import (
	"testing"
	"time"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/metrics"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestIntegration_GetUsageMetrics walks the quota list over bufconn: the API
// order is preserved (sorting is the UI's decision) and a quota with no
// reset_time arrives with a zero time rather than the epoch.
func TestIntegration_GetUsageMetrics(t *testing.T) {
	client, ts := setupTestServer(t)

	quotas, err := client.GetUsageMetrics()
	if err != nil {
		t.Fatalf("GetUsageMetrics failed: %v", err)
	}

	if len(quotas) != 3 {
		t.Fatalf("len(quotas) = %d, want 3", len(quotas))
	}

	// API order, not remaining-share order.
	wantNames := []string{
		"MarketDataService.lastQuote",
		"AccountsService.getAccount",
		"ReportsService.createAccountReport",
	}
	for i, want := range wantNames {
		if quotas[i].Name != want {
			t.Errorf("quotas[%d].Name = %q, want %q", i, quotas[i].Name, want)
		}
	}

	if quotas[0].Limit != 200 || quotas[0].Remaining != 100 {
		t.Errorf("quotas[0] = %d/%d, want 200/100", quotas[0].Remaining, quotas[0].Limit)
	}
	wantReset := time.Date(2026, 9, 3, 17, 11, 21, 0, time.UTC)
	if !quotas[0].ResetAt.Equal(wantReset) {
		t.Errorf("quotas[0].ResetAt = %v, want %v", quotas[0].ResetAt, wantReset)
	}
	if !quotas[2].ResetAt.IsZero() {
		t.Errorf("quotas[2].ResetAt = %v, want zero for an untouched quota", quotas[2].ResetAt)
	}

	if got := ts.UsageMetrics.GetUsageMetricsCallCount.Load(); got != 1 {
		t.Errorf("GetUsageMetrics called %d times, want 1", got)
	}
}

// TestIntegration_GetUsageMetrics_Error surfaces a rate-limited failure with
// the code intact, so the sub-screen can label it "лимит API".
func TestIntegration_GetUsageMetrics_Error(t *testing.T) {
	client, ts := setupTestServer(t)
	ts.UsageMetrics.GetUsageMetricsError = status.Error(codes.ResourceExhausted, "quota exceeded")

	quotas, err := client.GetUsageMetrics()
	if err == nil {
		t.Fatal("expected an error")
	}
	if quotas != nil {
		t.Errorf("quotas = %+v, want nil on error", quotas)
	}
	if !IsRateLimited(err) {
		t.Errorf("IsRateLimited = false for %v, want true", err)
	}
}

// TestIntegration_GetUsageMetrics_Empty treats an empty list as a valid answer.
func TestIntegration_GetUsageMetrics_Empty(t *testing.T) {
	client, ts := setupTestServer(t)
	ts.UsageMetrics.Quotas = []*metrics.GetUsageMetricsResponse_QuotaUsage{}

	quotas, err := client.GetUsageMetrics()
	if err != nil {
		t.Fatalf("an empty quota list is not an error, got %v", err)
	}
	if len(quotas) != 0 {
		t.Errorf("len(quotas) = %d, want 0", len(quotas))
	}
}
