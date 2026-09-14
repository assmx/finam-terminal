package analytics

import (
	"testing"
	"time"

	"finam-terminal/models"
)

var flowFrom = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
var flowTo = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

func tx(category string, day int, amount float64, currency string) models.Transaction {
	return models.Transaction{
		ID:        category + "-" + itoa(day),
		Category:  category,
		Timestamp: flowFrom.AddDate(0, 0, day),
		Amount:    amount,
		Currency:  currency,
	}
}

// TestClassify maps every category the API enum can produce.
func TestClassify(t *testing.T) {
	cases := []struct {
		category string
		want     FlowGroup
	}{
		{"DEPOSIT", GroupDeposit},
		{"WITHDRAW", GroupWithdraw},
		{"INCOME", GroupIncome},
		{"COMMISSION", GroupCommission},
		{"TAX", GroupTax},
		{"LOAN", GroupLoan},
		{"FINE", GroupFine},
		{"TRANSFER", GroupTransfer},
		{"INHERITANCE", GroupMisc},
		{"CONTRACT_TERMINATION", GroupMisc},
		{"OUTCOMES", GroupMisc},
		{"OTHERS", GroupMisc},
		{"SOMETHING_NEW", GroupMisc},
		{"", GroupMisc},
		// The enum is upper case, but nothing in the contract promises it.
		{"deposit", GroupDeposit},
	}
	for _, c := range cases {
		t.Run(c.category, func(t *testing.T) {
			got := Classify(models.Transaction{Category: c.category})
			if got != c.want {
				t.Errorf("Classify(%q) = %s, want %s", c.category, got.Label(), c.want.Label())
			}
		})
	}
}

// TestClassify_TradeWins is the rule that stops the realised result being
// counted twice: a transaction that reflects a trade belongs to the trades
// group whatever category it carries, and the trades group is not a cash flow.
func TestClassify_TradeWins(t *testing.T) {
	for _, category := range []string{"OTHERS", "COMMISSION", "DEPOSIT", ""} {
		got := Classify(models.Transaction{
			Category: category,
			Trade:    &models.TransactionTrade{Size: 10, Price: 100},
		})
		if got != GroupTrade {
			t.Errorf("a %q transaction carrying a trade classified as %s, want the trades group",
				category, got.Label())
		}
	}
}

// TestGroupLabels: every group renders a name, including one out of range.
func TestGroupLabels(t *testing.T) {
	for _, g := range FlowGroupOrder {
		if g.Label() == "" {
			t.Errorf("group %d has no label", int(g))
		}
	}
	if FlowGroup(99).Label() == "" {
		t.Error("an unknown group rendered an empty label")
	}
	if len(FlowGroupOrder) == 0 {
		t.Error("FlowGroupOrder is empty; the money table would have no rows")
	}
}

// TestFlows_Totals computes the four figures the Money screen leads with.
func TestFlows_Totals(t *testing.T) {
	flow := Flows([]models.Transaction{
		tx("DEPOSIT", 1, 100000, "RUB"),
		tx("WITHDRAW", 2, -20000, "RUB"),
		tx("INCOME", 3, 3400, "RUB"),
		tx("COMMISSION", 4, -150, "RUB"),
		tx("TAX", 5, -250, "RUB"),
		tx("LOAN", 6, -37.8, "RUB"),
		tx("FINE", 7, -10, "RUB"),
	}, flowFrom, flowTo, "RUB")

	c, ok := flow.ByCurrency["RUB"]
	if !ok {
		t.Fatalf("no RUB flows in %v", flow.ByCurrency)
	}

	if !approx(c.Deposits, 100000) || !approx(c.Withdrawals, -20000) {
		t.Errorf("deposits/withdrawals = %v / %v, want 100000 / -20000", c.Deposits, c.Withdrawals)
	}
	if !approx(c.NetDeposit, 80000) {
		t.Errorf("NetDeposit = %v, want 80000", c.NetDeposit)
	}
	if !approx(c.Payouts, 3400) {
		t.Errorf("Payouts = %v, want 3400", c.Payouts)
	}
	// Costs keep the sign the API sent: they are money leaving the account.
	if !approx(c.Costs, -447.8) {
		t.Errorf("Costs = %v, want -447.8 (commission + tax + loan + fine)", c.Costs)
	}
}

// TestFlows_GroupSums keeps every group separately for the table.
func TestFlows_GroupSums(t *testing.T) {
	flow := Flows([]models.Transaction{
		tx("COMMISSION", 1, -100, "RUB"),
		tx("COMMISSION", 2, -50, "RUB"),
		tx("OTHERS", 3, -7, "RUB"),
		tx("INHERITANCE", 4, 5, "RUB"),
	}, flowFrom, flowTo, "RUB")

	c := flow.ByCurrency["RUB"]
	if !approx(c.Groups[GroupCommission], -150) {
		t.Errorf("commissions = %v, want -150", c.Groups[GroupCommission])
	}
	if !approx(c.Groups[GroupMisc], -2) {
		t.Errorf("misc = %v, want -2 (the two unclassified entries netted)", c.Groups[GroupMisc])
	}
}

