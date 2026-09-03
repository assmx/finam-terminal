// Package analytics holds the portfolio calculations behind the Analytics tab.
//
// Everything here is a pure function over models types and plain Go values: no
// I/O, no tview, no imports of api or ui. That is deliberate — the numbers on
// the screen are the part worth testing, and testing them must not require a
// gRPC connection or a terminal.
//
// Two rules hold across the package. Nothing panics on empty or malformed
// broker data, and no result is ever NaN or Inf: a value that cannot be
// computed is reported through a flag instead, so the renderer can print "Н/Д"
// rather than a number nobody should act on.
package analytics

import (
	"math"
	"strconv"
	"strings"
)

// ParseNumber reads one of the string numbers the broker sends in models.
//
// It accepts a comma as the decimal separator, which the API does use, and
// rejects everything that is not a finite number — the "N/A" placeholder the
// existing models carry, blanks, and the NaN/Inf spellings strconv would
// otherwise accept and let poison every sum downstream. The bool is false in
// all those cases; callers count the rejects rather than substituting zero,
// because a zero would silently understate a portfolio.
func ParseNumber(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "N/A" {
		return 0, false
	}

	v, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// SafeShare returns part/whole, or 0 when the ratio would be meaningless.
//
// A zero or negative base is not an arithmetic edge case to be papered over:
// it means the portfolio has no value to take a share of, and the caller
// reports that state rather than drawing a bar.
func SafeShare(part, whole float64) float64 {
	if whole <= 0 {
		return 0
	}
	share := part / whole
	if math.IsNaN(share) || math.IsInf(share, 0) {
		return 0
	}
	return share
}

// PositionValue returns the market value of a position, signed: a short
// position answers negative, matching the Positions tab's Value column.
// Callers that need exposure rather than direction — allocation shares,
// leverage — take the absolute value themselves.
//
// last is the live quote's last price and brokerPrice is GetAccount's own
// current_price. The live price wins; the broker's valuation is the fallback
// so a position whose quote has not arrived yet still shows a number. When
// neither is readable the bool is false and the caller counts the position as
// unpriced.
//
// faceValue switches the formula for bonds quoted as a percent of par:
// price/100 × faceValue × quantity. It is 0 at every call site today, which
// keeps the plain price × quantity form — the reconnaissance could not observe
// a real bond position, so the percent-of-par format stays unconfirmed. Both
// screens go through this one function, so turning it on is a single change
// that cannot make them disagree.
func PositionValue(quantity, last, brokerPrice string, faceValue float64) (float64, bool) {
	qty, ok := ParseNumber(quantity)
	if !ok {
		return 0, false
	}

	price, ok := ParseNumber(last)
	if !ok {
		if price, ok = ParseNumber(brokerPrice); !ok {
			return 0, false
		}
	}

	if faceValue > 0 {
		price = price / 100 * faceValue
	}

	value := qty * price
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}
