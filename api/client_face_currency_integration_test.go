//go:build integration

package api

import (
	"testing"

	"finam-terminal/api/testserver"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestIntegration_BondFaceCurrency walks the face-currency lookup over bufconn:
// one future-calendar request per bond, the past calendar only when nothing is
// scheduled, nothing at all for a bond whose day calendar is already loaded,
// and nothing again for the rest of the session.
func TestIntegration_BondFaceCurrency(t *testing.T) {
	client, ts := setupTestServer(t)
	ts.CorporateActions.BondCalendars = testserver.CurrencyBondCalendars()

	// A replacement bond: "$" in the calendar, one request.
	got, err := client.GetBondFaceCurrency("RU000A10A851@MISX")
	if err != nil || got != "USD" {
		t.Fatalf("РФ ЗО 27 Д: GetBondFaceCurrency = %q, %v; want USD, nil", got, err)
	}
	if f, p := ts.CorporateActions.FutureBondEventsCallCount.Load(), ts.CorporateActions.PastBondEventsCallCount.Load(); f != 1 || p != 0 {
		t.Errorf("after the first lookup: future=%d past=%d, want 1 and 0", f, p)
	}

	// Asked again: free.
	if got, _ := client.GetBondFaceCurrency("RU000A10A851@MISX"); got != "USD" {
		t.Errorf("second lookup = %q, want USD", got)
	}
	if n := ts.CorporateActions.BondEventCalls(); n != 1 {
		t.Errorf("a repeated lookup cost requests: %d in total, want 1", n)
	}

	// Nothing scheduled: the past calendar answers, without an interval the
	// endpoint would refuse.
	got, err = client.GetBondFaceCurrency("RU000A10DQA8@MISX")
	if err != nil || got != "CNY" {
		t.Fatalf("ОФЗ 33 CNY: GetBondFaceCurrency = %q, %v; want CNY, nil", got, err)
	}
	if n := ts.CorporateActions.BondEventCalls(); n != 3 {
		t.Errorf("after the fallback: %d requests in total, want 3", n)
	}

	// The payout screen already loaded this bond's day calendar: free.
	if _, err := client.GetBondEvents("RU000A1087C3@MISX"); err != nil {
		t.Fatalf("GetBondEvents: %v", err)
	}
	before := ts.CorporateActions.BondEventCalls()
	if cached, ok := client.BondFaceCurrencyCached("RU000A1087C3@MISX"); !ok || cached != "CNY" {
		t.Errorf("BondFaceCurrencyCached = %q, %v; want CNY, true", cached, ok)
	}
	got, err = client.GetBondFaceCurrency("RU000A1087C3@MISX")
	if err != nil || got != "CNY" {
		t.Fatalf("ГПБ3P6CNY: GetBondFaceCurrency = %q, %v; want CNY, nil", got, err)
	}
	if n := ts.CorporateActions.BondEventCalls(); n != before {
		t.Errorf("a loaded day calendar cost %d more requests, want 0", n-before)
	}
}

// TestIntegration_BondFaceCurrencyRateLimited: a refusal is reported as a rate
// limit, remembered nowhere, and the next lookup asks again.
func TestIntegration_BondFaceCurrencyRateLimited(t *testing.T) {
	client, ts := setupTestServer(t)
	ts.CorporateActions.BondCalendars = testserver.CurrencyBondCalendars()
	ts.CorporateActions.BondEventsError = status.Error(codes.ResourceExhausted, "Too many requests")

	_, err := client.GetBondFaceCurrency("RU000A10A851@MISX")
	if err == nil || !IsRateLimited(err) {
		t.Fatalf("GetBondFaceCurrency error = %v, want a rate limit", err)
	}
	if _, ok := client.BondFaceCurrencyCached("RU000A10A851@MISX"); ok {
		t.Error("a refused lookup left an answer behind")
	}

	ts.CorporateActions.BondEventsError = nil
	got, err := client.GetBondFaceCurrency("RU000A10A851@MISX")
	if err != nil || got != "USD" {
		t.Fatalf("retry: GetBondFaceCurrency = %q, %v; want USD, nil", got, err)
	}
}
