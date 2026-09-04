package api

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/type/date"
)

// errTest stands in for any load failure.
var errTest = errors.New("calendar unavailable")

// TestDateValue turns a protobuf date into the raw time the payout screen
// sorts and filters by, without reparsing the formatted string.
func TestDateValue(t *testing.T) {
	cases := []struct {
		name string
		in   *date.Date
		want time.Time
	}{
		{"nil", nil, time.Time{}},
		{"zero fields", &date.Date{}, time.Time{}},
		{"a real date", &date.Date{Year: 2026, Month: 5, Day: 12},
			time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)},
		{"no day", &date.Date{Year: 2026, Month: 5}, time.Time{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := dateValue(c.in)
			if !got.Equal(c.want) {
				t.Errorf("dateValue(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestCalendarCache_HitAndExpiry covers the whole cache contract in one place:
// a stored value is served without touching the loader, an expired one is
// reloaded, and a failed load is not stored.
func TestCalendarCache_HitAndExpiry(t *testing.T) {
	c := &Client{}

	loads := 0

	// A miss loads.
	v, err := calendarCached(c, "dividends", "SBER@MISX", func() ([]int, error) {
		loads++
		return []int{1, 2, 3}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(v) != 3 || loads != 1 {
		t.Fatalf("first call: got %v after %d loads, want 3 values after 1 load", v, loads)
	}

	// A hit does not.
	v, err = calendarCached(c, "dividends", "SBER@MISX", func() ([]int, error) {
		loads++
		return []int{9}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(v) != 3 || loads != 1 {
		t.Errorf("second call: got %v after %d loads, want the cached 3 values and no new load", v, loads)
	}

	// A different symbol is a different entry.
	if _, err := calendarCached(c, "dividends", "GAZP@MISX", func() ([]int, error) {
		loads++
		return []int{7}, nil
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loads != 2 {
		t.Errorf("loads = %d, want 2 — another symbol must not hit the first one's entry", loads)
	}

	// A different calendar for the same symbol is also a different entry.
	if _, err := calendarCached(c, "splits", "SBER@MISX", func() ([]int, error) {
		loads++
		return []int{5}, nil
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loads != 3 {
		t.Errorf("loads = %d, want 3 — a different calendar must not hit the dividends entry", loads)
	}
}

// TestCalendarCache_EmptyIsCached stores an empty answer. An instrument with no
// dividends is a fact, and re-asking every refresh would spend two requests to
// learn nothing.
func TestCalendarCache_EmptyIsCached(t *testing.T) {
	c := &Client{}

	loads := 0
	for range 2 {
		if _, err := calendarCached(c, "dividends", "GAZP@MISX", func() ([]int, error) {
			loads++
			return nil, nil
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if loads != 1 {
		t.Errorf("loads = %d, want 1 — an empty answer is an answer", loads)
	}
}

// TestCalendarCache_ErrorNotCached retries after a failure. Caching a failure
// for a day would turn one bad moment into a day without a calendar.
func TestCalendarCache_ErrorNotCached(t *testing.T) {
	c := &Client{}

	loads := 0
	if _, err := calendarCached(c, "dividends", "SBER@MISX", func() ([]int, error) {
		loads++
		return nil, errTest
	}); err == nil {
		t.Fatal("expected the load error to surface")
	}

	v, err := calendarCached(c, "dividends", "SBER@MISX", func() ([]int, error) {
		loads++
		return []int{1}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loads != 2 || len(v) != 1 {
		t.Errorf("loads = %d, values = %v; want the failure to be retried", loads, v)
	}
}

// TestCalendarCache_Expires reloads once the entry is older than the TTL.
func TestCalendarCache_Expires(t *testing.T) {
	prev := calendarCacheTTL
	calendarCacheTTL = 0
	t.Cleanup(func() { calendarCacheTTL = prev })

	c := &Client{}
	loads := 0
	for range 2 {
		if _, err := calendarCached(c, "dividends", "SBER@MISX", func() ([]int, error) {
			loads++
			return []int{1}, nil
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if loads != 2 {
		t.Errorf("loads = %d, want 2 — an expired entry must be reloaded", loads)
	}
}
