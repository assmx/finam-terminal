//go:build integration

package api

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestGetTransactionsIntegration_AllCategories walks the whole fixture over
// bufconn and checks the categories the cash-flow grouping switches on, plus
// the two shapes that are easy to get wrong: a charge (negative money) and a
// securities transfer (a quantity with no money at all).
func TestGetTransactionsIntegration_AllCategories(t *testing.T) {
	client, server := setupTestServer(t)

	from := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

	txs, err := client.GetTransactions("ACC001", from, to, 0)
	if err != nil {
		t.Fatalf("GetTransactions failed: %v", err)
	}
	if len(txs) == 0 {
		t.Fatal("no transactions returned")
	}
	if server.Accounts.TransactionsCallCount != 1 {
		t.Errorf("Transactions called %d times, want 1", server.Accounts.TransactionsCallCount)
	}

	byCategory := map[string]int{}
	for _, tx := range txs {
		byCategory[tx.Category]++
	}
	for _, want := range []string{"DEPOSIT", "WITHDRAW", "INCOME", "COMMISSION", "TAX", "TRANSFER", "OTHERS"} {
		if byCategory[want] == 0 {
			t.Errorf("fixture carries no %s transaction; the category table would go untested", want)
		}
	}

	var charge, transfer, withTrade, foreign int
	for _, tx := range txs {
		if tx.Category == "COMMISSION" {
			charge++
			if tx.Amount >= 0 {
				t.Errorf("commission %s has amount %v, want a negative charge", tx.ID, tx.Amount)
			}
		}
		if tx.Category == "TRANSFER" {
			transfer++
			if tx.ChangeQty == 0 {
				t.Errorf("transfer %s has no ChangeQty; securities movements arrive as a quantity", tx.ID)
			}
			if tx.Amount != 0 {
				t.Errorf("transfer %s has amount %v, want no money on a securities transfer", tx.ID, tx.Amount)
			}
		}
		if tx.Trade != nil {
			withTrade++
			if tx.Trade.Size == 0 || tx.Trade.Price == 0 {
				t.Errorf("transaction %s has an empty trade sub-message: %+v", tx.ID, *tx.Trade)
			}
		}
		if tx.Currency != "" && tx.Currency != "RUB" {
			foreign++
		}
	}
	if charge == 0 || transfer == 0 {
		t.Errorf("charge=%d transfer=%d, want at least one of each", charge, transfer)
	}
	if withTrade == 0 {
		t.Error("no transaction reflects a trade; the group that must stay out of cash flows is untested")
	}
	if foreign == 0 {
		t.Error("no non-RUB transaction; the foreign-currency deposit case is untested")
	}
}

// TestGetTransactionsIntegration_Interval proves the mock honours the request
// interval, which is what makes the loader's chunking testable at all.
func TestGetTransactionsIntegration_Interval(t *testing.T) {
	client, _ := setupTestServer(t)

	wide, err := client.GetTransactions("ACC001", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), 0)
	if err != nil {
		t.Fatalf("wide window failed: %v", err)
	}

	// A window in the past of every fixture record.
	empty, err := client.GetTransactions("ACC001", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC), 0)
	if err != nil {
		t.Fatalf("empty window failed: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("got %d transactions outside the fixture range, want none", len(empty))
	}

	// A window that ends before the last record must return strictly fewer.
	half := wide[0].Timestamp
	for _, tx := range wide {
		if tx.Timestamp.After(half) {
			half = tx.Timestamp
		}
	}
	narrow, err := client.GetTransactions("ACC001", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		half.Add(-time.Second), 0)
	if err != nil {
		t.Fatalf("narrow window failed: %v", err)
	}
	if len(narrow) >= len(wide) {
		t.Errorf("narrow window returned %d of %d; the mock ignores the interval", len(narrow), len(wide))
	}
}

// TestGetTransactionsIntegration_Limit checks that the mock truncates like the
// API is assumed to: at most limit records, newest first.
func TestGetTransactionsIntegration_Limit(t *testing.T) {
	client, _ := setupTestServer(t)

	from := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

	all, err := client.GetTransactions("ACC001", from, to, 0)
	if err != nil {
		t.Fatalf("unlimited call failed: %v", err)
	}
	if len(all) < 3 {
		t.Fatalf("fixture has %d transactions, need at least 3 to test truncation", len(all))
	}

	limited, err := client.GetTransactions("ACC001", from, to, 2)
	if err != nil {
		t.Fatalf("limited call failed: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("got %d transactions with limit=2, want exactly 2", len(limited))
	}
}

// TestGetTransactionsIntegration_Error surfaces the gRPC code so the loader can
// tell a rate limit from an ordinary failure.
func TestGetTransactionsIntegration_Error(t *testing.T) {
	client, server := setupTestServer(t)
	server.Accounts.TransactionsError = status.Error(codes.ResourceExhausted, "quota exceeded")

	_, err := client.GetTransactions("ACC001", time.Now().Add(-time.Hour), time.Now(), 0)
	if err == nil {
		t.Fatal("expected an error")
	}
	if status.Code(err) != codes.ResourceExhausted {
		t.Errorf("status code = %v, want ResourceExhausted", status.Code(err))
	}
}

// TestGetTransactionsIntegration_UnknownAccount returns an empty list rather
// than an error for an account the fixture does not know.
func TestGetTransactionsIntegration_UnknownAccount(t *testing.T) {
	client, _ := setupTestServer(t)

	txs, err := client.GetTransactions("NOPE", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txs) != 0 {
		t.Errorf("got %d transactions for an unknown account, want none", len(txs))
	}
}
