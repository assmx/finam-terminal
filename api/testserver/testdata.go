package testserver

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	tradeapiv1 "github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1"
	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/accounts"
	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/assets"
	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/corporateactions"
	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/marketdata"
	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/metrics"
	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/orders"
	"google.golang.org/genproto/googleapis/type/date"
	"google.golang.org/genproto/googleapis/type/decimal"
	"google.golang.org/genproto/googleapis/type/interval"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// MakeJWT generates a minimal valid JWT with the given expiry time.
func MakeJWT(expiry time.Time) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{
		"exp": expiry.Unix(),
		"sub": "test-user",
	})
	payload := base64.RawURLEncoding.EncodeToString(claims)
	sig := base64.RawURLEncoding.EncodeToString([]byte("fake-signature"))
	return fmt.Sprintf("%s.%s.%s", header, payload, sig)
}

// DefaultAssets returns a set of realistic instruments for testing.
func DefaultAssets() []*assets.Asset {
	// Types are the values the real bulk list uses (confirmed 2026-09-03 over
	// the whole catalogue). ROSN deliberately carries none, so "the API sent
	// no type" stays covered alongside the populated cases.
	return []*assets.Asset{
		{Ticker: "SBER", Symbol: "SBER@TQBR", Name: "Сбер Банк", Mic: "TQBR", Type: "EQUITIES"},
		{Ticker: "GAZP", Symbol: "GAZP@TQBR", Name: "Газпром", Mic: "TQBR", Type: "EQUITIES"},
		{Ticker: "LKOH", Symbol: "LKOH@TQBR", Name: "ЛУКОЙЛ", Mic: "TQBR", Type: "BONDS"},
		{Ticker: "YNDX", Symbol: "YNDX@TQBR", Name: "Яндекс", Mic: "TQBR", Type: "FUTURES"},
		{Ticker: "ROSN", Symbol: "ROSN@TQBR", Name: "Роснефть", Mic: "TQBR"},
	}
}

// DefaultQuotas returns three API quotas at different fill levels, shaped like
// the real answer observed on 2026-09-03: names in Service.methodCamelCase
// form, a uniform limit of 200 with one small outlier, and — the case that
// shapes the renderer — no reset_time on a quota nothing has spent this window.
//
// The list is deliberately not in remaining-share order, so a test can tell
// sorting apart from the order the API happened to send.
func DefaultQuotas() []*metrics.GetUsageMetricsResponse_QuotaUsage {
	reset := timestamppb.New(time.Date(2026, 9, 3, 17, 11, 21, 0, time.UTC))
	return []*metrics.GetUsageMetricsResponse_QuotaUsage{
		// Half spent.
		{Name: "MarketDataService.lastQuote", Limit: 200, Remaining: 100, ResetTime: reset},
		// Nearly exhausted.
		{Name: "AccountsService.getAccount", Limit: 200, Remaining: 12, ResetTime: reset},
		// Untouched: the API sends no reset_time for these.
		{Name: "ReportsService.createAccountReport", Limit: 3, Remaining: 3},
	}
}

// DefaultMCPortfolio returns a margin (MC) account: cash in two currencies —
// the second one negative, because a borrowed balance is a normal state the
// mapping must carry through with its sign — plus the three margin numbers.
func DefaultMCPortfolio() AccountPortfolio {
	return AccountPortfolio{
		Cash: []*money.Money{
			{CurrencyCode: "RUB", Units: 125000, Nanos: 500000000},
			{CurrencyCode: "USD", Units: -300, Nanos: -250000000},
		},
		MC: &accounts.MC{
			AvailableCash:     &decimal.Decimal{Value: "120000.50"},
			InitialMargin:     &decimal.Decimal{Value: "80000"},
			MaintenanceMargin: &decimal.Decimal{Value: "40000"},
		},
		FirstTradeDate:    timestamppb.New(time.Date(2019, 4, 15, 10, 30, 0, 0, time.UTC)),
		FirstNonTradeDate: timestamppb.New(time.Date(2019, 4, 10, 8, 0, 0, 0, time.UTC)),
	}
}

// DefaultFORTSPortfolio returns a derivatives (FORTS) account, where the broker
// reports money_reserved instead of the MC margin pair.
func DefaultFORTSPortfolio() AccountPortfolio {
	return AccountPortfolio{
		Cash: []*money.Money{
			{CurrencyCode: "RUB", Units: 75000, Nanos: 0},
		},
		FORTS: &accounts.FORTS{
			AvailableCash: &decimal.Decimal{Value: "75000"},
			MoneyReserved: &decimal.Decimal{Value: "25000.25"},
		},
	}
}

