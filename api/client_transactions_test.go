package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/accounts"
	"google.golang.org/genproto/googleapis/type/decimal"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// transactionsClient builds a Client whose Transactions call returns resp and
// records the request it was given.
func transactionsClient(resp *accounts.TransactionsResponse, err error, seen **accounts.TransactionsRequest) *Client {
	return &Client{
		accountsClient: &mockAccountsServiceClient{
			TransactionsFunc: func(_ context.Context, in *accounts.TransactionsRequest, _ ...grpc.CallOption) (*accounts.TransactionsResponse, error) {
				if seen != nil {
					*seen = in
				}
				return resp, err
			},
		},
		assetMicCache:       map[string]string{},
		assetLotCache:       map[string]float64{},
		tradeLotCache:       map[string]float64{},
		instrumentNameCache: map[string]string{},
	}
}

// TestGetTransactions_Mapping checks every field of the mapping at once: the
// category comes from the enum name rather than the free-text field, the money
// keeps its sign, and the trade sub-message is carried through.
func TestGetTransactions_Mapping(t *testing.T) {
	ts := time.Date(2026, 5, 12, 10, 30, 0, 0, time.UTC)
	var seen *accounts.TransactionsRequest

	client := transactionsClient(&accounts.TransactionsResponse{
		Transactions: []*accounts.Transaction{
			{
				Id:                  "tx-1",
				Category:            "free text",
				TransactionCategory: accounts.Transaction_DEPOSIT,
				TransactionName:     "Ввод денежных средств",
				Timestamp:           timestamppb.New(ts),
				Change:              &money.Money{CurrencyCode: "RUB", Units: 100000, Nanos: 500000000},
			},
			{
				Id:                  "tx-2",
				TransactionCategory: accounts.Transaction_COMMISSION,
				TransactionName:     "Комиссия брокера",
				Timestamp:           timestamppb.New(ts.Add(time.Hour)),
				Symbol:              "SBER@MISX",
				// A charge arrives negative in both components; keeping the
				// sign of each is what makes -1.50 come out as -1.50 rather
				// than -0.50.
				Change: &money.Money{CurrencyCode: "RUB", Units: -1, Nanos: -500000000},
			},
			{
				Id:                  "tx-3",
				TransactionCategory: accounts.Transaction_TRANSFER,
				TransactionName:     "Перевод бумаг",
				Timestamp:           timestamppb.New(ts.Add(2 * time.Hour)),
				Symbol:              "GAZP@MISX",
				ChangeQty:           &decimal.Decimal{Value: "-10"},
			},
			{
				Id:                  "tx-4",
				TransactionCategory: accounts.Transaction_OTHERS,
				TransactionName:     "Сделка",
				Timestamp:           timestamppb.New(ts.Add(3 * time.Hour)),
				Symbol:              "LKOH@MISX",
				Change:              &money.Money{CurrencyCode: "RUB", Units: -50000},
				Trade: &accounts.Transaction_Trade{
					Size:            &decimal.Decimal{Value: "10"},
					Price:           &decimal.Decimal{Value: "5000"},
					AccruedInterest: &decimal.Decimal{Value: "12.34"},
				},
			},
		},
	}, nil, &seen)

	from := ts.Add(-24 * time.Hour)
	to := ts.Add(24 * time.Hour)
	txs, err := client.GetTransactions("ACC001", from, to, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txs) != 4 {
		t.Fatalf("got %d transactions, want 4", len(txs))
	}

	// Request carries the account, the interval and the limit verbatim.
	if seen.AccountId != "ACC001" {
		t.Errorf("AccountId = %q, want ACC001", seen.AccountId)
	}
	if seen.Limit != 500 {
		t.Errorf("Limit = %d, want 500", seen.Limit)
	}
	if !seen.Interval.StartTime.AsTime().Equal(from) || !seen.Interval.EndTime.AsTime().Equal(to) {
		t.Errorf("Interval = %v..%v, want %v..%v",
			seen.Interval.StartTime.AsTime(), seen.Interval.EndTime.AsTime(), from, to)
	}

	// Deposit: category from the enum, amount assembled from units+nanos.
	if txs[0].ID != "tx-1" {
		t.Errorf("ID = %q, want tx-1", txs[0].ID)
	}
	if txs[0].Category != "DEPOSIT" {
		t.Errorf("Category = %q, want DEPOSIT (the enum name, not the free-text field)", txs[0].Category)
	}
	if txs[0].Name != "Ввод денежных средств" {
		t.Errorf("Name = %q, want the transaction_name", txs[0].Name)
	}
	if txs[0].Amount != 100000.5 {
		t.Errorf("Amount = %v, want 100000.5", txs[0].Amount)
	}
	if txs[0].Currency != "RUB" {
		t.Errorf("Currency = %q, want RUB", txs[0].Currency)
	}
	if !txs[0].Timestamp.Equal(ts) {
		t.Errorf("Timestamp = %v, want %v", txs[0].Timestamp, ts)
	}
	if txs[0].Trade != nil {
		t.Error("Trade is set on a deposit, want nil")
	}

	// Commission: negative on both components.
	if txs[1].Amount != -1.5 {
		t.Errorf("Amount = %v, want -1.5", txs[1].Amount)
	}
	if txs[1].Symbol != "SBER@MISX" {
		t.Errorf("Symbol = %q, want SBER@MISX", txs[1].Symbol)
	}

	// Transfer: quantity only, no money.
	if txs[2].Category != "TRANSFER" {
		t.Errorf("Category = %q, want TRANSFER", txs[2].Category)
	}
	if txs[2].ChangeQty != -10 {
		t.Errorf("ChangeQty = %v, want -10", txs[2].ChangeQty)
	}
	if txs[2].Amount != 0 || txs[2].Currency != "" {
		t.Errorf("money = %v %q, want zero and empty for a securities transfer", txs[2].Amount, txs[2].Currency)
	}

	// Trade sub-message survives.
	if txs[3].Trade == nil {
		t.Fatal("Trade = nil, want the trade sub-message")
	}
	if txs[3].Trade.Size != 10 || txs[3].Trade.Price != 5000 || txs[3].Trade.AccruedInterest != 12.34 {
		t.Errorf("Trade = %+v, want {Size:10 Price:5000 AccruedInterest:12.34}", *txs[3].Trade)
	}
}

