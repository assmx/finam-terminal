package analytics

import (
	"math"
	"sort"
	"time"

	"finam-terminal/models"
)

// XIRR search parameters. The bracket is wide enough for any account a private
// investor will produce and deliberately excludes −100%: a total loss has no
// finite annual rate, and reporting the boundary would be a number the reader
// would act on.
const (
	xirrLow        = -0.99
	xirrHigh       = 10.0
	xirrTolerance  = 1e-7
	xirrIterations = 200

	// minAnnualisableDays is the shortest horizon worth expressing as an
	// annual rate. Three weeks of return extrapolated to a year is arithmetic,
	// not information.
	minAnnualisableDays = 30
)

// XIRRStatus says whether an annual rate could be computed, and if not, why —
// so the screen can name the reason rather than printing a bare "Н/Д".
type XIRRStatus int

const (
	// XIRROK means the rate is usable.
	XIRROK XIRRStatus = iota
	// XIRRShortHorizon means the period is too short to annualise.
	XIRRShortHorizon
	// XIRRNoSignChange means the flows never reverse direction, so no rate
	// makes them sum to zero.
	XIRRNoSignChange
	// XIRRNoRoot means no rate inside the search bracket balances the flows.
	XIRRNoRoot

	xirrStatusCount
)

var xirrStatusLabels = [xirrStatusCount]string{
	"",
	"период короче 30 дней",
	"нет ввода и вывода",
	"не удалось вычислить",
}

// Label is the explanation shown beside "Н/Д".
func (s XIRRStatus) Label() string {
	if s < 0 || s >= xirrStatusCount {
		return xirrStatusLabels[XIRRNoRoot]
	}
	if s == XIRROK {
		return "рассчитано"
	}
	return xirrStatusLabels[s]
}

// Flow is one dated cash movement, from the investor's point of view: money put
// into the account is negative, money taken out is positive, and the account's
// current value counts as a final positive flow.
type Flow struct {
	At     time.Time
	Amount float64
}

// XIRR is the annual rate that makes the flows sum to zero in present value —
// the money-weighted return, which is the honest one when deposits and
// withdrawals happen at times the investor chose.
//
// It is found by bisection rather than Newton's method: bisection cannot
// diverge or land on a second root, and 200 halvings of the bracket reach the
// tolerance with room to spare. Time is measured ACT/365 from the earliest
// flow.
func XIRR(flows []Flow) (float64, XIRRStatus) {
	if len(flows) < 2 {
		return 0, XIRRNoSignChange
	}

	var positive, negative bool
	for _, f := range flows {
		switch {
		case f.Amount > 0:
			positive = true
		case f.Amount < 0:
			negative = true
		}
	}
	if !positive || !negative {
		return 0, XIRRNoSignChange
	}

	sorted := make([]Flow, len(flows))
	copy(sorted, flows)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })

	base := sorted[0].At
	span := sorted[len(sorted)-1].At.Sub(base)
	if span <= 0 {
		// Every flow at one instant: there is no time for a rate to act over.
		return 0, XIRRNoRoot
	}

	npv := func(rate float64) float64 {
		var sum float64
		for _, f := range sorted {
			t := f.At.Sub(base).Hours() / 24 / 365
			sum += f.Amount / math.Pow(1+rate, t)
		}
		return sum
	}

	low, high := xirrLow, xirrHigh
	fLow, fHigh := npv(low), npv(high)
	if math.IsNaN(fLow) || math.IsNaN(fHigh) || fLow*fHigh > 0 {
		return 0, XIRRNoRoot
	}

	for range xirrIterations {
		mid := (low + high) / 2
		fMid := npv(mid)
		if math.Abs(fMid) < xirrTolerance || (high-low)/2 < xirrTolerance {
			return mid, XIRROK
		}
		if fLow*fMid < 0 {
			high = mid
		} else {
			low, fLow = mid, fMid
		}
	}

	rate := (low + high) / 2
	if math.IsNaN(rate) || math.IsInf(rate, 0) {
		return 0, XIRRNoRoot
	}
	return rate, XIRROK
}

// SinceOpen is the account's result over its whole life.
//
// It is the one horizon on which a return can be computed honestly: the API
// carries no equity history, so a return over an arbitrary month would need an
// unrealised result at the month's start that nobody has. Here the two ends are
// known — nothing was in the account before it opened, and its value now is
// reported every five seconds.
type SinceOpen struct {
	From time.Time
	Days int

	// Currency is the base currency everything below is measured in.
	Currency string

	NetDeposit float64 // deposits minus withdrawals
	Result     float64 // equity minus net deposit

	// SimpleReturn is Result over NetDeposit. Invalid when the net deposit is
	// not positive: a share of nothing, or of a negative, is not a percentage.
	SimpleReturn float64
	SimpleValid  bool

	// XIRR is the money-weighted annual return. XIRRStatus says why it is
	// missing when it is.
	XIRR       float64
	XIRRStatus XIRRStatus

	// Excluded holds the net deposit in every other currency, so the screen can
	// say what it left out. The Trade API has no exchange rates, so these
	// cannot be folded in.
	Excluded map[string]float64
}

// SinceOpenResult computes the block from the account's whole transaction
// history and its current equity.
//
// Only deposits and withdrawals are flows. A commission, a dividend or a
// purchase happens inside the account: it is part of the result, not part of
// what was put in. A transaction carrying a trade is excluded for the same
// reason, whatever category it arrives under.
func SinceOpenResult(transactions []models.Transaction, equity float64, baseCurrency string, from, now time.Time) SinceOpen {
	if baseCurrency == "" {
		baseCurrency = defaultBaseCurrency
	}

	out := SinceOpen{
		From:       from,
		Currency:   baseCurrency,
		Excluded:   make(map[string]float64),
		XIRRStatus: XIRRNoSignChange,
	}
	if !from.IsZero() && from.Before(now) {
		out.Days = int(now.Sub(from).Hours() / 24)
	}

	flows := make([]Flow, 0, len(transactions)+1)

	for _, t := range transactions {
		group := Classify(t)
		if group != GroupDeposit && group != GroupWithdraw {
			continue
		}

		currency := t.Currency
		if currency == "" {
			currency = baseCurrency
		}
		if currency != baseCurrency {
			out.Excluded[currency] += t.Amount
			continue
		}

		out.NetDeposit += t.Amount

		// The investor's sign is the opposite of the account's: money added to
		// the account left the investor's pocket.
		if t.Amount != 0 && !t.Timestamp.IsZero() {
			flows = append(flows, Flow{At: t.Timestamp, Amount: -t.Amount})
		}
	}

	out.Result = equity - out.NetDeposit
	if out.NetDeposit > 0 {
		out.SimpleReturn = out.Result / out.NetDeposit
		out.SimpleValid = !math.IsNaN(out.SimpleReturn) && !math.IsInf(out.SimpleReturn, 0)
	}

	if out.Days < minAnnualisableDays {
		out.XIRRStatus = XIRRShortHorizon
		return out
	}

	flows = append(flows, Flow{At: now, Amount: equity})
	rate, status := XIRR(flows)
	out.XIRR, out.XIRRStatus = rate, status
	return out
}
