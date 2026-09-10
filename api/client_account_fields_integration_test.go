//go:build integration

package api

import (
	"testing"
	"time"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/accounts"
	"google.golang.org/genproto/googleapis/type/decimal"
)

// TestIntegration_GetAccountDetails_MarginFields walks the MC account end to
// end through bufconn: the cash list, the portfolio oneof and the dates must
// survive the real generated codec, not just the hand-rolled unit mock.
func TestIntegration_GetAccountDetails_MarginFields(t *testing.T) {
	client, _ := setupTestServer(t)

	account, _, err := client.GetAccountDetails("ACC001")
	if err != nil {
		t.Fatalf("GetAccountDetails failed: %v", err)
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

	if len(account.Cash) != 2 {
		t.Fatalf("len(Cash) = %d, want 2", len(account.Cash))
	}
	if account.Cash[0].Currency != "RUB" || account.Cash[0].Amount != 125000.5 {
		t.Errorf("Cash[0] = %+v, want {RUB 125000.5}", account.Cash[0])
	}
	// A borrowed balance keeps its sign all the way through: units and nanos
	// are both negative in google.type.Money and must not cancel out.
	if account.Cash[1].Currency != "USD" || account.Cash[1].Amount != -300.25 {
		t.Errorf("Cash[1] = %+v, want {USD -300.25}", account.Cash[1])
	}

	wantFirst := time.Date(2019, 4, 15, 10, 30, 0, 0, time.UTC)
	if !account.FirstTradeDate.Equal(wantFirst) {
		t.Errorf("FirstTradeDate = %v, want %v", account.FirstTradeDate, wantFirst)
	}
	wantNonTrade := time.Date(2019, 4, 10, 8, 0, 0, 0, time.UTC)
	if !account.FirstNonTradeDate.Equal(wantNonTrade) {
		t.Errorf("FirstNonTradeDate = %v, want %v", account.FirstNonTradeDate, wantNonTrade)
	}
}

// TestIntegration_GetAccountDetails_FORTS covers the second account, where the
// broker sends money_reserved and no MC margins at all.
func TestIntegration_GetAccountDetails_FORTS(t *testing.T) {
	client, _ := setupTestServer(t)

	account, _, err := client.GetAccountDetails("ACC002")
	if err != nil {
		t.Fatalf("GetAccountDetails failed: %v", err)
	}

	if account.PortfolioKind != "FORTS" {
		t.Errorf("PortfolioKind = %q, want FORTS", account.PortfolioKind)
	}
	if !account.HasMarginData {
		t.Error("HasMarginData = false, want true")
	}
	if account.MoneyReserved != 25000.25 {
		t.Errorf("MoneyReserved = %v, want 25000.25", account.MoneyReserved)
	}
	if account.InitialMargin != 0 || account.MaintenanceMargin != 0 {
		t.Errorf("MC margins should be zero on a FORTS account, got %v/%v",
			account.InitialMargin, account.MaintenanceMargin)
	}
	if !account.FirstTradeDate.IsZero() {
		t.Errorf("FirstTradeDate = %v, want zero: this fixture sends no date", account.FirstTradeDate)
	}
}

// TestIntegration_GetAccountDetails_PositionMaintenanceMargin carries the
// per-position collateral through the real codec: filled on a FORTS position,
// absent — and so "not reported" — on a stock one.
func TestIntegration_GetAccountDetails_PositionMaintenanceMargin(t *testing.T) {
	client, ts := setupTestServer(t)

	ts.Accounts.Positions["ACC001"] = []*accounts.Position{
		{
			Symbol:            "SiZ6@RTSX",
			Quantity:          &decimal.Decimal{Value: "2"},
			CurrentPrice:      &decimal.Decimal{Value: "90000"},
			MaintenanceMargin: &decimal.Decimal{Value: "15000.5"},
		},
		{
			Symbol:       "SBER@TQBR",
			Quantity:     &decimal.Decimal{Value: "100"},
			CurrentPrice: &decimal.Decimal{Value: "285.00"},
		},
	}

	_, positions, err := client.GetAccountDetails("ACC001")
	if err != nil {
		t.Fatalf("GetAccountDetails failed: %v", err)
	}

	got := map[string]string{}
	for _, p := range positions {
		got[p.Symbol] = p.MaintenanceMargin
	}
	if got["SiZ6@RTSX"] != "15000.5" {
		t.Errorf("FORTS position MaintenanceMargin = %q, want 15000.5", got["SiZ6@RTSX"])
	}
	if got["SBER@TQBR"] != "N/A" {
		t.Errorf("stock position MaintenanceMargin = %q, want N/A", got["SBER@TQBR"])
	}
}

// TestIntegration_GetAccountDetails_NoPortfolio covers an account the mock has
// no fixture for, i.e. a response with an empty portfolio oneof and no cash.
func TestIntegration_GetAccountDetails_NoPortfolio(t *testing.T) {
	client, _ := setupTestServer(t)

	account, _, err := client.GetAccountDetails("ACC-UNKNOWN")
	if err != nil {
		t.Fatalf("GetAccountDetails failed: %v", err)
	}

	if account.PortfolioKind != "" {
		t.Errorf("PortfolioKind = %q, want empty", account.PortfolioKind)
	}
	if account.HasMarginData {
		t.Error("HasMarginData = true, want false for an absent oneof")
	}
	if account.Cash != nil {
		t.Errorf("Cash = %+v, want nil", account.Cash)
	}
}
