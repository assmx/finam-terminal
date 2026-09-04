package api

import (
	"sync"
	"time"

	"google.golang.org/genproto/googleapis/type/date"
)

// calendarCacheTTL is how long a corporate-action calendar is reused.
//
// A day is generous because these calendars barely move: a dividend record date
// announced this morning is the same date tomorrow. The Payouts screen asks for
// two calls per position, so without the cache a refresh on a twenty-position
// account would spend forty requests to learn nothing new. A variable rather
// than a constant so tests can expire it.
var calendarCacheTTL = 24 * time.Hour

// calendarCacheEntry is one cached calendar. The value is held as any because
// the three calendars carry different element types and share this one store.
type calendarCacheEntry struct {
	value    any
	loadedAt time.Time
}

// calendarCache is the per-client store, keyed by calendar kind and symbol.
type calendarCache struct {
	mu      sync.Mutex
	entries map[string]calendarCacheEntry
}

// calendarCached returns the cached calendar for a symbol, loading it when the
// entry is missing or older than the TTL.
//
// A failed load is deliberately not stored: caching a failure for a day would
// turn one bad moment into a day without a calendar. An empty successful answer
// is stored, because "this instrument pays no dividends" is an answer and
// re-asking it every refresh costs two requests to learn nothing.
//
// It is a free function rather than a method because Go methods cannot take
// type parameters, and the three calendars carry different element types.
func calendarCached[T any](c *Client, kind, symbol string, load func() ([]T, error)) ([]T, error) {
	key := kind + "|" + symbol

	c.calendars.mu.Lock()
	entry, ok := c.calendars.entries[key]
	c.calendars.mu.Unlock()

	if ok && time.Since(entry.loadedAt) < calendarCacheTTL {
		if v, sameType := entry.value.([]T); sameType {
			return v, nil
		}
	}

	value, err := load()
	if err != nil {
		return nil, err
	}

	c.calendars.mu.Lock()
	if c.calendars.entries == nil {
		c.calendars.entries = make(map[string]calendarCacheEntry)
	}
	c.calendars.entries[key] = calendarCacheEntry{value: value, loadedAt: time.Now()}
	c.calendars.mu.Unlock()

	return value, nil
}

// dateValue converts a protobuf date into the instant the payout screen sorts
// and filters by, so nothing has to reparse the formatted string.
//
// An absent or incomplete date answers the zero time rather than a plausible
// one: a record the API dated 0000-00-00 must be recognisable as undated, and
// year 1 is not a date anybody will mistake for real.
func dateValue(d *date.Date) time.Time {
	if d == nil || d.GetYear() == 0 || d.GetMonth() == 0 || d.GetDay() == 0 {
		return time.Time{}
	}
	return time.Date(int(d.GetYear()), time.Month(d.GetMonth()), int(d.GetDay()), 0, 0, 0, 0, time.UTC)
}
