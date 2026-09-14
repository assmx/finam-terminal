package analytics

import (
	"math"
	"testing"
	"time"

	"finam-terminal/models"
)

var xirrNow = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

func years(n float64) time.Time {
	return xirrNow.Add(-time.Duration(n * 365 * 24 * float64(time.Hour)))
}

// TestXIRR_KnownRate is the case that can be checked by hand: 100 in, 121 out
// two years later is exactly 10% a year.
func TestXIRR_KnownRate(t *testing.T) {
	rate, status := XIRR([]Flow{
		{At: years(2), Amount: -100},
		{At: xirrNow, Amount: 121},
	})
	if status != XIRROK {
		t.Fatalf("status = %s, want a computed rate", status.Label())
	}
	if math.Abs(rate-0.10) > 1e-6 {
		t.Errorf("rate = %.10f, want 0.10", rate)
	}
}

// TestXIRR_Doubling: 100 in, 200 out one year later is 100% a year.
func TestXIRR_Doubling(t *testing.T) {
	rate, status := XIRR([]Flow{
		{At: years(1), Amount: -100},
		{At: xirrNow, Amount: 200},
	})
	if status != XIRROK {
		t.Fatalf("status = %s", status.Label())
	}
	if math.Abs(rate-1.0) > 1e-6 {
		t.Errorf("rate = %.10f, want 1.0", rate)
	}
}

// TestXIRR_Loss produces a negative rate rather than refusing.
func TestXIRR_Loss(t *testing.T) {
	rate, status := XIRR([]Flow{
		{At: years(1), Amount: -100},
		{At: xirrNow, Amount: 80},
	})
	if status != XIRROK {
		t.Fatalf("status = %s", status.Label())
	}
	if math.Abs(rate-(-0.20)) > 1e-6 {
		t.Errorf("rate = %.10f, want -0.20", rate)
	}
}

// TestXIRR_SeveralFlows checks a sequence against a value computed from the
// definition: the discounted flows must sum to zero at the rate returned.
func TestXIRR_SeveralFlows(t *testing.T) {
	flows := []Flow{
		{At: years(3), Amount: -100000},
		{At: years(2), Amount: -50000},
		{At: years(1), Amount: 30000}, // a withdrawal
		{At: xirrNow, Amount: 160000},
	}

	rate, status := XIRR(flows)
	if status != XIRROK {
		t.Fatalf("status = %s", status.Label())
	}

	// Verify against the equation itself rather than a number copied from
	// somewhere. The tolerance is on the rate, not on the present value: with
	// flows in the hundreds of thousands, a rate accurate to 1e-7 still leaves
	// a present value of a few hundredths of a rouble. So check that the root
	// is bracketed — the present value must change sign across the returned
	// rate within a millionth.
	base := flows[0].At
	npv := func(r float64) float64 {
		var sum float64
		for _, f := range flows {
			t := f.At.Sub(base).Hours() / 24 / 365
			sum += f.Amount / math.Pow(1+r, t)
		}
		return sum
	}
	if low, high := npv(rate-1e-6), npv(rate+1e-6); low*high > 0 {
		t.Errorf("rate %.10f does not bracket the root: npv(-1e-6) = %v, npv(+1e-6) = %v",
			rate, low, high)
	}
}

// TestXIRR_NoSignChange refuses when every flow points the same way. There is
// no rate that makes a stream of deposits sum to zero.
func TestXIRR_NoSignChange(t *testing.T) {
	_, status := XIRR([]Flow{
		{At: years(2), Amount: -100},
		{At: years(1), Amount: -50},
	})
	if status != XIRRNoSignChange {
		t.Errorf("status = %s, want the no-sign-change refusal", status.Label())
	}
}

// TestXIRR_Empty and a single flow both refuse.
func TestXIRR_TooFewFlows(t *testing.T) {
	if _, status := XIRR(nil); status == XIRROK {
		t.Error("an empty flow list produced a rate")
	}
	if _, status := XIRR([]Flow{{At: xirrNow, Amount: 100}}); status == XIRROK {
		t.Error("a single flow produced a rate")
	}
}

// TestXIRR_OutsideBracket refuses rather than reporting a boundary value. A
// total loss has no finite annual rate, and clamping it to −99% would be a
// number the reader would act on.
func TestXIRR_OutsideBracket(t *testing.T) {
	_, status := XIRR([]Flow{
		{At: years(1), Amount: -100000},
		{At: xirrNow, Amount: 0.0001},
	})
	if status == XIRROK {
		t.Error("a near-total loss produced a rate inside the bracket")
	}
}

// TestXIRR_ZeroSpan refuses when every flow lands on the same instant: there is
// no time for a rate to act over.
func TestXIRR_ZeroSpan(t *testing.T) {
	_, status := XIRR([]Flow{
		{At: xirrNow, Amount: -100},
		{At: xirrNow, Amount: 120},
	})
	if status == XIRROK {
		t.Error("flows at a single instant produced an annual rate")
	}
}

