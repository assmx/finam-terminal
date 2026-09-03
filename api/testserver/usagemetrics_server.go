package testserver

import (
	"context"
	"sync/atomic"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/metrics"
)

// MockUsageMetricsServer implements metrics.UsageMetricsServiceServer for testing.
type MockUsageMetricsServer struct {
	metrics.UnimplementedUsageMetricsServiceServer

	// Quotas is the list GetUsageMetrics answers with.
	Quotas []*metrics.GetUsageMetricsResponse_QuotaUsage

	// GetUsageMetricsError, if set, is returned instead of the list.
	GetUsageMetricsError error

	// GetUsageMetricsCallCount counts calls. The Analytics tab promises one
	// request on entry and one per R, so tests assert on this.
	GetUsageMetricsCallCount atomic.Int64
}

// NewMockUsageMetricsServer creates a MockUsageMetricsServer with default quotas.
func NewMockUsageMetricsServer() *MockUsageMetricsServer {
	return &MockUsageMetricsServer{Quotas: DefaultQuotas()}
}

// GetUsageMetrics returns the configured quota list.
func (m *MockUsageMetricsServer) GetUsageMetrics(_ context.Context, _ *metrics.GetUsageMetricsRequest) (*metrics.GetUsageMetricsResponse, error) {
	m.GetUsageMetricsCallCount.Add(1)

	if m.GetUsageMetricsError != nil {
		return nil, m.GetUsageMetricsError
	}
	return &metrics.GetUsageMetricsResponse{Quotas: m.Quotas}, nil
}