// DefaultAccountPositions returns positions for a test account.
func DefaultAccountPositions(accountID string) []*accounts.Position {
	return []*accounts.Position{
		{
			Symbol:       "SBER@TQBR",
			Quantity:     &decimal.Decimal{Value: "100"},
			AveragePrice: &decimal.Decimal{Value: "280.50"},
			CurrentPrice: &decimal.Decimal{Value: "285.00"},
		},
		{
			Symbol:       "GAZP@TQBR",
			Quantity:     &decimal.Decimal{Value: "50"},
			AveragePrice: &decimal.Decimal{Value: "155.00"},
			CurrentPrice: &decimal.Decimal{Value: "160.30"},
		},
		// Zero-quantity position (should be filtered by client)
		{
			Symbol:       "LKOH@TQBR",
			Quantity:     &decimal.Decimal{Value: "0"},
			AveragePrice: &decimal.Decimal{Value: "7000.00"},
			CurrentPrice: &decimal.Decimal{Value: "7100.00"},
		},
	}
}

// DefaultQuote returns a realistic quote for the given symbol.
func DefaultQuote(symbol string) *marketdata.Quote {
	quotes := map[string]*marketdata.Quote{
		"SBER@TQBR": {
			Symbol: "SBER@TQBR",
			Last:   &decimal.Decimal{Value: "285.00"},
			Bid:    &decimal.Decimal{Value: "284.90"},
			Ask:    &decimal.Decimal{Value: "285.10"},
			Close:  &decimal.Decimal{Value: "280.00"},
			Change: &decimal.Decimal{Value: "5.00"},
		},
		"GAZP@TQBR": {
			Symbol: "GAZP@TQBR",
			Last:   &decimal.Decimal{Value: "160.30"},
			Bid:    &decimal.Decimal{Value: "160.20"},
			Ask:    &decimal.Decimal{Value: "160.40"},
			Close:  &decimal.Decimal{Value: "162.00"},
			Change: &decimal.Decimal{Value: "-1.70"},
		},
	}
	if q, ok := quotes[symbol]; ok {
		return q
	}
	if q, ok := fxQuotes()[symbol]; ok {
		return q
	}
	return nil
}

// fxQuotes are currency-pair quotes shaped like the answers of 2026-09-10:
// live pairs with a recent timestamp — KZT quoted per 100 units — and one
// frozen EUR pair that still answers with its January 2025 price, which is how
// every EUR pair on MISX looked. Built on each call so "recent" stays recent.
func fxQuotes() map[string]*marketdata.Quote {
	recent := timestamppb.New(time.Now().Add(-time.Minute))
	return map[string]*marketdata.Quote{
		"USD000UTSTOM@MISX": {
			Symbol: "USD000UTSTOM@MISX", Timestamp: recent,
			Last: &decimal.Decimal{Value: "84.26"}, Close: &decimal.Decimal{Value: "85.1675"},
		},
		"CNYRUB_TOM@MISX": {
			Symbol: "CNYRUB_TOM@MISX", Timestamp: recent,
			Last: &decimal.Decimal{Value: "12.53"}, Close: &decimal.Decimal{Value: "12.665"},
		},
		"EURRUB@#WWCP": {
			Symbol: "EURRUB@#WWCP", Timestamp: recent,
			Last: &decimal.Decimal{Value: "97.634"}, Close: &decimal.Decimal{Value: "98.874"},
		},
		"KZTRUB_TOM@MISX": {
			Symbol: "KZTRUB_TOM@MISX", Timestamp: recent,
			Last: &decimal.Decimal{Value: "19.11"}, Close: &decimal.Decimal{Value: "19.1975"},
		},
		"EUR_RUB__TOM@MISX": {
			Symbol:    "EUR_RUB__TOM@MISX",
			Timestamp: timestamppb.New(time.Date(2025, 1, 9, 7, 0, 3, 0, time.UTC)),
			Last:      &decimal.Decimal{Value: "95.62"}, Close: &decimal.Decimal{Value: "95.62"},
		},
	}
}