// TestXIRRStatusLabels: every status renders something.
func TestXIRRStatusLabels(t *testing.T) {
	for _, s := range []XIRRStatus{XIRROK, XIRRShortHorizon, XIRRNoSignChange, XIRRNoRoot} {
		if s.Label() == "" {
			t.Errorf("status %d has no label", int(s))
		}
	}
	if XIRRStatus(99).Label() == "" {
		t.Error("an unknown status rendered an empty label")
	}
}

// --- SinceOpenResult ---

func deposit(day int, amount float64, currency string) models.Transaction {
	return models.Transaction{
		ID:        "d" + itoa(day),
		Category:  "DEPOSIT",
		Timestamp: xirrNow.AddDate(0, 0, -day),
		Amount:    amount,
		Currency:  currency,
	}
}

func withdrawal(day int, amount float64, currency string) models.Transaction {
	return models.Transaction{
		ID:        "w" + itoa(day),
		Category:  "WITHDRAW",
		Timestamp: xirrNow.AddDate(0, 0, -day),
		Amount:    amount,
		Currency:  currency,
	}
}

// TestSinceOpen_Basics computes the headline figures of the since-open block.
func TestSinceOpen_Basics(t *testing.T) {
	from := xirrNow.AddDate(-2, 0, 0)

	got := SinceOpenResult([]models.Transaction{
		deposit(730, 100000, "RUB"),
		deposit(365, 50000, "RUB"),
		withdrawal(200, -30000, "RUB"),
	}, 160000, "RUB", from, xirrNow)

	if !approx(got.NetDeposit, 120000) {
		t.Errorf("NetDeposit = %v, want 120000", got.NetDeposit)
	}
	if !approx(got.Result, 40000) {
		t.Errorf("Result = %v, want 40000 (equity minus net deposit)", got.Result)
	}
	if !got.SimpleValid || !approx(got.SimpleReturn, 40000.0/120000.0) {
		t.Errorf("SimpleReturn = %v (valid %v), want 1/3", got.SimpleReturn, got.SimpleValid)
	}
	if got.XIRRStatus != XIRROK {
		t.Errorf("XIRRStatus = %s, want a computed rate", got.XIRRStatus.Label())
	}
	if got.XIRR <= 0 {
		t.Errorf("XIRR = %v, want a positive rate for a profitable account", got.XIRR)
	}
	if got.Currency != "RUB" {
		t.Errorf("Currency = %q, want RUB", got.Currency)
	}
	if got.Days < 700 {
		t.Errorf("Days = %d, want about two years", got.Days)
	}
}

// TestSinceOpen_ShortHorizon keeps the simple return and refuses the annual
// one. Annualising three weeks would produce a number nobody should act on.
func TestSinceOpen_ShortHorizon(t *testing.T) {
	from := xirrNow.AddDate(0, 0, -20)

	got := SinceOpenResult([]models.Transaction{
		deposit(20, 100000, "RUB"),
	}, 110000, "RUB", from, xirrNow)

	if !got.SimpleValid || !approx(got.SimpleReturn, 0.1) {
		t.Errorf("SimpleReturn = %v (valid %v), want 0.1", got.SimpleReturn, got.SimpleValid)
	}
	if got.XIRRStatus != XIRRShortHorizon {
		t.Errorf("XIRRStatus = %s, want the short-horizon refusal", got.XIRRStatus.Label())
	}
}

// TestSinceOpen_ZeroNetDeposit refuses the simple return: a percentage of
// nothing is not a percentage.
func TestSinceOpen_ZeroNetDeposit(t *testing.T) {
	from := xirrNow.AddDate(-1, 0, 0)

	got := SinceOpenResult([]models.Transaction{
		deposit(300, 100000, "RUB"),
		withdrawal(100, -100000, "RUB"),
	}, 5000, "RUB", from, xirrNow)

	if got.SimpleValid {
		t.Errorf("SimpleReturn = %v, want it refused for a zero net deposit", got.SimpleReturn)
	}
	if !approx(got.Result, 5000) {
		t.Errorf("Result = %v, want 5000 — the result itself is still known", got.Result)
	}
	// The flows do change sign, so the annual rate is still computable.
	if got.XIRRStatus != XIRROK {
		t.Errorf("XIRRStatus = %s, want a rate despite the zero net deposit", got.XIRRStatus.Label())
	}
}

// TestSinceOpen_NegativeNetDeposit: an account that has paid out more than it
// took in still reports its result, and refuses only the ratio.
func TestSinceOpen_NegativeNetDeposit(t *testing.T) {
	from := xirrNow.AddDate(-3, 0, 0)

	got := SinceOpenResult([]models.Transaction{
		deposit(1000, 100000, "RUB"),
		withdrawal(200, -150000, "RUB"),
	}, 20000, "RUB", from, xirrNow)

	if got.SimpleValid {
		t.Error("a negative net deposit produced a simple return")
	}
	if !approx(got.NetDeposit, -50000) {
		t.Errorf("NetDeposit = %v, want -50000", got.NetDeposit)
	}
	if !approx(got.Result, 70000) {
		t.Errorf("Result = %v, want 70000", got.Result)
	}
}

