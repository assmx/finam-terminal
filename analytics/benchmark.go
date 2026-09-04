package analytics

import (
	"math"
	"time"

	"finam-terminal/models"
)

// Benchmark is the index's own result over the same horizon as the account's.
//
// The index is a price index: it excludes the dividends its constituents pay,
// while the account's result includes everything it received. The screen says
// so, because comparing the two without that note flatters the index's rival by
// a couple of points a year.
type Benchmark struct {
	FirstClose float64
	LastClose  float64
	FirstAt    time.Time
	LastAt     time.Time

	// Cumulative is the whole-period change, last/first − 1.
	Cumulative float64

	// Annual is the compound annual rate. Invalid over a horizon shorter than
	// 30 days, where annualising would be arithmetic rather than information.
	Annual      float64
	AnnualValid bool
}

// DifferencePP is how far the account's annual rate is above the index's, in
// percentage points. Positive means the account won.
func (b Benchmark) DifferencePP(accountAnnual float64) float64 {
	return (accountAnnual - b.Annual) * 100
}

// BenchmarkFromBars derives the index result from two narrow windows of daily
// bars: one at the start of the horizon and one at its end.
//
// Two windows rather than one long request, because the daily timeframe refuses
// any interval wider than 366 days and the horizon is routinely longer. Each
// window is deliberately wider than the day it is aiming at — a seven-day
// window over the Russian New Year holidays comes back empty — so bars before
// the horizon are skipped rather than taken as its opening price.
//
// The bool is false when either end is missing or unusable. A horizon older
// than the index itself is the ordinary case for that, and the screen says
// "нет данных" rather than showing a made-up comparison.
func BenchmarkFromBars(firstWindow, lastWindow []models.Bar, from, to time.Time) (Benchmark, bool) {
	first, ok := firstUsableBar(firstWindow, from)
	if !ok {
		return Benchmark{}, false
	}
	last, ok := lastUsableBar(lastWindow, to)
	if !ok {
		return Benchmark{}, false
	}

	if first.Close <= 0 || last.Close <= 0 {
		return Benchmark{}, false
	}
	if last.Timestamp.Before(first.Timestamp) {
		return Benchmark{}, false
	}

	b := Benchmark{
		FirstClose: first.Close,
		LastClose:  last.Close,
		FirstAt:    first.Timestamp,
		LastAt:     last.Timestamp,
		Cumulative: last.Close/first.Close - 1,
	}
	if math.IsNaN(b.Cumulative) || math.IsInf(b.Cumulative, 0) {
		return Benchmark{}, false
	}

	days := last.Timestamp.Sub(first.Timestamp).Hours() / 24
	if days >= minAnnualisableDays {
		annual := math.Pow(last.Close/first.Close, 365/days) - 1
		if !math.IsNaN(annual) && !math.IsInf(annual, 0) {
			b.Annual, b.AnnualValid = annual, true
		}
	}

	return b, true
}

// firstUsableBar is the earliest bar at or after the horizon's start.
func firstUsableBar(bars []models.Bar, from time.Time) (models.Bar, bool) {
	var best models.Bar
	found := false
	for _, b := range bars {
		if !usableBar(b) || b.Timestamp.Before(from) {
			continue
		}
		if !found || b.Timestamp.Before(best.Timestamp) {
			best, found = b, true
		}
	}
	return best, found
}

// lastUsableBar is the latest bar at or before the horizon's end.
func lastUsableBar(bars []models.Bar, to time.Time) (models.Bar, bool) {
	var best models.Bar
	found := false
	for _, b := range bars {
		if !usableBar(b) || b.Timestamp.After(to) {
			continue
		}
		if !found || b.Timestamp.After(best.Timestamp) {
			best, found = b, true
		}
	}
	return best, found
}

// usableBar rejects what cannot be part of a ratio: an undated bar, and a close
// that is not a finite positive number.
func usableBar(b models.Bar) bool {
	return !b.Timestamp.IsZero() && b.Close > 0 &&
		!math.IsNaN(b.Close) && !math.IsInf(b.Close, 0)
}
