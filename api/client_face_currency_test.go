package api

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"finam-terminal/models"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/corporateactions"
	"google.golang.org/genproto/googleapis/type/date"
	"google.golang.org/genproto/googleapis/type/decimal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// mockBondCalendarClient serves the two bond-event RPCs from funcs and counts
// them. Every other corporate-action method panics through the nil embedded
// interface, which is what a test that must not touch them wants.
type mockBondCalendarClient struct {
	corporateactions.CorporateActionsServiceClient

	past   func(*corporateactions.GetPastBondsEventsRequest) (*corporateactions.GetPastBondsEventsResponse, error)
	future func(*corporateactions.GetFutureBondsEventsRequest) (*corporateactions.GetFutureBondsEventsResponse, error)

	pastCalls   atomic.Int64
	futureCalls atomic.Int64
	lastPast    atomic.Pointer[corporateactions.GetPastBondsEventsRequest]
}

func (m *mockBondCalendarClient) GetPastBondsEvents(_ context.Context, in *corporateactions.GetPastBondsEventsRequest, _ ...grpc.CallOption) (*corporateactions.GetPastBondsEventsResponse, error) {
	m.pastCalls.Add(1)
	m.lastPast.Store(in)
	if m.past == nil {
		return &corporateactions.GetPastBondsEventsResponse{}, nil
	}
	return m.past(in)
}

func (m *mockBondCalendarClient) GetFutureBondsEvents(_ context.Context, in *corporateactions.GetFutureBondsEventsRequest, _ ...grpc.CallOption) (*corporateactions.GetFutureBondsEventsResponse, error) {
	m.futureCalls.Add(1)
	if m.future == nil {
		return &corporateactions.GetFutureBondsEventsResponse{}, nil
	}
	return m.future(in)
}

func (m *mockBondCalendarClient) calls() int64 { return m.pastCalls.Load() + m.futureCalls.Load() }

// coupon is a bond-calendar coupon carrying the currency as the real API sends
// it: a symbol, not a code.
func coupon(currency string) *corporateactions.BondEvent {
	e := &corporateactions.BondEvent{
		Date:  &date.Date{Year: 2026, Month: 12, Day: 23},
		Type:  corporateactions.BondEventType_COUPON,
		Value: &decimal.Decimal{Value: "4250.0"},
		EventDetails: &corporateactions.BondEvent_CouponDetails{CouponDetails: &corporateactions.CouponEventDetails{
			FaceValue: &decimal.Decimal{Value: "200000.0"},
		}},
	}
	if currency != "" {
		e.Currency = wrapperspb.String(currency)
	}
	return e
}

func futureEvents(events ...*corporateactions.BondEvent) func(*corporateactions.GetFutureBondsEventsRequest) (*corporateactions.GetFutureBondsEventsResponse, error) {
	return func(*corporateactions.GetFutureBondsEventsRequest) (*corporateactions.GetFutureBondsEventsResponse, error) {
		return &corporateactions.GetFutureBondsEventsResponse{Events: events}, nil
	}
}

func pastEvents(events ...*corporateactions.BondEvent) func(*corporateactions.GetPastBondsEventsRequest) (*corporateactions.GetPastBondsEventsResponse, error) {
	return func(*corporateactions.GetPastBondsEventsRequest) (*corporateactions.GetPastBondsEventsResponse, error) {
		return &corporateactions.GetPastBondsEventsResponse{Events: events}, nil
	}
}