// TestSinceOpen_ExcludesOtherCurrencies: the Trade API carries no exchange
// rates, so a foreign flow cannot enter the result. It is reported instead, so
// the screen can say what it left out.
func TestSinceOpen_ExcludesOtherCurrencies(t *testing.T) {
	from := xirrNow.AddDate(-1, 0, 0)

	got := SinceOpenResult([]models.Transaction{
		deposit(300, 100000, "RUB"),
		deposit(200, 500, "USD"),
		withdrawal(100, -100, "USD"),
	}, 120000, "RUB", from, xirrNow)

	if !approx(got.NetDeposit, 100000) {
		t.Errorf("NetDeposit = %v, want only the RUB flows", got.NetDeposit)
	}
	if !approx(got.Excluded["USD"], 400) {
		t.Errorf("Excluded = %v, want 400 USD reported separately", got.Excluded)
	}
	if len(got.Excluded) != 1 {
		t.Errorf("Excluded = %v, want exactly one foreign currency", got.Excluded)
	}
}

// TestSinceOpen_IgnoresNonCashMovements: only deposits and withdrawals are
// flows. A commission or a dividend happens inside the account and is part of
// the result, not of what was put in.
func TestSinceOpen_IgnoresNonCashMovements(t *testing.T) {
	from := xirrNow.AddDate(-1, 0, 0)

	got := SinceOpenResult([]models.Transaction{
		deposit(300, 100000, "RUB"),
		tx("COMMISSION", 0, -500, "RUB"),
		tx("INCOME", 0, 3000, "RUB"),
		tx("TRANSFER", 0, 0, "RUB"),
	}, 110000, "RUB", from, xirrNow)

	if !approx(got.NetDeposit, 100000) {
		t.Errorf("NetDeposit = %v, want 100000 — only deposits and withdrawals are flows",
			got.NetDeposit)
	}
	if !approx(got.Result, 10000) {
		t.Errorf("Result = %v, want 10000", got.Result)
	}
}

// TestSinceOpen_TradeTransactionsAreNotFlows: money moving into a purchase
// never leaves the account, so it must not be mistaken for a deposit.
func TestSinceOpen_TradeTransactionsAreNotFlows(t *testing.T) {
	from := xirrNow.AddDate(-1, 0, 0)

	purchase := deposit(200, -2805, "RUB")
	purchase.Trade = &models.TransactionTrade{Size: 10, Price: 280.5}

	got := SinceOpenResult([]models.Transaction{
		deposit(300, 100000, "RUB"),
		purchase,
	}, 110000, "RUB", from, xirrNow)

	if !approx(got.NetDeposit, 100000) {
		t.Errorf("NetDeposit = %v, want the purchase left out", got.NetDeposit)
	}
}

// TestSinceOpen_NoFlows still reports the result. An account funded before the
// history begins has an equity and no deposits the terminal can see.
func TestSinceOpen_NoFlows(t *testing.T) {
	from := xirrNow.AddDate(-1, 0, 0)

	got := SinceOpenResult(nil, 150000, "RUB", from, xirrNow)

	if got.SimpleValid {
		t.Error("a simple return was reported with no deposits to measure against")
	}
	if got.XIRRStatus == XIRROK {
		t.Error("an annual rate was reported with no flows")
	}
	if !approx(got.Result, 150000) {
		t.Errorf("Result = %v, want the whole equity", got.Result)
	}
}

// TestSinceOpen_NothingIsNaN: whatever the input, no figure that reaches the
// screen may be NaN or infinite.
func TestSinceOpen_NothingIsNaN(t *testing.T) {
	cases := []struct {
		name   string
		txs    []models.Transaction
		equity float64
		from   time.Time
	}{
		{"nothing at all", nil, 0, time.Time{}},
		{"zero equity", []models.Transaction{deposit(300, 100000, "RUB")}, 0, xirrNow.AddDate(-1, 0, 0)},
		{"future horizon", []models.Transaction{deposit(300, 100000, "RUB")}, 1000, xirrNow.AddDate(1, 0, 0)},
		{"zero deposit amount", []models.Transaction{deposit(300, 0, "RUB")}, 1000, xirrNow.AddDate(-1, 0, 0)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SinceOpenResult(c.txs, c.equity, "RUB", c.from, xirrNow)
			for name, v := range map[string]float64{
				"NetDeposit": got.NetDeposit, "Result": got.Result,
				"SimpleReturn": got.SimpleReturn, "XIRR": got.XIRR,
			} {
				if math.IsNaN(v) || math.IsInf(v, 0) {
					t.Errorf("%s = %v", name, v)
				}
			}
		})
	}
}
