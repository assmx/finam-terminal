package testserver

import (
	"context"

	tradeapiv1 "github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1"
	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/accounts"
	"google.golang.org/genproto/googleapis/type/decimal"
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

	// GetAccountError, if set, is returned by GetAccount.
	GetAccountError error
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

// Trades returns trade history.
func (m *MockAccountsServer) Trades(_ context.Context, req *accounts.TradesRequest) (*accounts.TradesResponse, error) {
	trades := m.TradeHistory[req.AccountId]
	return &accounts.TradesResponse{
		Trades: trades,
	}, nil
}