// TestCurrencyCode maps what the calendar sends onto ISO codes. The four
// symbols are the ones the reconnaissance saw; "¥" is the yuan because every
// bond carrying it was a CNY bond — MOEX lists no yen bonds.
func TestCurrencyCode(t *testing.T) {
	tests := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"₽", "RUB", true},
		{"$", "USD", true},
		{"€", "EUR", true},
		{"¥", "CNY", true},
		{" $ ", "USD", true},
		{"USD", "USD", true},
		{"cny", "CNY", true},
		{"RUR", "RUB", true},
		{"SUR", "RUB", true},
		{"£", "", false},
		{"%", "", false},
		{"", "", false},
		{"US", "", false},
		{"USDT", "", false},
		{"12$", "", false},
		{"U$D", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := currencyCode(tt.in)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("currencyCode(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// TestMapBondEvent_NormalisesCurrency: the calendar model carries a code, so
// the payout totals no longer split "₽" from "RUB". A symbol the table does not
// know is kept as sent — for display it beats a blank.
func TestMapBondEvent_NormalisesCurrency(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"dollar", "$", "USD"},
		{"rouble", "₽", "RUB"},
		{"already a code", "EUR", "EUR"},
		{"unknown symbol kept", "£", "£"},
		{"absent", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mapBondEvent(coupon(tt.in), true).Currency; got != tt.want {
				t.Errorf("mapBondEvent currency %q -> %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func faceClient(m *mockBondCalendarClient) *Client {
	return &Client{corporateActionsClient: m}
}

// TestGetBondFaceCurrency_FromCachedCalendar: a calendar the payout screen or
// the profile already loaded answers for free.
func TestGetBondFaceCurrency_FromCachedCalendar(t *testing.T) {
	m := &mockBondCalendarClient{}
	client := faceClient(m)
	client.calendars.entries = map[string]calendarCacheEntry{
		bondEventsCalendar + "|RU000A10A851@MISX": {
			value:    []models.BondEvent{{Kind: models.BondEventCoupon, Currency: "USD"}},
			loadedAt: time.Now(),
		},
	}

	got, err := client.GetBondFaceCurrency("RU000A10A851@MISX")
	if err != nil || got != "USD" {
		t.Fatalf("GetBondFaceCurrency = %q, %v; want USD, nil", got, err)
	}
	if n := m.calls(); n != 0 {
		t.Errorf("a cached calendar cost %d requests, want 0", n)
	}
}

// TestGetBondFaceCurrency_StaleCalendarIgnored: a calendar past its day is not
// evidence; the lookup asks.
func TestGetBondFaceCurrency_StaleCalendarIgnored(t *testing.T) {
	m := &mockBondCalendarClient{future: futureEvents(coupon("¥"))}
	client := faceClient(m)
	client.calendars.entries = map[string]calendarCacheEntry{
		bondEventsCalendar + "|RU000A1087C3@MISX": {
			value:    []models.BondEvent{{Currency: "USD"}},
			loadedAt: time.Now().Add(-2 * calendarCacheTTL),
		},
	}

	got, err := client.GetBondFaceCurrency("RU000A1087C3@MISX")
	if err != nil || got != "CNY" {
		t.Fatalf("GetBondFaceCurrency = %q, %v; want CNY, nil", got, err)
	}
	if n := m.futureCalls.Load(); n != 1 {
		t.Errorf("future calendar asked %d times, want 1", n)
	}
}

// TestGetBondFaceCurrency_OneRequestThenFree: one future-calendar request, and
// the answer holds for the session.
func TestGetBondFaceCurrency_OneRequestThenFree(t *testing.T) {
	m := &mockBondCalendarClient{future: futureEvents(coupon("$"))}
	client := faceClient(m)

	for i := 0; i < 3; i++ {
		got, err := client.GetBondFaceCurrency("RU000A10A851@MISX")
		if err != nil || got != "USD" {
			t.Fatalf("call %d: GetBondFaceCurrency = %q, %v; want USD, nil", i, got, err)
		}
	}
	if n := m.futureCalls.Load(); n != 1 {
		t.Errorf("future calendar asked %d times, want 1", n)
	}
	if n := m.pastCalls.Load(); n != 0 {
		t.Errorf("past calendar asked %d times, want 0 when the future one answered", n)
	}
}

// TestGetBondFaceCurrency_FallsBackToPast: a bond with nothing ahead of it
// still has a history that names its currency. The past request carries no
// interval — the endpoint refuses a date_to of today.
func TestGetBondFaceCurrency_FallsBackToPast(t *testing.T) {
	m := &mockBondCalendarClient{future: futureEvents(), past: pastEvents(coupon("¥"))}
	client := faceClient(m)

	got, err := client.GetBondFaceCurrency("RU000A10DQA8@MISX")
	if err != nil || got != "CNY" {
		t.Fatalf("GetBondFaceCurrency = %q, %v; want CNY, nil", got, err)
	}
	if m.futureCalls.Load() != 1 || m.pastCalls.Load() != 1 {
		t.Errorf("calls: future=%d past=%d, want 1 and 1", m.futureCalls.Load(), m.pastCalls.Load())
	}
	if req := m.lastPast.Load(); req == nil || req.GetDateTo() != nil || req.GetDateFrom() != nil {
		t.Errorf("past request = %+v, want no interval", req)
	}
}

// TestGetBondFaceCurrency_UnnamedCurrencyIsAnAnswer: a calendar whose events
// name no currency the table knows — or no events at all — is an answer, "" is
// cached, and the bond is not asked about again this session.
func TestGetBondFaceCurrency_UnnamedCurrencyIsAnAnswer(t *testing.T) {
	tests := []struct {
		name string
		m    *mockBondCalendarClient
		want int64
	}{
		{"unknown symbol", &mockBondCalendarClient{future: futureEvents(coupon("£"))}, 1},
		{"no currency", &mockBondCalendarClient{future: futureEvents(coupon(""))}, 1},
		{"no events anywhere", &mockBondCalendarClient{}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := faceClient(tt.m)
			for i := 0; i < 2; i++ {
				got, err := client.GetBondFaceCurrency("X@MISX")
				if err != nil || got != "" {
					t.Fatalf("call %d: GetBondFaceCurrency = %q, %v; want \"\", nil", i, got, err)
				}
			}
			if n := tt.m.calls(); n != tt.want {
				t.Errorf("requests = %d, want %d (the second lookup must be free)", n, tt.want)
			}
		})
	}
}

// TestGetBondFaceCurrency_FailureNotCached: a failed lookup is retried on the
// next call, and a rate limit is recognisable to the caller.
func TestGetBondFaceCurrency_FailureNotCached(t *testing.T) {
	failing := true
	m := &mockBondCalendarClient{future: func(*corporateactions.GetFutureBondsEventsRequest) (*corporateactions.GetFutureBondsEventsResponse, error) {
		if failing {
			return nil, status.Error(codes.ResourceExhausted, "limit")
		}
		return &corporateactions.GetFutureBondsEventsResponse{Events: []*corporateactions.BondEvent{coupon("€")}}, nil
	}}
	client := faceClient(m)

	_, err := client.GetBondFaceCurrency("RU000A10A836@MISX")
	if err == nil || !IsRateLimited(err) {
		t.Fatalf("GetBondFaceCurrency error = %v, want a recognisable rate limit", err)
	}

	failing = false
	got, err := client.GetBondFaceCurrency("RU000A10A836@MISX")
	if err != nil || got != "EUR" {
		t.Fatalf("retry: GetBondFaceCurrency = %q, %v; want EUR, nil", got, err)
	}
	if n := m.futureCalls.Load(); n != 2 {
		t.Errorf("future calendar asked %d times, want 2 (failure not cached)", n)
	}
}

// TestGetBondFaceCurrency_PastFailureNotCached: the fallback request failing is
// a failure too.
func TestGetBondFaceCurrency_PastFailureNotCached(t *testing.T) {
	m := &mockBondCalendarClient{
		future: futureEvents(),
		past: func(*corporateactions.GetPastBondsEventsRequest) (*corporateactions.GetPastBondsEventsResponse, error) {
			return nil, status.Error(codes.DeadlineExceeded, "slow")
		},
	}
	client := faceClient(m)

	if _, err := client.GetBondFaceCurrency("X@MISX"); err == nil {
		t.Fatal("GetBondFaceCurrency succeeded although the past calendar failed")
	}
	if _, err := client.GetBondFaceCurrency("X@MISX"); err == nil {
		t.Fatal("second call succeeded — the failure must be retried, not remembered")
	}
	if n := m.calls(); n != 4 {
		t.Errorf("requests = %d, want 4 (two attempts of two requests)", n)
	}
}

// TestGetBondFaceCurrency_EmptySymbol answers nothing without asking.
func TestGetBondFaceCurrency_EmptySymbol(t *testing.T) {
	m := &mockBondCalendarClient{}
	client := faceClient(m)

	got, err := client.GetBondFaceCurrency("")
	if err != nil || got != "" {
		t.Errorf("GetBondFaceCurrency(\"\") = %q, %v; want \"\", nil", got, err)
	}
	if n := m.calls(); n != 0 {
		t.Errorf("an empty symbol cost %d requests, want 0", n)
	}
}

// TestBondFaceCurrencyCached reads the session answer without ever asking.
func TestBondFaceCurrencyCached(t *testing.T) {
	m := &mockBondCalendarClient{future: futureEvents(coupon("$"))}
	client := faceClient(m)

	if _, ok := client.BondFaceCurrencyCached("RU000A10A851@MISX"); ok {
		t.Fatal("a bond never looked up reported a cached face currency")
	}
	if _, err := client.GetBondFaceCurrency("RU000A10A851@MISX"); err != nil {
		t.Fatalf("GetBondFaceCurrency: %v", err)
	}
	got, ok := client.BondFaceCurrencyCached("RU000A10A851@MISX")
	if !ok || got != "USD" {
		t.Errorf("BondFaceCurrencyCached = %q, %v; want USD, true", got, ok)
	}

	// A calendar another screen loaded is read too — still without a request.
	client.calendars.mu.Lock()
	if client.calendars.entries == nil {
		client.calendars.entries = make(map[string]calendarCacheEntry)
	}
	client.calendars.entries[bondEventsCalendar+"|RU000A1087C3@MISX"] = calendarCacheEntry{
		value:    []models.BondEvent{{Currency: "CNY"}},
		loadedAt: time.Now(),
	}
	client.calendars.mu.Unlock()
	got, ok = client.BondFaceCurrencyCached("RU000A1087C3@MISX")
	if !ok || got != "CNY" {
		t.Errorf("BondFaceCurrencyCached from the day calendar = %q, %v; want CNY, true", got, ok)
	}

	if n := m.calls(); n != 1 {
		t.Errorf("requests = %d, want the one lookup", n)
	}
}