// TestFlows_TradesExcluded: a transaction reflecting a trade is reported but
// never enters the money totals, because FIFO already accounts for it.
func TestFlows_TradesExcluded(t *testing.T) {
	purchase := tx("OTHERS", 1, -2805, "RUB")
	purchase.Trade = &models.TransactionTrade{Size: 10, Price: 280.5}

	flow := Flows([]models.Transaction{
		purchase,
		tx("COMMISSION", 2, -1.5, "RUB"),
	}, flowFrom, flowTo, "RUB")

	c := flow.ByCurrency["RUB"]
	if !approx(c.Groups[GroupMisc], 0) {
		t.Errorf("misc = %v; the trade must not land in an ordinary group", c.Groups[GroupMisc])
	}
	if !approx(c.Groups[GroupTrade], -2805) {
		t.Errorf("trades group = %v, want -2805 reported separately", c.Groups[GroupTrade])
	}
	if !approx(c.Costs, -1.5) {
		t.Errorf("Costs = %v, want only the commission", c.Costs)
	}
	if !approx(c.NetDeposit, 0) {
		t.Errorf("NetDeposit = %v, want 0 — a purchase is not a deposit", c.NetDeposit)
	}
}

// TestFlows_TransferIsQuantityNotMoney: a securities transfer moves shares, and
// counting it as money would invent a deposit.
func TestFlows_TransferIsQuantityNotMoney(t *testing.T) {
	transfer := models.Transaction{
		ID:        "t1",
		Category:  "TRANSFER",
		Timestamp: flowFrom.AddDate(0, 0, 1),
		Symbol:    "GAZP@MISX",
		ChangeQty: -10,
	}

	flow := Flows([]models.Transaction{transfer}, flowFrom, flowTo, "RUB")

	if !approx(flow.TransferQty, -10) {
		t.Errorf("TransferQty = %v, want -10", flow.TransferQty)
	}
	if c, ok := flow.ByCurrency["RUB"]; ok {
		if !approx(c.Groups[GroupTransfer], 0) || !approx(c.NetDeposit, 0) {
			t.Errorf("the transfer reached the money totals: %+v", c)
		}
	}
}

// TestFlows_SeparatesCurrencies never mixes two currencies, and a blank falls
// back to the account's base for the same reason the trade statistics do.
func TestFlows_SeparatesCurrencies(t *testing.T) {
	flow := Flows([]models.Transaction{
		tx("DEPOSIT", 1, 100000, "RUB"),
		tx("DEPOSIT", 2, 500, "USD"),
		tx("COMMISSION", 3, -10, ""),
	}, flowFrom, flowTo, "RUB")

	if len(flow.ByCurrency) != 2 {
		t.Fatalf("got %d currency groups, want 2: %v", len(flow.ByCurrency), flow.ByCurrency)
	}
	if !approx(flow.ByCurrency["USD"].Deposits, 500) {
		t.Errorf("USD deposits = %v, want 500", flow.ByCurrency["USD"].Deposits)
	}
	if !approx(flow.ByCurrency["RUB"].Costs, -10) {
		t.Errorf("the blank-currency commission did not join the base currency: %+v",
			flow.ByCurrency["RUB"])
	}
}

// TestFlows_WindowBoundaries includes both endpoints and nothing outside them.
func TestFlows_WindowBoundaries(t *testing.T) {
	at := func(ts time.Time) models.Transaction {
		return models.Transaction{ID: ts.String(), Category: "DEPOSIT", Timestamp: ts, Amount: 1, Currency: "RUB"}
	}

	flow := Flows([]models.Transaction{
		at(flowFrom.Add(-time.Second)),
		at(flowFrom),
		at(flowTo),
		at(flowTo.Add(time.Second)),
		at(time.Time{}),
	}, flowFrom, flowTo, "RUB")

	if !approx(flow.ByCurrency["RUB"].Deposits, 2) {
		t.Errorf("deposits = %v, want 2 — both endpoints in, everything else out",
			flow.ByCurrency["RUB"].Deposits)
	}
}

// TestFlows_Empty answers a usable zero value.
func TestFlows_Empty(t *testing.T) {
	flow := Flows(nil, flowFrom, flowTo, "RUB")
	if flow.ByCurrency == nil {
		t.Fatal("ByCurrency is nil; the renderer would have to check")
	}
	if len(flow.ByCurrency) != 0 || flow.TransferQty != 0 {
		t.Errorf("got %+v, want an empty flow", flow)
	}
}

// TestFlows_CurrencyCarried names each group so the renderer does not have to
// carry the map key alongside the value.
func TestFlows_CurrencyCarried(t *testing.T) {
	flow := Flows([]models.Transaction{tx("DEPOSIT", 1, 1, "USD")}, flowFrom, flowTo, "RUB")
	if flow.ByCurrency["USD"].Currency != "USD" {
		t.Errorf("Currency = %q, want USD", flow.ByCurrency["USD"].Currency)
	}
}
