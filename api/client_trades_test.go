package api

import (
	"context"
	"testing"
	"time"

	tradeapiv1 "github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1"
	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/accounts"
	"google.golang.org/genproto/googleapis/type/decimal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// tradesClient builds a Client whose Trades call returns resp and records the
// request it was given.
func tradesClient(resp *accounts.TradesResponse, err error, seen **accounts.TradesRequest) *Client {
	return &Client{
		accountsClient: &mockAccountsServiceClient{
			TradesFunc: func(_ context.Context, in *accounts.TradesRequest, _ ...grpc.CallOption) (*accounts.TradesResponse, error) {
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

func sampleTrades(ts time.Time) *accounts.TradesResponse {
	return &accounts.TradesResponse{
		Trades: []*tradeapiv1.AccountTrade{
			{
				TradeId:   "T1",
				Symbol:    "SBER@MISX",
				Side:      tradeapiv1.Side_SIDE_BUY,
				Size:      &decimal.Decimal{Value: "10"},
				Price:     &decimal.Decimal{Value: "280.50"},
				Timestamp: timestamppb.New(ts),
			},
			{
				TradeId:         "T2",
				Symbol:          "SU26238@TQOB",
				Side:            tradeapiv1.Side_SIDE_SELL,
				Size:            &decimal.Decimal{Value: "3"},
				Price:           &decimal.Decimal{Value: "650.10"},
				AccruedInterest: &decimal.Decimal{Value: "12.34"},
				Currency:        "RUB",
				Timestamp:       timestamppb.New(ts.Add(time.Hour)),
			},
		},
	}
}

// TestGetTrades_RequestShape checks that the window and the limit reach the API
// verbatim — the history loader steers both and nothing may quietly rewrite
// them.
func TestGetTrades_RequestShape(t *testing.T) {
	ts := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	var seen *accounts.TradesRequest
	client := tradesClient(sampleTrades(ts), nil, &seen)

	from := ts.Add(-92 * 24 * time.Hour)
	to := ts.Add(24 * time.Hour)
	trades, err := client.GetTrades("ACC001", from, to, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(trades) != 2 {
		t.Fatalf("got %d trades, want 2", len(trades))
	}

	if seen.AccountId != "ACC001" {
		t.Errorf("AccountId = %q, want ACC001", seen.AccountId)
	}
	if seen.Limit != 1000 {
		t.Errorf("Limit = %d, want 1000", seen.Limit)
	}
	if !seen.Interval.StartTime.AsTime().Equal(from) || !seen.Interval.EndTime.AsTime().Equal(to) {
		t.Errorf("Interval = %v..%v, want %v..%v",
			seen.Interval.StartTime.AsTime(), seen.Interval.EndTime.AsTime(), from, to)
	}
}

// TestGetTrades_Mapping pins the fields the FIFO matcher reads.
func TestGetTrades_Mapping(t *testing.T) {
	ts := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	client := tradesClient(sampleTrades(ts), nil, nil)

	trades, err := client.GetTrades("ACC001", ts.Add(-time.Hour), ts.Add(2*time.Hour), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if trades[0].ID != "T1" || trades[0].Side != "Buy" || trades[0].Price != "280.50" || trades[0].Quantity != "10" {
		t.Errorf("equity trade = %+v, want the buy of 10 at 280.50", trades[0])
	}
	if trades[0].AccruedInterest != "" {
		t.Errorf("AccruedInterest = %q, want empty for an equity", trades[0].AccruedInterest)
	}
	if !trades[0].Timestamp.Equal(ts) {
		t.Errorf("Timestamp = %v, want %v", trades[0].Timestamp, ts)
	}
	if trades[1].Side != "Sell" || trades[1].AccruedInterest != "12.34" || trades[1].Currency != "RUB" {
		t.Errorf("bond trade = %+v, want the sell with accrued interest 12.34 in RUB", trades[1])
	}
}

// TestGetTradeHistory_IsGetTradesWindow proves the History tab's method is now
// a thin wrapper: the same records come back, and the request it makes is a
// 30-day window with no limit.
func TestGetTradeHistory_IsGetTradesWindow(t *testing.T) {
	ts := time.Now().Add(-24 * time.Hour)
	var seen *accounts.TradesRequest
	client := tradesClient(sampleTrades(ts), nil, &seen)

	before := time.Now()
	trades, err := client.GetTradeHistory("ACC001")
	after := time.Now()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(trades) != 2 {
		t.Fatalf("got %d trades, want 2", len(trades))
	}
	if trades[0].ID != "T1" || trades[1].ID != "T2" {
		t.Errorf("trade ids = %s,%s; the wrapper must not reorder", trades[0].ID, trades[1].ID)
	}

	if seen.Limit != 0 {
		t.Errorf("Limit = %d, want 0 (the API default) for the History tab", seen.Limit)
	}
	end := seen.Interval.EndTime.AsTime()
	if end.Before(before.Add(-time.Second)) || end.After(after.Add(time.Second)) {
		t.Errorf("EndTime = %v, want roughly now", end)
	}
	span := end.Sub(seen.Interval.StartTime.AsTime())
	if span < 29*24*time.Hour || span > 31*24*time.Hour {
		t.Errorf("window = %v, want about 30 days", span)
	}
}

// TestGetTrades_Error keeps the gRPC code reachable so the loader can tell a
// rate limit from an ordinary failure.
func TestGetTrades_Error(t *testing.T) {
	client := tradesClient(nil, status.Error(codes.ResourceExhausted, "rate limited"), nil)

	trades, err := client.GetTrades("ACC001", time.Now().Add(-time.Hour), time.Now(), 0)
	if err == nil {
		t.Fatal("expected an error")
	}
	if trades != nil {
		t.Errorf("trades = %v, want nil on error", trades)
	}
	if status.Code(err) != codes.ResourceExhausted {
		t.Errorf("status code = %v, want ResourceExhausted", status.Code(err))
	}
}

// TestGetTrades_NilEntries drops a nil record rather than panicking on it.
func TestGetTrades_NilEntries(t *testing.T) {
	client := tradesClient(&accounts.TradesResponse{
		Trades: []*tradeapiv1.AccountTrade{nil, {TradeId: "T1"}},
	}, nil, nil)

	trades, err := client.GetTrades("ACC001", time.Now().Add(-time.Hour), time.Now(), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(trades) != 1 || trades[0].ID != "T1" {
		t.Fatalf("got %+v, want just T1", trades)
	}
	if !trades[0].Timestamp.IsZero() {
		t.Errorf("Timestamp = %v, want the zero time for an absent timestamp", trades[0].Timestamp)
	}
}
