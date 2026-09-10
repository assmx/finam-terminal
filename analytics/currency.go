package analytics

import (
	"math"
	"sort"
	"strings"
	"time"

	"finam-terminal/models"
)

const (
	// FXRateStaleAfter is the age past which a rate is shown with its date as
	// well as its time, marked as not current. The MOEX currency section closes
	// between 17:30 and 19:00 (observed 2026-09-10) and the broker keeps valuing
	// at the last trade all evening, so twelve hours keep the evening quiet
	// while the weekend and the morning before the first trade are marked.
	FXRateStaleAfter = 12 * time.Hour

	// faceSameCurrencyMin and faceSameCurrencyMax bound the ratio of a bond's
	// per-piece value (GetAssetParams, in its settlement currency) to its price
	// × face / 100, inside which the face is taken to be in the quote currency.
	// Inside the band the difference is accrued interest — 1.01 to 1.04 on the
	// bonds observed — plus the price drift since the GetAssetParams call.
	// Outside it the face is in another currency than the bond settles in:
	// ≈13 for a yuan face settled in roubles, ≈85 for a dollar one.
	faceSameCurrencyMin = 0.8
	faceSameCurrencyMax = 1.3
)

// Instrument is what the terminal knows about one instrument's money, all of it
// read from caches filled by requests the terminal already makes.
type Instrument struct {
	// Quote is the settlement currency (GetAsset.quote_currency); "" when the
	// broker named none or has not been asked yet.
	Quote string

	// FaceValue is a bond's face value; 0 for anything else. A bond's price is a
	// percentage of it.
	FaceValue float64

	// Unit is the value of one piece in its settlement currency, derived from
	// GetAssetParams; a zero Value means unknown.
	Unit models.UnitValue

	// Face is a bond's face currency as its calendar names it, and FaceChecked
	// says the calendar was read — so Face "" with FaceChecked true is a
	// calendar that named nothing, not a lookup still to be made.
	Face        string
	FaceChecked bool
}

// CurrencyState says how sure a position's currency is.
type CurrencyState int

const (
	// CurrencyKnown: the broker named the currency the value is in.
	CurrencyKnown CurrencyState = iota

	// CurrencyUnknown: GetAsset named no currency (or was not asked yet). The
	// position is counted in the base currency, as it was before currencies
	// were read at all — guessing it out would silently shrink the portfolio —
	// and flagged.
	CurrencyUnknown

	// FaceUnresolved: a bond whose face is in another currency than it settles
	// in, and which currency is not known yet. It is valued at the broker's own
	// per-piece figure in the settlement currency, which already carries the
	// conversion, so the amount is right; only its currency of risk is open.
	FaceUnresolved

	// FaceUnchecked: a bond with nothing to check its face against — no
	// per-piece value and no calendar yet. It is valued in its quote currency,
	// which is right for almost every bond, and flagged until checked.
	FaceUnchecked
)

// PositionMoney is one position valued in its own currency.
type PositionMoney struct {
	// Currency is what Value is in.
	Currency string

	// Value is signed: a short answers negative, like PositionValue.
	Value float64

	State CurrencyState

	// Valid is false when the position has no readable quantity or price, or
	// the value overflowed.
	Valid bool
}

