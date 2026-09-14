package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/metrics"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestGetUsageMetrics maps the quota list, including the case that shapes the
// renderer: reset_time is nil for every quota untouched in the current window,
// which is the normal state for most of the 39 quotas the API reports.
func TestGetUsageMetrics(t *testing.T) {
	reset := time.Date(2026, 9, 3, 17, 11, 21, 0, time.UTC)

	client := &Client{
		usageMetricsClient: &mockUsageMetricsServiceClient{
			GetUsageMetricsFunc: func(_ context.Context, _ *metrics.GetUsageMetricsRequest, _ ...grpc.CallOption) (*metrics.GetUsageMetricsResponse, error) {
				return &metrics.GetUsageMetricsResponse{
					Quotas: []*metrics.GetUsageMetricsResponse_QuotaUsage{
						{Name: "UsageMetricsService.getUsageMetrics", Limit: 200, Remaining: 199, ResetTime: timestamppb.New(reset)},
						{Name: "AccountsService.getAccount", Limit: 200, Remaining: 200},
						{Name: "ReportsService.createAccountReport", Limit: 3, Remaining: 3},
						nil,
					},
				}, nil
			},
		},
	}

	quotas, err := client.GetUsageMetrics()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(quotas) != 3 {
		t.Fatalf("len(quotas) = %d, want 3 (the nil entry is dropped)", len(quotas))
	}

	if quotas[0].Name != "UsageMetricsService.getUsageMetrics" {
		t.Errorf("quotas[0].Name = %q", quotas[0].Name)
	}
	if quotas[0].Limit != 200 || quotas[0].Remaining != 199 {
		t.Errorf("quotas[0] limit/remaining = %d/%d, want 200/199", quotas[0].Limit, quotas[0].Remaining)
	}
	if !quotas[0].ResetAt.Equal(reset) {
		t.Errorf("quotas[0].ResetAt = %v, want %v", quotas[0].ResetAt, reset)
	}

	// An untouched quota carries no reset time at all. Mapping it to the Unix
	// epoch would render as a countdown of many years; zero means "unknown".
	if !quotas[1].ResetAt.IsZero() {
		t.Errorf("quotas[1].ResetAt = %v, want zero for a nil reset_time", quotas[1].ResetAt)
	}
	if quotas[2].Limit != 3 {
		t.Errorf("quotas[2].Limit = %d, want 3", quotas[2].Limit)
	}
}

// TestGetUsageMetrics_Empty treats an empty quota list as a valid answer, not
// an error: the UI says "квоты не получены" and offers R.
func TestGetUsageMetrics_Empty(t *testing.T) {
	client := &Client{
		usageMetricsClient: &mockUsageMetricsServiceClient{
			GetUsageMetricsFunc: func(_ context.Context, _ *metrics.GetUsageMetricsRequest, _ ...grpc.CallOption) (*metrics.GetUsageMetricsResponse, error) {
				return &metrics.GetUsageMetricsResponse{}, nil
			},
		},
	}

	quotas, err := client.GetUsageMetrics()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(quotas) != 0 {
		t.Errorf("len(quotas) = %d, want 0", len(quotas))
	}
}

// TestGetUsageMetrics_Error propagates a failure so the sub-screen can show it
// with the R hint instead of an empty table.
func TestGetUsageMetrics_Error(t *testing.T) {
	wantErr := status.Error(codes.ResourceExhausted, "quota exceeded")

	client := &Client{
		usageMetricsClient: &mockUsageMetricsServiceClient{
			GetUsageMetricsFunc: func(_ context.Context, _ *metrics.GetUsageMetricsRequest, _ ...grpc.CallOption) (*metrics.GetUsageMetricsResponse, error) {
				return nil, wantErr
			},
		},
	}

	quotas, err := client.GetUsageMetrics()
	if err == nil {
		t.Fatal("expected an error")
	}
	if quotas != nil {
		t.Errorf("quotas = %+v, want nil on error", quotas)
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error should wrap the gRPC status, got %v", err)
	}
	// The UI marks a rate-limited failure differently, so the code must survive.
	if !IsRateLimited(err) {
		t.Error("IsRateLimited should recognise the wrapped ResourceExhausted")
	}
}