// DefaultStreamQuote returns a quote as SubscribeQuote would deliver it. A
// snapshot carries the full state and sets IsDataSnapshot; an incremental
// update carries only Last and Timestamp, like the real stream.
func DefaultStreamQuote(symbol string, snapshot bool) *marketdata.Quote {
	ts := timestamppb.New(time.Date(2026, 8, 13, 10, 30, 0, 0, time.UTC))

	if !snapshot {
		return &marketdata.Quote{
			Symbol:    symbol,
			Timestamp: ts,
			Last:      &decimal.Decimal{Value: "291.00"},
		}
	}

	return &marketdata.Quote{
		Symbol:         symbol,
		Timestamp:      ts,
		Ask:            &decimal.Decimal{Value: "290.10"},
		AskSize:        &decimal.Decimal{Value: "50"},
		Bid:            &decimal.Decimal{Value: "289.90"},
		BidSize:        &decimal.Decimal{Value: "100"},
		Last:           &decimal.Decimal{Value: "290.00"},
		LastSize:       &decimal.Decimal{Value: "10"},
		Volume:         &decimal.Decimal{Value: "1500000"},
		Turnover:       &decimal.Decimal{Value: "435000000"},
		Open:           &decimal.Decimal{Value: "288.00"},
		High:           &decimal.Decimal{Value: "292.00"},
		Low:            &decimal.Decimal{Value: "287.50"},
		Close:          &decimal.Decimal{Value: "289.00"},
		Change:         &decimal.Decimal{Value: "1.00"},
		OpenInterest:   &decimal.Decimal{Value: "0"},
		IsDataSnapshot: true,
	}
}

// DefaultBars returns 5 candlesticks for testing.
func DefaultBars(symbol string) []*marketdata.Bar {
	base := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	bars := make([]*marketdata.Bar, 5)
	for i := range bars {
		t := base.Add(time.Duration(i) * time.Hour)
		bars[i] = &marketdata.Bar{
			Timestamp: timestamppb.New(t),
			Open:      &decimal.Decimal{Value: fmt.Sprintf("%.2f", 280.0+float64(i))},
			High:      &decimal.Decimal{Value: fmt.Sprintf("%.2f", 282.0+float64(i))},
			Low:       &decimal.Decimal{Value: fmt.Sprintf("%.2f", 279.0+float64(i))},
			Close:     &decimal.Decimal{Value: fmt.Sprintf("%.2f", 281.0+float64(i))},
			Volume:    &decimal.Decimal{Value: fmt.Sprintf("%d", 1000+i*100)},
		}
	}
	return bars
}

// DefaultOrders returns a mix of order types for testing.
func DefaultOrders(accountID string) []*orders.OrderState {
	return []*orders.OrderState{
		{
			OrderId: "ORD001",
			Status:  orders.OrderStatus_ORDER_STATUS_NEW,
			// This stop order spawned ORD002 (both present in the active set → link is visible on both sides)
			TriggeredOrderId: "ORD002",
			Order: &orders.Order{
				AccountId:  accountID,
				Symbol:     "SBER@TQBR",
				Side:       tradeapiv1.Side_SIDE_BUY,
				Type:       orders.OrderType_ORDER_TYPE_LIMIT,
				Quantity:   &decimal.Decimal{Value: "10"},
				LimitPrice: &decimal.Decimal{Value: "280.00"},
			},
		},
		{
			OrderId: "ORD002",
			Status:  orders.OrderStatus_ORDER_STATUS_NEW,
			Order: &orders.Order{
				AccountId: accountID,
				Symbol:    "GAZP@TQBR",
				Side:      tradeapiv1.Side_SIDE_SELL,
				Type:      orders.OrderType_ORDER_TYPE_MARKET,
				Quantity:  &decimal.Decimal{Value: "5"},
			},
		},
	}
}

// DefaultTrades returns trade history entries for testing.
func DefaultTrades(accountID string) []*tradeapiv1.AccountTrade {
	// Anchored a few days back rather than on a fixed date: GetTradeHistory
	// asks for the last 30 days and the mock honours the interval, so a
	// hard-coded date would silently age out of the window.
	t := time.Now().UTC().Add(-5 * 24 * time.Hour)
	return []*tradeapiv1.AccountTrade{
		{
			TradeId:   "TRD001",
			AccountId: accountID,
			Symbol:    "SBER@TQBR",
			Side:      tradeapiv1.Side_SIDE_BUY,
			Size:      &decimal.Decimal{Value: "10"},
			Price:     &decimal.Decimal{Value: "280.50"},
			Timestamp: timestamppb.New(t),
		},
		{
			TradeId:   "TRD002",
			AccountId: accountID,
			Symbol:    "GAZP@TQBR",
			Side:      tradeapiv1.Side_SIDE_SELL,
			Size:      &decimal.Decimal{Value: "5"},
			Price:     &decimal.Decimal{Value: "160.00"},
			Timestamp: timestamppb.New(t.Add(30 * time.Minute)),
		},
		// Bond trade carries accrued interest (2.16.0) and an explicit price currency
		{
			TradeId:         "TRD003",
			AccountId:       accountID,
			Symbol:          "SU26238@TQOB",
			Side:            tradeapiv1.Side_SIDE_BUY,
			Size:            &decimal.Decimal{Value: "3"},
			Price:           &decimal.Decimal{Value: "650.10"},
			AccruedInterest: &decimal.Decimal{Value: "12.34"},
			Currency:        "RUB",
			Timestamp:       timestamppb.New(t.Add(time.Hour)),
		},
	}
}

