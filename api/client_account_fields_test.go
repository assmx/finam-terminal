package api

import (
	"context"
	"testing"
	"time"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/accounts"
	"google.golang.org/genproto/googleapis/type/decimal"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// accountFieldsClient builds a Client whose GetAccount returns resp.
func accountFieldsClient(resp *accounts.GetAccountResponse) *Client {
	return &Client{
		accountsClient: &mockAccountsServiceClient{
			GetAccountFunc: func(_ context.Context, in *accounts.GetAccountRequest, _ ...grpc.CallOption) (*accounts.GetAccountResponse, error) {
				resp.AccountId = in.AccountId
				return resp, nil
			},
		},
		assetMicCache:       map[string]string{},
		assetLotCache:       map[string]float64{},
		tradeLotCache:       map[string]float64{},
		instrumentNameCache: map[string]string{},
	}
}

// TestGetAccountDetails_PortfolioMC checks the MC branch of the portfolio
// oneof: the kind, the three margin numbers and the HasMarginData flag.
func TestGetAccountDetails_PortfolioMC(t *testing.T) {
	client := accountFieldsClient(&accounts.GetAccountResponse{
		Equity: &decimal.Decimal{Value: "500000"},
		Portfolio: &accounts.GetAccountResponse_PortfolioMc{
			PortfolioMc: &accounts.MC{
				AvailableCash:     &decimal.Decimal{Value: "120000.50"},
				InitialMargin:     &decimal.Decimal{Value: "80000"},
				MaintenanceMargin: &decimal.Decimal{Value: "40000"},
			},
		},
	})

	account, _, err := client.GetAccountDetails("ACC001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if account.PortfolioKind != "MC" {
		t.Errorf("PortfolioKind = %q, want MC", account.PortfolioKind)
	}
	if !account.HasMarginData {
		t.Error("HasMarginData = false, want true")
	}
	if account.AvailableCash != 120000.50 {
		t.Errorf("AvailableCash = %v, want 120000.50", account.AvailableCash)
	}
	if account.InitialMargin != 80000 {
		t.Errorf("InitialMargin = %v, want 80000", account.InitialMargin)
	}
	if account.MaintenanceMargin != 40000 {
		t.Errorf("MaintenanceMargin = %v, want 40000", account.MaintenanceMargin)
	}
	if account.MoneyReserved != 0 {
		t.Errorf("MoneyReserved = %v, want 0 for an MC portfolio", account.MoneyReserved)
	}
}

// TestGetAccountDetails_PortfolioFORTS checks the FORTS branch: money_reserved
// is populated and the MC-only margin fields stay zero.
func TestGetAccountDetails_PortfolioFORTS(t *testing.T) {
	client := accountFieldsClient(&accounts.GetAccountResponse{
		Portfolio: &accounts.GetAccountResponse_PortfolioForts{
			PortfolioForts: &accounts.FORTS{
				AvailableCash: &decimal.Decimal{Value: "75000"},
				MoneyReserved: &decimal.Decimal{Value: "25000.25"},
			},
		},
	})

	account, _, err := client.GetAccountDetails("ACC002")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if account.PortfolioKind != "FORTS" {
		t.Errorf("PortfolioKind = %q, want FORTS", account.PortfolioKind)
	}
	if !account.HasMarginData {
		t.Error("HasMarginData = false, want true")
	}
	if account.AvailableCash != 75000 {
		t.Errorf("AvailableCash = %v, want 75000", account.AvailableCash)
	}
	if account.MoneyReserved != 25000.25 {
		t.Errorf("MoneyReserved = %v, want 25000.25", account.MoneyReserved)
	}
	if account.InitialMargin != 0 || account.MaintenanceMargin != 0 {
		t.Errorf("MC margins should stay zero for FORTS, got %v/%v",
			account.InitialMargin, account.MaintenanceMargin)
	}
}

// TestGetAccountDetails_PortfolioMCT covers the MCT branch. MCT is an empty
// message in the proto, so the kind is known but no numbers exist: the UI must
// be told there is no margin data rather than shown zeros as if they were real.
func TestGetAccountDetails_PortfolioMCT(t *testing.T) {
	client := accountFieldsClient(&accounts.GetAccountResponse{
		Portfolio: &accounts.GetAccountResponse_PortfolioMct{PortfolioMct: &accounts.MCT{}},
	})

	account, _, err := client.GetAccountDetails("ACC003")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if account.PortfolioKind != "MCT" {
		t.Errorf("PortfolioKind = %q, want MCT", account.PortfolioKind)
	}
	if account.HasMarginData {
		t.Error("HasMarginData = true, want false: MCT carries no margin fields")
	}
}

// TestGetAccountDetails_PortfolioEmpty covers a response with no portfolio
// oneof at all, which the reconnaissance could not rule out.
func TestGetAccountDetails_PortfolioEmpty(t *testing.T) {
	client := accountFieldsClient(&accounts.GetAccountResponse{
		Equity: &decimal.Decimal{Value: "1000"},
	})

	account, _, err := client.GetAccountDetails("ACC004")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if account.PortfolioKind != "" {
		t.Errorf("PortfolioKind = %q, want empty", account.PortfolioKind)
	}
	if account.HasMarginData {
		t.Error("HasMarginData = true, want false for an empty oneof")
	}
	if account.AvailableCash != 0 || account.InitialMargin != 0 ||
		account.MaintenanceMargin != 0 || account.MoneyReserved != 0 {
		t.Error("margin numbers should all be zero for an empty oneof")
	}
}

// TestGetAccountDetails_NilMarginFields proves every *decimal.Decimal inside
// the oneof is read nil-safe: the broker may send the branch with fields absent.
func TestGetAccountDetails_NilMarginFields(t *testing.T) {
	client := accountFieldsClient(&accounts.GetAccountResponse{
		Portfolio: &accounts.GetAccountResponse_PortfolioMc{PortfolioMc: &accounts.MC{}},
	})

	account, _, err := client.GetAccountDetails("ACC005")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if account.PortfolioKind != "MC" {
		t.Errorf("PortfolioKind = %q, want MC", account.PortfolioKind)
	}
	if !account.HasMarginData {
		t.Error("HasMarginData = false: the MC branch was present, only its fields were not")
	}
	if account.AvailableCash != 0 || account.InitialMargin != 0 || account.MaintenanceMargin != 0 {
		t.Error("nil decimals must map to 0")
	}
}

// TestGetAccountDetails_Cash maps the repeated google.type.Money list,
// including the negative (borrowed) case where units and nanos share a sign.
func TestGetAccountDetails_Cash(t *testing.T) {
	client := accountFieldsClient(&accounts.GetAccountResponse{
		Cash: []*money.Money{
			{CurrencyCode: "RUB", Units: 125000, Nanos: 500000000},
			{CurrencyCode: "USD", Units: -300, Nanos: -250000000},
			{CurrencyCode: "CNY", Units: 0, Nanos: 0},
			nil,
		},
	})

	account, _, err := client.GetAccountDetails("ACC006")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(account.Cash) != 3 {
		t.Fatalf("len(Cash) = %d, want 3 (the nil entry is dropped)", len(account.Cash))
	}

	want := []struct {
		currency string
		amount   float64
	}{
		{"RUB", 125000.5},
		{"USD", -300.25},
		{"CNY", 0},
	}
	for i, w := range want {
		if account.Cash[i].Currency != w.currency {
			t.Errorf("Cash[%d].Currency = %q, want %q", i, account.Cash[i].Currency, w.currency)
		}
		if account.Cash[i].Amount != w.amount {
			t.Errorf("Cash[%d].Amount = %v, want %v", i, account.Cash[i].Amount, w.amount)
		}
	}
}

// TestGetAccountDetails_Dates maps first_trade_date / first_non_trade_date and
// leaves them zero when the broker omits them.
func TestGetAccountDetails_Dates(t *testing.T) {
	first := time.Date(2019, 4, 15, 10, 30, 0, 0, time.UTC)
	nonTrade := time.Date(2019, 4, 10, 8, 0, 0, 0, time.UTC)

	client := accountFieldsClient(&accounts.GetAccountResponse{
		FirstTradeDate:    timestamppb.New(first),
		FirstNonTradeDate: timestamppb.New(nonTrade),
	})

	account, _, err := client.GetAccountDetails("ACC007")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !account.FirstTradeDate.Equal(first) {
		t.Errorf("FirstTradeDate = %v, want %v", account.FirstTradeDate, first)
	}
	if !account.FirstNonTradeDate.Equal(nonTrade) {
		t.Errorf("FirstNonTradeDate = %v, want %v", account.FirstNonTradeDate, nonTrade)
	}

	bare := accountFieldsClient(&accounts.GetAccountResponse{})
	account, _, err = bare.GetAccountDetails("ACC008")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !account.FirstTradeDate.IsZero() {
		t.Errorf("FirstTradeDate = %v, want zero time when absent", account.FirstTradeDate)
	}
	if !account.FirstNonTradeDate.IsZero() {
		t.Errorf("FirstNonTradeDate = %v, want zero time when absent", account.FirstNonTradeDate)
	}
}
