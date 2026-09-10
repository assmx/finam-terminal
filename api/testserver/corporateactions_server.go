package testserver

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/corporateactions"
	"google.golang.org/genproto/googleapis/type/date"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

	// LastPastBondEventsRequest is the last request that method received. The
	// interval it carries is the whole point of the validation below, so a test
	// can assert on what was actually sent rather than only on the outcome.
	LastPastBondEventsRequest atomic.Pointer[corporateactions.GetPastBondsEventsRequest]
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

// GetPastBondsEvents mirrors the real endpoint's validation of date_to.
//
// Measured against the live API on 2026-09-09: this method refuses a date_to
// of today with `InvalidArgument: Invalid arguments:date_to`, whatever the
// width of the window (30 days, six months and a year were all refused), while
// the same window ending yesterday is accepted, and so is a request carrying
// no interval at all — the proto documents a one-year default for that case.
// The sibling dividend and split endpoints accept a date_to of today, so this
// belongs here and nowhere else.
//
// Without this check the mock happily answered a request the broker rejects,
// and the bug went unnoticed until a bond in a real portfolio silently
// contributed nothing to the payout forecast.
func (m *MockCorporateActionsServer) GetPastBondsEvents(_ context.Context, req *corporateactions.GetPastBondsEventsRequest) (*corporateactions.GetPastBondsEventsResponse, error) {
	m.PastBondEventsCallCount.Add(1)
	m.LastPastBondEventsRequest.Store(req)
	if m.BondEventsError != nil {
		return nil, m.BondEventsError
	}
	if err := rejectDateToToday(req.GetDateTo()); err != nil {
		return nil, err
	}
	return &corporateactions.GetPastBondsEventsResponse{Events: m.PastBondEvents}, nil
}

// rejectDateToToday answers the error the broker answers for a date_to that is
// not strictly in the past. A nil date is the accepted "no interval" form.
func rejectDateToToday(to *date.Date) error {
	if to == nil {
		return nil
	}

	day := time.Date(int(to.GetYear()), time.Month(to.GetMonth()), int(to.GetDay()), 0, 0, 0, 0, time.Local)
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)

	if !day.Before(today) {
		return status.Error(codes.InvalidArgument, "Invalid arguments:date_to")
	}
	return nil
}

func (m *MockCorporateActionsServer) GetFutureBondsEvents(_ context.Context, req *corporateactions.GetFutureBondsEventsRequest) (*corporateactions.GetFutureBondsEventsResponse, error) {
	m.FutureBondEventsCallCount.Add(1)
	if m.BondEventsError != nil {
		return nil, m.BondEventsError
	}
	return &corporateactions.GetFutureBondsEventsResponse{Events: m.FutureBondEvents}, nil
}