// currencyInstrument is one instrument of the currency reconnaissance
// (2026-09-10): the GetAsset and GetAssetParams answers the real API gave for
// it, trimmed to the fields the currency layer reads.
type currencyInstrument struct {
	info   *assets.GetAssetResponse
	params *assets.GetAssetParamsResponse
}

// currencyBond builds the GetAsset answer of a bond as the real API shapes it:
// the face value, and "%" in the field a reader would expect to hold the face
// currency — it is the unit of the price, on every bond observed.
func currencyBond(ticker, board, name, quote, face string) *assets.GetAssetResponse {
	return &assets.GetAssetResponse{
		Ticker:        ticker,
		Board:         board,
		Mic:           "MISX",
		Type:          "BONDS",
		Name:          name,
		LotSize:       &decimal.Decimal{Value: "1"},
		Decimals:      4,
		QuoteCurrency: quote,
		AssetDetails: &assets.GetAssetResponse_BondDetails_{BondDetails: &assets.GetAssetResponse_BondDetails{
			BondFaceValue: &decimal.Decimal{Value: face},
			Currency:      "%",
		}},
	}
}

// currencyParams builds a GetAssetParams answer carrying the long margin pair
// the per-piece value is derived from (margin × 100 / risk rate / lot).
func currencyParams(symbol, riskRate, currency string, units int64, nanos int32) *assets.GetAssetParamsResponse {
	return &assets.GetAssetParamsResponse{
		Symbol:            symbol,
		Longable:          &assets.Longable{Value: assets.Longable_AVAILABLE},
		Shortable:         &assets.Shortable{Value: assets.Shortable_NOT_AVAILABLE},
		LongRiskRate:      &decimal.Decimal{Value: riskRate},
		LongInitialMargin: &money.Money{CurrencyCode: currency, Units: units, Nanos: nanos},
		TradeLotSize:      1,
	}
}

// currencyInstruments are the reconnaissance instruments by symbol. Three
// bonds cover the three shapes the currency layer has to tell apart:
//
//   - RU000A10DQA8 «ОФЗ 33 CNY» trades and settles in yuan (board TQOY):
//     quote_currency CNY, face 10 000, a per-piece value ≈ price × face / 100.
//   - RU000A10A851 «РФ ЗО 27 Д» is a replacement bond: a 200 000 USD face
//     settled in roubles, so quote_currency is RUB and the per-piece value is
//     ~85 times price × face / 100.
//   - RU000A1087C3 «ГПБ3P6CNY» has a yuan face settled in roubles: the same
//     mismatch at the yuan rate (~13).
//
// YDEX@MISX is an ordinary rouble equity.
//
// Each lookup builds fresh messages, so no two calls ever share a proto.
func lookupCurrencyInstrument(symbol string) (currencyInstrument, bool) {
	switch symbol {
	case "RU000A10DQA8@MISX":
		return currencyInstrument{
			info:   currencyBond("RU000A10DQA8", "TQOY", "ОФЗ 33 CNY", "CNY", "10000.0"),
			params: currencyParams(symbol, "25.0", "CNY", 2387, 82500000),
		}, true
	case "RU000A10A851@MISX":
		return currencyInstrument{
			info:   currencyBond("RU000A10A851", "TQCB", "РФ ЗО 27 Д", "RUB", "200000.0"),
			params: currencyParams(symbol, "33.0", "RUB", 5489206, 908900000),
		}, true
	case "RU000A1087C3@MISX":
		return currencyInstrument{
			info:   currencyBond("RU000A1087C3", "TQCB", "ГПБ3P6CNY", "RUB", "100.0"),
			params: currencyParams(symbol, "100.0", "RUB", 1320, 679159000),
		}, true
	case "YDEX@MISX":
		return currencyInstrument{
			info: &assets.GetAssetResponse{
				Ticker:        "YDEX",
				Board:         "TQBR",
				Mic:           "MISX",
				Type:          "EQUITIES",
				Name:          "ЯНДЕКС",
				LotSize:       &decimal.Decimal{Value: "1"},
				Decimals:      1,
				QuoteCurrency: "RUB",
			},
			params: currencyParams(symbol, "15.0", "RUB", 571, 650000000),
		}, true
	}
	return currencyInstrument{}, false
}