// ValuePosition values one position in its own currency.
//
// A non-bond is in its quote currency. A bond is valued through its face —
// quantity × price × face / 100, the percent-of-face form the reconnaissance
// confirmed — in its face currency, which is decided in this order:
//
//  1. the calendar named it;
//  2. the per-piece value from GetAssetParams sits within
//     [faceSameCurrencyMin, faceSameCurrencyMax] of price × face / 100 in the
//     same currency, so the face is in the quote currency;
//  3. the per-piece value is known but far off — the face is in another
//     currency: FaceUnresolved, valued at quantity × that per-piece value;
//  4. nothing to check against: the quote currency, FaceUnchecked.
//
// A currency the broker did not name falls back to base, flagged
// CurrencyUnknown. last is the live quote's last price and wins over the
// broker's current_price, as in PositionValue.
func ValuePosition(p models.Position, last string, inst Instrument, base string) PositionMoney {
	quote := strings.ToUpper(strings.TrimSpace(inst.Quote))

	if inst.FaceValue <= 0 {
		value, ok := PositionValue(p.Quantity, last, p.CurrentPrice, 0)
		if quote == "" {
			return PositionMoney{Currency: base, Value: value, State: CurrencyUnknown, Valid: ok}
		}
		return PositionMoney{Currency: quote, Value: value, State: CurrencyKnown, Valid: ok}
	}

	value, ok := PositionValue(p.Quantity, last, p.CurrentPrice, inst.FaceValue)

	if face := strings.ToUpper(strings.TrimSpace(inst.Face)); face != "" {
		return PositionMoney{Currency: face, Value: value, State: CurrencyKnown, Valid: ok}
	}

	switch faceCheck(p, last, inst) {
	case faceInQuote:
		return PositionMoney{Currency: settlementCurrency(inst, base), Value: value, State: CurrencyKnown, Valid: ok}
	case faceElsewhere:
		qty, qtyOK := ParseNumber(p.Quantity)
		unitValue := qty * inst.Unit.Value
		valid := qtyOK && !math.IsNaN(unitValue) && !math.IsInf(unitValue, 0)
		if !valid {
			unitValue = 0
		}
		return PositionMoney{Currency: strings.ToUpper(inst.Unit.Currency), Value: unitValue, State: FaceUnresolved, Valid: valid}
	default:
		currency := quote
		if currency == "" {
			currency = base
		}
		return PositionMoney{Currency: currency, Value: value, State: FaceUnchecked, Valid: ok}
	}
}

// NeedsFaceCurrency reports whether a bond's face currency has to be looked up
// in its calendar: it is a bond, the calendar has not been read, and what is
// already cached does not show the face to be in the quote currency. An
// ordinary rouble or yuan bond answers false and costs no request.
func NeedsFaceCurrency(p models.Position, last string, inst Instrument) bool {
	if inst.FaceValue <= 0 || inst.FaceChecked {
		return false
	}
	return faceCheck(p, last, inst) != faceInQuote
}

// RatesToFetch lists the rouble rates a portfolio needs to be measured in its
// base currency, sorted: one for each currency held — a position valued in it,
// or a positive cash line — that is neither the rouble nor the base, plus the
// base's own when the base is not the rouble, since a cross rate needs both
// legs. A rouble-only account needs none, and so costs no request. Whether a
// currency has a pair to ask for is the API layer's business; this lists what
// the portfolio holds.
func RatesToFetch(in StructureInput) []string {
	base := strings.ToUpper(baseCurrency(in.Cash))

	held := make(map[string]bool)
	for _, p := range in.Positions {
		money := ValuePosition(p, quoteLast(in.Quotes, p.Symbol), lookupInstrument(in.Instruments, p), base)
		if money.Valid && money.Value != 0 {
			held[strings.ToUpper(money.Currency)] = true
		}
	}
	for _, c := range in.Cash {
		if code := strings.ToUpper(strings.TrimSpace(c.Currency)); c.Amount > 0 && code != "" {
			held[code] = true
		}
	}

	need := make(map[string]bool)
	for code := range held {
		if code == base {
			continue
		}
		if code != defaultBaseCurrency {
			need[code] = true
		}
		if base != defaultBaseCurrency {
			need[base] = true
		}
	}
	return sortedKeys(need)
}

// BondsNeedingFace lists, sorted, the held bonds whose face currency only a
// calendar request can settle (see NeedsFaceCurrency). Ordinary rouble and
// yuan bonds are not on it, so a portfolio of them costs no calendar request.
func BondsNeedingFace(in StructureInput) []string {
	need := make(map[string]bool)
	for _, p := range in.Positions {
		if qty, ok := ParseNumber(p.Quantity); !ok || qty == 0 {
			continue
		}
		if NeedsFaceCurrency(p, quoteLast(in.Quotes, p.Symbol), lookupInstrument(in.Instruments, p)) {
			need[p.Symbol] = true
		}
	}
	return sortedKeys(need)
}