// TestGetTransactions_NilFields makes sure a record with nothing but an id
// maps without panicking. The reconnaissance never saw a live transaction, so
// nothing may be assumed present.
func TestGetTransactions_NilFields(t *testing.T) {
	client := transactionsClient(&accounts.TransactionsResponse{
		Transactions: []*accounts.Transaction{
			{Id: "bare"},
			nil,
		},
	}, nil, nil)

	txs, err := client.GetTransactions("ACC001", time.Now().Add(-time.Hour), time.Now(), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("got %d transactions, want 1 (the nil entry dropped)", len(txs))
	}
	if txs[0].Category != "OTHERS" {
		t.Errorf("Category = %q, want OTHERS for the zero enum value", txs[0].Category)
	}
	if !txs[0].Timestamp.IsZero() {
		t.Errorf("Timestamp = %v, want the zero time for an absent timestamp", txs[0].Timestamp)
	}
	if txs[0].Amount != 0 || txs[0].ChangeQty != 0 || txs[0].Trade != nil {
		t.Errorf("bare transaction = %+v, want zero values throughout", txs[0])
	}
}

// TestGetTransactions_Error propagates the gRPC failure instead of returning a
// partial list.
func TestGetTransactions_Error(t *testing.T) {
	client := transactionsClient(nil, status.Error(codes.ResourceExhausted, "rate limited"), nil)

	txs, err := client.GetTransactions("ACC001", time.Now().Add(-time.Hour), time.Now(), 0)
	if err == nil {
		t.Fatal("expected an error")
	}
	if txs != nil {
		t.Errorf("transactions = %v, want nil on error", txs)
	}
	if status.Code(err) != codes.ResourceExhausted {
		t.Errorf("status code = %v, want ResourceExhausted to survive the wrap", status.Code(err))
	}
	var se interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &se) {
		t.Error("the gRPC status is not reachable through errors.As; the loader needs it to tell a rate limit apart")
	}
}

// TestGetTransactions_Empty treats an empty response as an empty result, not
// an error: an account with no movements in the window is normal.
func TestGetTransactions_Empty(t *testing.T) {
	client := transactionsClient(&accounts.TransactionsResponse{}, nil, nil)

	txs, err := client.GetTransactions("ACC001", time.Now().Add(-time.Hour), time.Now(), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txs) != 0 {
		t.Errorf("got %d transactions, want none", len(txs))
	}
}