// CurrencyAccountPositions returns a portfolio holding every reconnaissance
// instrument, for tests that need the currency layer end to end. Bond prices
// are percentages of face, as the real API sends them.
func CurrencyAccountPositions() []*accounts.Position {
	return []*accounts.Position{
		{
			Symbol:       "YDEX@MISX",
			Quantity:     &decimal.Decimal{Value: "9"},
			AveragePrice: &decimal.Decimal{Value: "4314.8"},
			CurrentPrice: &decimal.Decimal{Value: "3811.0"},
		},
		{
			Symbol:       "RU000A10DQA8@MISX",
			Quantity:     &decimal.Decimal{Value: "2"},
			AveragePrice: &decimal.Decimal{Value: "93.1"},
			CurrentPrice: &decimal.Decimal{Value: "93.5"},
		},
		{
			Symbol:       "RU000A10A851@MISX",
			Quantity:     &decimal.Decimal{Value: "1"},
			AveragePrice: &decimal.Decimal{Value: "96.0"},
			CurrentPrice: &decimal.Decimal{Value: "97.25"},
		},
		{
			Symbol:       "RU000A1087C3@MISX",
			Quantity:     &decimal.Decimal{Value: "10"},
			AveragePrice: &decimal.Decimal{Value: "99.0"},
			CurrentPrice: &decimal.Decimal{Value: "101.5662"},
		},
	}
}

// DefaultAssetInfo returns a GetAssetResponse for the given symbol.
func DefaultAssetInfo(symbol string) *assets.GetAssetResponse {
	if inst, ok := lookupCurrencyInstrument(symbol); ok {
		return inst.info
	}

	ticker := symbolTicker(symbol)
	mic := "TQBR"
	if i := len(ticker); i < len(symbol) {
		mic = symbol[i+1:] // extract MIC from ticker@MIC
	}
	return &assets.GetAssetResponse{
		Ticker:   ticker,
		Board:    mic,
		Mic:      mic,
		Name:     "Test Asset " + symbol,
		LotSize:  &decimal.Decimal{Value: "10"},
		Decimals: 2,
	}
}

// DefaultAssetParams returns trading parameters for the given symbol.
// TradeLotSize is deliberately 5 while DefaultAssetInfo reports LotSize 10, so
// tests can prove that the trade lot (GetAssetParams.trade_lot_size) wins over
// the asset lot (GetAsset.lot_size) end to end.
func DefaultAssetParams(symbol string) *assets.GetAssetParamsResponse {
	if inst, ok := lookupCurrencyInstrument(symbol); ok {
		return inst.params
	}

	return &assets.GetAssetParamsResponse{
		Symbol:       symbol,
		Longable:     &assets.Longable{Value: assets.Longable_AVAILABLE},
		Shortable:    &assets.Shortable{Value: assets.Shortable_AVAILABLE},
		TradeLotSize: 5,
	}
}

// DefaultSchedule returns a trading schedule.
func DefaultSchedule() *assets.ScheduleResponse {
	today := time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC)
	return &assets.ScheduleResponse{
		Sessions: []*assets.ScheduleResponse_Sessions{
			{
				Type: "main",
				Interval: &interval.Interval{
					StartTime: timestamppb.New(today.Add(7 * time.Hour)),
					EndTime:   timestamppb.New(today.Add(15*time.Hour + 40*time.Minute)),
				},
			},
		},
	}
}

// DefaultDividends returns past and future dividend fixtures. The past set is
// returned DESC-friendly and the future set ASC-friendly so client-side merging
// and IsFuture flagging can be asserted.
func DefaultDividends() (past, future []*corporateactions.Dividend) {
	past = []*corporateactions.Dividend{
		{
			Date:     &date.Date{Year: 2026, Month: 3, Day: 15},
			Amount:   &decimal.Decimal{Value: "15.5"},
			Currency: "RUB",
		},
	}
	future = []*corporateactions.Dividend{
		{
			Date:     &date.Date{Year: 2026, Month: 9, Day: 15},
			Amount:   &decimal.Decimal{Value: "20.0"},
			Currency: "RUB",
		},
	}
	return past, future
}

