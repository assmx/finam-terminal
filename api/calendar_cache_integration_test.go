//go:build integration

package api

import (
	"errors"
	"testing"
	"time"
)

// TestCalendarCacheIntegration_SecondCallIsFree is the promise the Payouts
// screen is built on: asking again for the same symbol inside a day costs no
// requests at all.
func TestCalendarCacheIntegration_SecondCallIsFree(t *testing.T) {
	client, server := setupTestServer(t)

	first, err := client.GetDividends("SBER@TQBR")
	if err != nil {
		t.Fatalf("GetDividends failed: %v", err)
	}
	if server.CorporateActions.DividendCalls() != 2 {
		t.Fatalf("first call made %d RPCs, want 2 (past + future)", server.CorporateActions.DividendCalls())
	}

	second, err := client.GetDividends("SBER@TQBR")
	if err != nil {
		t.Fatalf("second GetDividends failed: %v", err)
	}
	if server.CorporateActions.DividendCalls() != 2 {
		t.Errorf("second call made %d RPCs in total, want the original 2",
			server.CorporateActions.DividendCalls())
	}
	if len(second) != len(first) {
		t.Errorf("cached answer has %d entries, the first had %d", len(second), len(first))
	}

	// Another symbol is another entry and does cost requests.
	if _, err := client.GetDividends("GAZP@TQBR"); err != nil {
		t.Fatalf("GetDividends for a second symbol failed: %v", err)
	}
	if server.CorporateActions.DividendCalls() != 4 {
		t.Errorf("after a second symbol the total is %d RPCs, want 4",
			server.CorporateActions.DividendCalls())
	}
}

// TestCalendarCacheIntegration_SplitsAndBondEvents checks the other two
// calendars cache independently of the dividends one.
func TestCalendarCacheIntegration_SplitsAndBondEvents(t *testing.T) {
	client, server := setupTestServer(t)

	for range 2 {
		if _, err := client.GetSplits("SBER@TQBR"); err != nil {
			t.Fatalf("GetSplits failed: %v", err)
		}
		if _, err := client.GetBondEvents("SU26238@TQOB"); err != nil {
			t.Fatalf("GetBondEvents failed: %v", err)
		}
	}

	if server.CorporateActions.SplitCalls() != 2 {
		t.Errorf("splits made %d RPCs, want 2", server.CorporateActions.SplitCalls())
	}
	if server.CorporateActions.BondEventCalls() != 2 {
		t.Errorf("bond events made %d RPCs, want 2", server.CorporateActions.BondEventCalls())
	}
	if server.CorporateActions.DividendCalls() != 0 {
		t.Errorf("dividends made %d RPCs; the calendars must not share an entry",
			server.CorporateActions.DividendCalls())
	}
}

// TestCalendarCacheIntegration_ErrorIsRetried keeps a failure out of the cache.
// Storing it would cost a day without a calendar for one bad moment.
func TestCalendarCacheIntegration_ErrorIsRetried(t *testing.T) {
	client, server := setupTestServer(t)
	server.CorporateActions.DividendsError = errors.New("temporary failure")

	if _, err := client.GetDividends("SBER@TQBR"); err == nil {
		t.Fatal("expected the failure to surface")
	}

	server.CorporateActions.DividendsError = nil
	divs, err := client.GetDividends("SBER@TQBR")
	if err != nil {
		t.Fatalf("the retry failed: %v", err)
	}
	if len(divs) == 0 {
		t.Error("the retry returned nothing; the failure was cached")
	}
}

// TestCalendarCacheIntegration_WhenIsPopulated proves the raw date travels
// alongside the formatted one, so the payout screen never reparses a string.
func TestCalendarCacheIntegration_WhenIsPopulated(t *testing.T) {
	client, _ := setupTestServer(t)

	divs, err := client.GetDividends("SBER@TQBR")
	if err != nil {
		t.Fatalf("GetDividends failed: %v", err)
	}
	if len(divs) == 0 {
		t.Fatal("no dividends in the fixture")
	}
	for _, d := range divs {
		if d.When.IsZero() {
			t.Errorf("dividend %s has a zero When alongside Date %q", d.Amount, d.Date)
			continue
		}
		if got := d.When.Format("2006-01-02"); got != d.Date {
			t.Errorf("When = %s but Date = %s; the two must describe the same day", got, d.Date)
		}
	}

	events, err := client.GetBondEvents("SU26238@TQOB")
	if err != nil {
		t.Fatalf("GetBondEvents failed: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no bond events in the fixture")
	}
	for _, e := range events {
		if e.When.IsZero() != (e.Date == "") {
			t.Errorf("bond event %s: When zero = %v but Date = %q", e.Kind, e.When.IsZero(), e.Date)
		}
	}

	// Ascending by date is the order the profile and the payout list both
	// depend on, and When must agree with it.
	var prev time.Time
	for _, e := range events {
		if e.When.IsZero() {
			continue
		}
		if !prev.IsZero() && e.When.Before(prev) {
			t.Errorf("bond events are not ascending: %v came after %v", e.When, prev)
		}
		prev = e.When
	}
}
