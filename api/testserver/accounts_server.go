package testserver

import (
	"context"
	"sort"

	tradeapiv1 "github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1"
	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/accounts"
	"google.golang.org/genproto/googleapis/type/decimal"
	"google.golang.org/genproto/googleapis/type/interval"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// AccountPortfolio carries the optional GetAccountResponse fields that vary by
// account: the cash list, the portfolio oneof and the first-transaction dates.
// The oneof branches are held as concrete messages because the interface the
// generated code uses for them is unexported; GetAccount wraps whichever is set.
type AccountPortfolio struct {
	Cash              []*money.Money
	MC                *accounts.MC
	FORTS             *accounts.FORTS
	MCT               *accounts.MCT
	FirstTradeDate    *timestamppb.Timestamp
	FirstNonTradeDate *timestamppb.Timestamp
}

// MockAccountsServer implements accounts.AccountsServiceServer for testing.
type MockAccountsServer struct {
	accounts.UnimplementedAccountsServiceServer

	// Positions keyed by account ID.
	Positions map[string][]*accounts.Position

	// TradeHistory keyed by account ID.
	TradeHistory map[string][]*tradeapiv1.AccountTrade

	// Portfolios keyed by account ID. An account with no entry answers with no
	// cash and an empty portfolio oneof, which is itself a case worth serving.
	Portfolios map[string]AccountPortfolio

	// TransactionHistory keyed by account ID. Named for symmetry with
	// TradeHistory, and because a field called Transactions would collide with
	// the service method of the same name.
	TransactionHistory map[string][]*accounts.Transaction

	// GetAccountError, if set, is returned by GetAccount.
	GetAccountError error

	// TradesError / TransactionsError, if set, are returned by their method.
	TradesError       error
	TransactionsError error

	// TradesLimitCap, when non-zero, is the largest number of records Trades
	// will return whatever the request asks for. It stands in for a
	// server-side cap the reconnaissance could not measure, so the loader's
	// chunk splitting can be exercised against one.
	TradesLimitCap int32

	// TransactionsLimitCap is the same cap for Transactions.
	TransactionsLimitCap int32

	// Call counters. They are written from the gRPC handler goroutine and read
	// by the test, which is why the suite runs under -race.
	TradesCallCount       int
	TransactionsCallCount int

	// Last request seen, for asserting what the loader actually asked for.
	LastTradesRequest       *accounts.TradesRequest
	LastTransactionsRequest *accounts.TransactionsRequest
}

// NewMockAccountsServer creates a MockAccountsServer with default data.
func NewMockAccountsServer() *MockAccountsServer {
	return &MockAccountsServer{
		Positions: map[string][]*accounts.Position{
			"ACC001": DefaultAccountPositions("ACC001"),
		},
		TradeHistory: map[string][]*tradeapiv1.AccountTrade{
			"ACC001": DefaultTrades("ACC001"),
		},
		Portfolios: map[string]AccountPortfolio{
			"ACC001": DefaultMCPortfolio(),
			"ACC002": DefaultFORTSPortfolio(),
		},
		TransactionHistory: map[string][]*accounts.Transaction{
			"ACC001": DefaultTransactions(),
		},
	}
}

// GetAccount returns account details with positions.
func (m *MockAccountsServer) GetAccount(_ context.Context, req *accounts.GetAccountRequest) (*accounts.GetAccountResponse, error) {
	if m.GetAccountError != nil {
		return nil, m.GetAccountError
	}

	resp := &accounts.GetAccountResponse{
		AccountId: req.AccountId,
		Equity:    &decimal.Decimal{Value: "500000.00"},
		Positions: m.Positions[req.AccountId],
	}

	p, ok := m.Portfolios[req.AccountId]
	if !ok {
		return resp, nil
	}

	resp.Cash = p.Cash
	resp.FirstTradeDate = p.FirstTradeDate
	resp.FirstNonTradeDate = p.FirstNonTradeDate
	switch {
	case p.MC != nil:
		resp.Portfolio = &accounts.GetAccountResponse_PortfolioMc{PortfolioMc: p.MC}
	case p.FORTS != nil:
		resp.Portfolio = &accounts.GetAccountResponse_PortfolioForts{PortfolioForts: p.FORTS}
	case p.MCT != nil:
		resp.Portfolio = &accounts.GetAccountResponse_PortfolioMct{PortfolioMct: p.MCT}
	}

	return resp, nil
}

// Trades returns trade history within the requested interval.
//
// The interval and the limit are honoured because the history loader is built
// entirely around them: it walks the account backwards in chunks and treats a
// response of exactly limit records as truncated. A mock that ignored either
// would let that whole mechanism pass untested.
func (m *MockAccountsServer) Trades(_ context.Context, req *accounts.TradesRequest) (*accounts.TradesResponse, error) {
	m.TradesCallCount++
	m.LastTradesRequest = req
	if m.TradesError != nil {
		return nil, m.TradesError
	}

	trades := m.TradeHistory[req.AccountId]
	kept := make([]*tradeapiv1.AccountTrade, 0, len(trades))
	for _, t := range trades {
		if t != nil && withinInterval(t.GetTimestamp(), req.GetInterval()) {
			kept = append(kept, t)
		}
	}

	kept = truncateNewest(kept, effectiveLimit(req.GetLimit(), m.TradesLimitCap),
		func(t *tradeapiv1.AccountTrade) *timestamppb.Timestamp { return t.GetTimestamp() })
	return &accounts.TradesResponse{Trades: kept}, nil
}

// Transactions returns money and securities movements within the requested
// interval, with the same interval/limit handling as Trades.
func (m *MockAccountsServer) Transactions(_ context.Context, req *accounts.TransactionsRequest) (*accounts.TransactionsResponse, error) {
	m.TransactionsCallCount++
	m.LastTransactionsRequest = req
	if m.TransactionsError != nil {
		return nil, m.TransactionsError
	}

	txs := m.TransactionHistory[req.AccountId]
	kept := make([]*accounts.Transaction, 0, len(txs))
	for _, t := range txs {
		if t != nil && withinInterval(t.GetTimestamp(), req.GetInterval()) {
			kept = append(kept, t)
		}
	}

	kept = truncateNewest(kept, effectiveLimit(req.GetLimit(), m.TransactionsLimitCap),
		func(t *accounts.Transaction) *timestamppb.Timestamp { return t.GetTimestamp() })
	return &accounts.TransactionsResponse{Transactions: kept}, nil
}

// withinInterval reports whether ts falls inside iv. A nil interval accepts
// everything; a record with no timestamp is never inside a bounded interval.
func withinInterval(ts *timestamppb.Timestamp, iv *interval.Interval) bool {
	if iv == nil || (iv.GetStartTime() == nil && iv.GetEndTime() == nil) {
		return true
	}
	if ts == nil {
		return false
	}
	t := ts.AsTime()
	if s := iv.GetStartTime(); s != nil && t.Before(s.AsTime()) {
		return false
	}
	if e := iv.GetEndTime(); e != nil && t.After(e.AsTime()) {
		return false
	}
	return true
}

// effectiveLimit combines the requested limit with the server-side cap: the
// smaller of the two wins, and zero on both sides means no limit.
func effectiveLimit(requested, cap int32) int {
	switch {
	case requested <= 0:
		return int(cap)
	case cap > 0 && cap < requested:
		return int(cap)
	default:
		return int(requested)
	}
}

// truncateNewest keeps at most n records, dropping the oldest, and hands them
// back in the order they arrived.
//
// Truncation drops the oldest because that is what makes a partial answer
// meaningful to a loader walking an account backwards: the newest end of the
// window is complete and the boundary moves. The original order is restored
// afterwards because the real API's ordering was never observed (the
// reconnaissance token had no trading account), so no caller may depend on it —
// keeping the fixture order is the arrangement that would catch a caller who
// quietly did.
func truncateNewest[T any](items []T, n int, at func(T) *timestamppb.Timestamp) []T {
	if n <= 0 || len(items) <= n {
		return items
	}

	type indexed struct {
		item  T
		order int
	}
	ranked := make([]indexed, len(items))
	for i, it := range items {
		ranked[i] = indexed{item: it, order: i}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		ti, tj := at(ranked[i].item), at(ranked[j].item)
		switch {
		case ti == nil:
			return false
		case tj == nil:
			return true
		default:
			return ti.AsTime().After(tj.AsTime())
		}
	})
	ranked = ranked[:n]

	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].order < ranked[j].order })
	out := make([]T, n)
	for i, r := range ranked {
		out[i] = r.item
	}
	return out
}