// DefaultSplits returns past and future split fixtures. The future split has a
// nil NewLot wrapper to exercise nil-safe handling of *wrapperspb.Int32Value.
func DefaultSplits() (past, future []*corporateactions.SplitInfo) {
	past = []*corporateactions.SplitInfo{
		{
			ExecDate:         &date.Date{Year: 2025, Month: 6, Day: 1},
			OldRatio:         &decimal.Decimal{Value: "1"},
			NewRatio:         &decimal.Decimal{Value: "10"},
			NewLot:           wrapperspb.Int32(1),
			ConvertationType: corporateactions.ConvertationType_ORDINARY,
		},
	}
	future = []*corporateactions.SplitInfo{
		{
			ExecDate:         &date.Date{Year: 2026, Month: 8, Day: 1},
			OldRatio:         &decimal.Decimal{Value: "2"},
			NewRatio:         &decimal.Decimal{Value: "1"},
			NewLot:           nil, // exercise nil Int32 wrapper
			ConvertationType: corporateactions.ConvertationType_TENDER_OFFER,
		},
	}
	return past, future
}

// DefaultBondEvents returns past and future bond-event fixtures covering all
// three oneof branches (coupon, amortization, offer) and nil pointer wrappers
// (the offer has nil Value and nil Currency).
func DefaultBondEvents() (past, future []*corporateactions.BondEvent) {
	past = []*corporateactions.BondEvent{
		{
			Date:     &date.Date{Year: 2026, Month: 1, Day: 20},
			Type:     corporateactions.BondEventType_COUPON,
			Value:    &decimal.Decimal{Value: "34.9"},
			Currency: wrapperspb.String("RUB"),
			EventDetails: &corporateactions.BondEvent_CouponDetails{
				CouponDetails: &corporateactions.CouponEventDetails{
					RecordDate:   &date.Date{Year: 2026, Month: 1, Day: 18},
					StartDate:    &date.Date{Year: 2025, Month: 7, Day: 20},
					FaceValue:    &decimal.Decimal{Value: "1000"},
					ValuePercent: &decimal.Decimal{Value: "6.98"},
				},
			},
		},
	}
	future = []*corporateactions.BondEvent{
		{
			Date:     &date.Date{Year: 2026, Month: 10, Day: 20},
			Type:     corporateactions.BondEventType_AMORTIZATION,
			Value:    &decimal.Decimal{Value: "200"},
			Currency: wrapperspb.String("RUB"),
			EventDetails: &corporateactions.BondEvent_AmortizationDetails{
				AmortizationDetails: &corporateactions.AmortizationEventDetails{
					NewFaceValue:        &decimal.Decimal{Value: "800"},
					InitialFaceValue:    &decimal.Decimal{Value: "1000"},
					AmortizationPercent: &decimal.Decimal{Value: "20"},
				},
			},
		},
		{
			Date:     &date.Date{Year: 2026, Month: 11, Day: 15},
			Type:     corporateactions.BondEventType_OFFER,
			Value:    nil, // offer: only price matters
			Currency: nil, // exercise nil StringValue wrapper
			EventDetails: &corporateactions.BondEvent_OfferDetails{
				OfferDetails: &corporateactions.OfferEventDetails{
					OfferType: wrapperspb.String("PUT"),
					Price:     &decimal.Decimal{Value: "100"},
					StartDate: &date.Date{Year: 2026, Month: 11, Day: 10},
					EndDate:   &date.Date{Year: 2026, Month: 11, Day: 14},
					Agent:     wrapperspb.String("Sberbank CIB"),
				},
			},
		},
	}
	return past, future
}

// currencyCoupon is a 2026 coupon as the real calendar sends it: the currency
// as a symbol ("$", "¥", "₽", "€"), not an ISO code.
func currencyCoupon(month, day int32, value, currency, face string) *corporateactions.BondEvent {
	return &corporateactions.BondEvent{
		Date:     &date.Date{Year: 2026, Month: month, Day: day},
		Type:     corporateactions.BondEventType_COUPON,
		Value:    &decimal.Decimal{Value: value},
		Currency: wrapperspb.String(currency),
		EventDetails: &corporateactions.BondEvent_CouponDetails{
			CouponDetails: &corporateactions.CouponEventDetails{FaceValue: &decimal.Decimal{Value: face}},
		},
	}
}

