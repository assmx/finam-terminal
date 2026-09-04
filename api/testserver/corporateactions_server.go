package testserver

import (
	"context"
	"sync/atomic"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/corporateactions"
)

// MockCorporateActionsServer implements corporateactions.CorporateActionsServiceServer
// for testing. It serves separate past/future fixtures per calendar kind so the
// client's past+future merging and IsFuture flagging can be asserted.
type MockCorporateActionsServer struct {
	corporateactions.UnimplementedCorporateActionsServiceServer

	PastDividends    []*corporateactions.Dividend
	FutureDividends  []*corporateactions.Dividend
	PastSplits       []*corporateactions.SplitInfo
	FutureSplits     []*corporateactions.SplitInfo
	PastBondEvents   []*corporateactions.BondEvent
	FutureBondEvents []*corporateactions.BondEvent

	// Optional per-kind error injection (applied to both past and future).
	DividendsError  error
	SplitsError     error
	BondEventsError error

	// Call counters, one per RPC. The calendar cache promises that a second
	// request for the same symbol inside a day costs nothing, and only a
	// counter can show that.
	PastDividendsCallCount    atomic.Int64
	FutureDividendsCallCount  atomic.Int64
	PastSplitsCallCount       atomic.Int64
	FutureSplitsCallCount     atomic.Int64
	PastBondEventsCallCount   atomic.Int64
	FutureBondEventsCallCount atomic.Int64
}

// DividendCalls is how many dividend RPCs the server has answered, past and
// future together.
func (m *MockCorporateActionsServer) DividendCalls() int64 {
	return m.PastDividendsCallCount.Load() + m.FutureDividendsCallCount.Load()
}

// SplitCalls is how many split RPCs the server has answered.
func (m *MockCorporateActionsServer) SplitCalls() int64 {
	return m.PastSplitsCallCount.Load() + m.FutureSplitsCallCount.Load()
}

// BondEventCalls is how many bond-event RPCs the server has answered.
func (m *MockCorporateActionsServer) BondEventCalls() int64 {
	return m.PastBondEventsCallCount.Load() + m.FutureBondEventsCallCount.Load()
}

// NewMockCorporateActionsServer creates a server populated with default fixtures.
func NewMockCorporateActionsServer() *MockCorporateActionsServer {
	pastDiv, futureDiv := DefaultDividends()
	pastSplit, futureSplit := DefaultSplits()
	pastBond, futureBond := DefaultBondEvents()
	return &MockCorporateActionsServer{
		PastDividends:    pastDiv,
		FutureDividends:  futureDiv,
		PastSplits:       pastSplit,
		FutureSplits:     futureSplit,
		PastBondEvents:   pastBond,
		FutureBondEvents: futureBond,
	}
}

func (m *MockCorporateActionsServer) GetPastDividends(_ context.Context, req *corporateactions.GetPastDividendsRequest) (*corporateactions.GetPastDividendsResponse, error) {
	m.PastDividendsCallCount.Add(1)
	if m.DividendsError != nil {
		return nil, m.DividendsError
	}
	return &corporateactions.GetPastDividendsResponse{Dividends: m.PastDividends}, nil
}

func (m *MockCorporateActionsServer) GetFutureDividends(_ context.Context, req *corporateactions.GetFutureDividendsRequest) (*corporateactions.GetFutureDividendsResponse, error) {
	m.FutureDividendsCallCount.Add(1)
	if m.DividendsError != nil {
		return nil, m.DividendsError
	}
	return &corporateactions.GetFutureDividendsResponse{Dividends: m.FutureDividends}, nil
}

func (m *MockCorporateActionsServer) GetPastSplits(_ context.Context, req *corporateactions.GetPastSplitsRequest) (*corporateactions.GetPastSplitsResponse, error) {
	m.PastSplitsCallCount.Add(1)
	if m.SplitsError != nil {
		return nil, m.SplitsError
	}
	return &corporateactions.GetPastSplitsResponse{Splits: m.PastSplits}, nil
}

func (m *MockCorporateActionsServer) GetFutureSplits(_ context.Context, req *corporateactions.GetFutureSplitsRequest) (*corporateactions.GetFutureSplitsResponse, error) {
	m.FutureSplitsCallCount.Add(1)
	if m.SplitsError != nil {
		return nil, m.SplitsError
	}
	return &corporateactions.GetFutureSplitsResponse{Splits: m.FutureSplits}, nil
}

func (m *MockCorporateActionsServer) GetPastBondsEvents(_ context.Context, req *corporateactions.GetPastBondsEventsRequest) (*corporateactions.GetPastBondsEventsResponse, error) {
	m.PastBondEventsCallCount.Add(1)
	if m.BondEventsError != nil {
		return nil, m.BondEventsError
	}
	return &corporateactions.GetPastBondsEventsResponse{Events: m.PastBondEvents}, nil
}

func (m *MockCorporateActionsServer) GetFutureBondsEvents(_ context.Context, req *corporateactions.GetFutureBondsEventsRequest) (*corporateactions.GetFutureBondsEventsResponse, error) {
	m.FutureBondEventsCallCount.Add(1)
	if m.BondEventsError != nil {
		return nil, m.BondEventsError
	}
	return &corporateactions.GetFutureBondsEventsResponse{Events: m.FutureBondEvents}, nil
}
