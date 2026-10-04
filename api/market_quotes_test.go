package api

import (
	"context"
	"reflect"
	"testing"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/assets"
	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/marketdata"
	"google.golang.org/genproto/googleapis/type/decimal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGetMarketQuotesAccountlessBudgetAndGuards(t *testing.T) {
	var called []string
	c := &Client{
		assetsClient: &mockAssetsServiceClient{
			GetAssetFunc: func(context.Context, *assets.GetAssetRequest, ...grpc.CallOption) (*assets.GetAssetResponse, error) {
				t.Fatal("market price request fetched account-requiring asset metadata")
				return nil, nil
			},
			GetAssetParamsFunc: func(context.Context, *assets.GetAssetParamsRequest, ...grpc.CallOption) (*assets.GetAssetParamsResponse, error) {
				t.Fatal("market price request fetched trading lot metadata")
				return nil, nil
			},
		},
		marketDataClient: &mockMarketDataServiceClient{
			LastQuoteFunc: func(ctx context.Context, req *marketdata.QuoteRequest, _ ...grpc.CallOption) (*marketdata.QuoteResponse, error) {
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("market quote has no per-call deadline")
				}
				called = append(called, req.Symbol)
				switch req.Symbol {
				case "ES@XCME":
					return &marketdata.QuoteResponse{Quote: &marketdata.Quote{Last: &decimal.Decimal{Value: "6701.25"}}}, nil
				case "NGZ6@RTSX":
					return nil, status.Error(codes.Unavailable, "temporary outage")
				case "BRZ6@RTSX":
					return nil, nil
				case "GDX6@RTSX":
					return &marketdata.QuoteResponse{}, nil
				case "USD000UTSTOM@MISX":
					return &marketdata.QuoteResponse{Quote: &marketdata.Quote{Last: &decimal.Decimal{Value: "83.5"}}}, nil
				default:
					t.Fatalf("unexpected quote request %q", req.Symbol)
					return nil, nil
				}
			},
		},
	}
	quotes, err := c.GetMarketQuotes([]string{"ES@XCME", "FXRL.MMBZ@_MMBZ", "AAPL.SPBZ@_SPBZ", "ES", "@XCME", "ES@", "NGZ6@RTSX", "BRZ6@RTSX", "GDX6@RTSX", "USD000UTSTOM@MISX"})
	if err != nil || len(quotes) != 2 || quotes["ES@XCME"].Last != "6701.25" || quotes["USD000UTSTOM@MISX"].Last != "83.5" {
		t.Fatalf("market prices = %+v, %v", quotes, err)
	}
	wantCalls := []string{"ES@XCME", "NGZ6@RTSX", "BRZ6@RTSX", "GDX6@RTSX", "USD000UTSTOM@MISX"}
	if !reflect.DeepEqual(called, wantCalls) {
		t.Fatalf("unary quote budget = %v; want %v", called, wantCalls)
	}
}

func TestGetMarketQuotesRateLimitStopsWithPartialPrices(t *testing.T) {
	calls := 0
	c := &Client{marketDataClient: &mockMarketDataServiceClient{
		LastQuoteFunc: func(context.Context, *marketdata.QuoteRequest, ...grpc.CallOption) (*marketdata.QuoteResponse, error) {
			calls++
			if calls == 2 {
				return nil, status.Error(codes.ResourceExhausted, "quota")
			}
			return &marketdata.QuoteResponse{Quote: &marketdata.Quote{Last: &decimal.Decimal{Value: "100"}}}, nil
		},
	}}
	quotes, err := c.GetMarketQuotes([]string{"ES@XCME", "SFZ6@RTSX", "USD000UTSTOM@MISX"})
	if !IsRateLimited(err) || len(quotes) != 1 || quotes["ES@XCME"].Last != "100" || calls != 2 {
		t.Fatalf("rate limited prices = %+v, %v; calls %d", quotes, err, calls)
	}
}