// CurrencyBondCalendars returns the reconnaissance bonds' calendars, keyed by
// symbol, for MockCorporateActionsServer.BondCalendars. The calendar is the
// only place the API names a bond's face currency:
//
//   - «РФ ЗО 27 Д» pays in "$" on a 200 000 face, both behind and ahead.
//   - «ГПБ3P6CNY» pays in "¥" although GetAsset says it settles in roubles.
//   - «ОФЗ 33 CNY» has a past coupon in "¥" and nothing scheduled, so a lookup
//     that starts with the future calendar has to fall back to the past one.
func CurrencyBondCalendars() map[string]BondCalendar {
	return map[string]BondCalendar{
		"RU000A10A851@MISX": {
			Past:   []*corporateactions.BondEvent{currencyCoupon(6, 23, "4250.0", "$", "200000.0")},
			Future: []*corporateactions.BondEvent{currencyCoupon(12, 23, "4250.0", "$", "200000.0")},
		},
		"RU000A1087C3@MISX": {
			Past:   []*corporateactions.BondEvent{currencyCoupon(4, 9, "2.49", "¥", "100.0")},
			Future: []*corporateactions.BondEvent{currencyCoupon(10, 9, "2.51", "¥", "100.0")},
		},
		"RU000A10DQA8@MISX": {
			Past: []*corporateactions.BondEvent{currencyCoupon(6, 10, "352.88", "¥", "10000.0")},
		},
	}
}

func symbolTicker(symbol string) string {
	for i, c := range symbol {
		if c == '@' {
			return symbol[:i]
		}
	}
	return symbol
}

// DefaultConstituents returns the IMOEX index composition as GetConstituents
// delivers it: two pages, so the pagination loop is exercised even though the
// real IMOEX fits in one. Page 1 (cursor 0) ends with next_cursor 2; page 2
// (cursor 2) ends with next_cursor 0, marking the last page.
//
// Weights are deliberately out of order so a caller that sorts by weight is
// distinguishable from one that keeps the API order.
func DefaultConstituents(cursor int64) *assets.GetConstituentsResponse {
	if cursor == 0 {
		return &assets.GetConstituentsResponse{
			Constituents: []*assets.Constituents{
				{
					Symbol: "SBER@MISX",
					Name:   "Сбербанк",
					Sector: "Финансы",
					Weight: &decimal.Decimal{Value: "0.0080"},
				},
				{
					Symbol: "GAZP@MISX",
					Name:   "Газпром",
					Sector: "Нефть и газ",
					Weight: &decimal.Decimal{Value: "0.0120"},
				},
			},
			NextCursor: 2,
		}
	}

	return &assets.GetConstituentsResponse{
		Constituents: []*assets.Constituents{
			{
				Symbol: "LKOH@MISX",
				Name:   "ЛУКОЙЛ",
				Sector: "Нефть и газ",
				Weight: &decimal.Decimal{Value: "0.0100"},
			},
			{
				// No weight: the mapping must tolerate a nil wrapper.
				Symbol: "MOEX@MISX",
				Name:   "МосБиржа",
				Sector: "Финансы",
			},
		},
		NextCursor: 0,
	}
}