// sortedKeys returns a set's members in order, or nil for an empty set.
func sortedKeys(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// faceVerdict is what the per-piece value says about a bond's face.
type faceVerdict int

const (
	faceUncheckable faceVerdict = iota // nothing to compare
	faceInQuote                        // within the band: face in the quote currency
	faceElsewhere                      // far off: face in another currency
)

// faceCheck compares a bond's per-piece value with price × face / 100.
func faceCheck(p models.Position, last string, inst Instrument) faceVerdict {
	unit := inst.Unit
	if unit.Currency == "" || !(unit.Value > 0) || math.IsInf(unit.Value, 0) {
		return faceUncheckable
	}

	quote := strings.TrimSpace(inst.Quote)
	if quote != "" && !strings.EqualFold(quote, unit.Currency) {
		// The per-piece value is in a currency the price is not: it cannot be
		// compared, but it is still what one piece is worth.
		return faceElsewhere
	}

	price, ok := ParseNumber(last)
	if !ok {
		if price, ok = ParseNumber(p.CurrentPrice); !ok {
			return faceUncheckable
		}
	}
	clean := price / 100 * inst.FaceValue
	if !(clean > 0) || math.IsInf(clean, 0) {
		return faceUncheckable
	}

	ratio := unit.Value / clean
	if ratio >= faceSameCurrencyMin && ratio <= faceSameCurrencyMax {
		return faceInQuote
	}
	return faceElsewhere
}

// settlementCurrency is the currency a bond settles in: its quote currency, or
// — when GetAsset named none — the currency of its margin, or the base.
func settlementCurrency(inst Instrument, base string) string {
	if quote := strings.ToUpper(strings.TrimSpace(inst.Quote)); quote != "" {
		return quote
	}
	if unit := strings.ToUpper(strings.TrimSpace(inst.Unit.Currency)); unit != "" {
		return unit
	}
	return base
}

// Rate is the price of one unit of a currency in the base currency.
type Rate struct {
	Value float64
	// At is when the rate was quoted — for a cross rate, the older of its two
	// legs — and zero for a currency against itself.
	At    time.Time
	Valid bool
}

// RateTo converts one unit of currency into base. Every pair the terminal reads
// is quoted against the rouble, so a rate to any other base is a cross through
// it: X/base = X/RUB ÷ base/RUB, and needs both legs. A currency against itself
// is 1. Anything missing or not a positive finite number is invalid.
func RateTo(currency, base string, rates map[string]models.FXRate) Rate {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	base = strings.ToUpper(strings.TrimSpace(base))
	if currency == "" || base == "" {
		return Rate{}
	}
	if currency == base {
		return Rate{Value: 1, Valid: true}
	}

	toRouble := func(code string) (float64, time.Time, bool) {
		if code == defaultBaseCurrency {
			return 1, time.Time{}, true
		}
		r, ok := rates[code]
		if !ok || !(r.Rate > 0) || math.IsInf(r.Rate, 0) {
			return 0, time.Time{}, false
		}
		return r.Rate, r.At, true
	}

	from, fromAt, ok := toRouble(currency)
	if !ok {
		return Rate{}
	}
	to, toAt, ok := toRouble(base)
	if !ok {
		return Rate{}
	}

	value := from / to
	if !(value > 0) || math.IsInf(value, 0) {
		return Rate{}
	}
	return Rate{Value: value, At: olderOf(fromAt, toAt), Valid: true}
}

// olderOf returns the earlier of two times, ignoring a zero one.
func olderOf(a, b time.Time) time.Time {
	switch {
	case a.IsZero():
		return b
	case b.IsZero():
		return a
	case a.Before(b):
		return a
	default:
		return b
	}
}

// RateIsStale reports whether a rate quoted at at is too old at now to be shown
// as current. A rate with no time cannot be judged and is not marked.
func RateIsStale(at, now time.Time) bool {
	return !at.IsZero() && now.Sub(at) > FXRateStaleAfter
}