// DefaultTransactions returns a transaction fixture covering every category the
// cash-flow grouping switches on, plus the three shapes that are easy to get
// wrong: a charge (negative money), a securities transfer (a quantity and no
// money) and a transaction that reflects a trade (which must stay out of the
// flow totals so the realised result is not counted twice alongside FIFO).
//
// A foreign-currency deposit is included because only base-currency flows enter
// the since-open figures — the Trade API carries no exchange rates — and the
// renderer has to say what it left out.
func DefaultTransactions() []*accounts.Transaction {
	// Same reasoning as DefaultTrades: anchored relative to now so a window a
	// test calls "recent" actually contains the fixture.
	t := time.Now().UTC().Add(-10 * 24 * time.Hour)
	at := func(d time.Duration) *timestamppb.Timestamp { return timestamppb.New(t.Add(d)) }

	return []*accounts.Transaction{
		{
			Id:                  "TX001",
			TransactionCategory: accounts.Transaction_DEPOSIT,
			TransactionName:     "Ввод денежных средств",
			Timestamp:           at(0),
			Change:              &money.Money{CurrencyCode: "RUB", Units: 100000},
		},
		{
			Id:                  "TX002",
			TransactionCategory: accounts.Transaction_DEPOSIT,
			TransactionName:     "Ввод валюты",
			Timestamp:           at(time.Hour),
			Change:              &money.Money{CurrencyCode: "USD", Units: 500},
		},
		{
			Id:                  "TX003",
			TransactionCategory: accounts.Transaction_COMMISSION,
			TransactionName:     "Комиссия брокера",
			Timestamp:           at(2 * time.Hour),
			Symbol:              "SBER@TQBR",
			Change:              &money.Money{CurrencyCode: "RUB", Units: -1, Nanos: -500000000},
		},
		{
			Id:                  "TX004",
			TransactionCategory: accounts.Transaction_TAX,
			TransactionName:     "НДФЛ",
			Timestamp:           at(3 * time.Hour),
			Change:              &money.Money{CurrencyCode: "RUB", Units: -250},
		},
		{
			Id:                  "TX005",
			TransactionCategory: accounts.Transaction_INCOME,
			TransactionName:     "Дивиденды SBER",
			Timestamp:           at(4 * time.Hour),
			Symbol:              "SBER@TQBR",
			Change:              &money.Money{CurrencyCode: "RUB", Units: 3400, Nanos: 250000000},
		},
		{
			Id:                  "TX006",
			TransactionCategory: accounts.Transaction_LOAN,
			TransactionName:     "Проценты по займу",
			Timestamp:           at(5 * time.Hour),
			Change:              &money.Money{CurrencyCode: "RUB", Units: -37, Nanos: -800000000},
		},
		{
			Id:                  "TX007",
			TransactionCategory: accounts.Transaction_FINE,
			TransactionName:     "Штраф",
			Timestamp:           at(6 * time.Hour),
			Change:              &money.Money{CurrencyCode: "RUB", Units: -10},
		},
		{
			Id:                  "TX008",
			TransactionCategory: accounts.Transaction_TRANSFER,
			TransactionName:     "Перевод бумаг",
			Timestamp:           at(7 * time.Hour),
			Symbol:              "GAZP@TQBR",
			ChangeQty:           &decimal.Decimal{Value: "-10"},
		},
		{
			Id:                  "TX009",
			TransactionCategory: accounts.Transaction_WITHDRAW,
			TransactionName:     "Вывод денежных средств",
			Timestamp:           at(8 * time.Hour),
			Change:              &money.Money{CurrencyCode: "RUB", Units: -20000},
		},
		{
			Id:                  "TX010",
			TransactionCategory: accounts.Transaction_OTHERS,
			TransactionName:     "Покупка SBER",
			Timestamp:           at(9 * time.Hour),
			Symbol:              "SBER@TQBR",
			Change:              &money.Money{CurrencyCode: "RUB", Units: -2805},
			Trade: &accounts.Transaction_Trade{
				Size:  &decimal.Decimal{Value: "10"},
				Price: &decimal.Decimal{Value: "280.50"},
			},
		},
		{
			Id:                  "TX011",
			TransactionCategory: accounts.Transaction_OTHERS,
			TransactionName:     "Покупка облигации",
			Timestamp:           at(10 * time.Hour),
			Symbol:              "SU26238@TQOB",
			Change:              &money.Money{CurrencyCode: "RUB", Units: -1962, Nanos: -640000000},
			Trade: &accounts.Transaction_Trade{
				Size:            &decimal.Decimal{Value: "3"},
				Price:           &decimal.Decimal{Value: "650.10"},
				AccruedInterest: &decimal.Decimal{Value: "12.34"},
			},
		},
	}
}

// LowTradesQuota returns a quota table whose AccountsService.trades entry is
// nearly spent, so a long history pass has to be refused before it starts.
//
// The other two entries are the collisions that make suffix matching on the
// bare method name wrong: both end in "Trades" and neither is the quota the
// loader spends.
func LowTradesQuota() []*metrics.GetUsageMetricsResponse_QuotaUsage {
	return []*metrics.GetUsageMetricsResponse_QuotaUsage{
		{Name: "AccountsService.trades", Limit: 200, Remaining: 21,
			ResetTime: timestamppb.New(time.Now().Add(37 * time.Second))},
		{Name: "AccountsService.transactions", Limit: 200, Remaining: 200},
		{Name: "OrdersService.subscribeTrades", Limit: 200, Remaining: 200},
		{Name: "MarketDataService.latestTrades", Limit: 200, Remaining: 200},
	}
}
