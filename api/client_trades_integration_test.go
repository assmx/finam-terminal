//go:build integration

package api

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// wideWindow spans every fixture record.
func wideWindow() (time.Time, time.Time) {
	return time.Now().Add(-365 * 24 * time.Hour), time.Now().Add(24 * time.Hour)
}

// TestGetTradesIntegration_WrapperMatchesWindow proves GetTradeHistory is now a
// window over GetTrades and nothing else: asking GetTrades for the same 30 days
// returns the same records in the same order.
func TestGetTradesIntegration_WrapperMatchesWindow(t *testing.T) {
	client, server := setupTestServer(t)

	viaWrapper, err := client.GetTradeHistory("ACC001")
	if err != nil {
		t.Fatalf("GetTradeHistory failed: %v", err)
	}
	callsAfterWrapper := server.Accounts.TradesCallCount

	now := time.Now()
	direct, err := client.GetTrades("ACC001", now.Add(-30*24*time.Hour), now, 0)
	if err != nil {
		t.Fatalf("GetTrades failed: %v", err)
	}

	if len(viaWrapper) != len(direct) {
		t.Fatalf("wrapper returned %d trades, direct call %d", len(viaWrapper), len(direct))
	}
	for i := range viaWrapper {
		if viaWrapper[i].ID != direct[i].ID {
			t.Errorf("position %d: wrapper %s, direct %s", i, viaWrapper[i].ID, direct[i].ID)
		}
	}
	if callsAfterWrapper != 1 {
		t.Errorf("the wrapper made %d calls, want exactly 1", callsAfterWrapper)
	}
	if server.Accounts.LastTradesRequest.GetLimit() != 0 {
		t.Errorf("limit = %d, want 0", server.Accounts.LastTradesRequest.GetLimit())
	}
}

// TestGetTradesIntegration_Interval checks the window actually filters, which
// the loader's backwards walk depends on entirely.
func TestGetTradesIntegration_Interval(t *testing.T) {
	client, _ := setupTestServer(t)

	from, to := wideWindow()
	all, err := client.GetTrades("ACC001", from, to, 0)
	if err != nil {
		t.Fatalf("wide window failed: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("fixture returned no trades")
	}

	// A window entirely before the fixture.
	none, err := client.GetTrades("ACC001", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC), 0)
	if err != nil {
		t.Fatalf("past window failed: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("got %d trades outside the fixture range, want none", len(none))
	}

	// A window ending just before the newest record must drop it.
	newest := all[0].Timestamp
	for _, tr := range all {
		if tr.Timestamp.After(newest) {
			newest = tr.Timestamp
		}
	}
	older, err := client.GetTrades("ACC001", from, newest.Add(-time.Second), 0)
	if err != nil {
		t.Fatalf("narrow window failed: %v", err)
	}
	if len(older) >= len(all) {
		t.Errorf("narrow window returned %d of %d; the interval is being ignored", len(older), len(all))
	}
}

// TestGetTradesIntegration_LimitTruncates makes the mock answer with exactly
// `limit` records — the signal the loader reads as "this chunk was truncated".
func TestGetTradesIntegration_LimitTruncates(t *testing.T) {
	client, _ := setupTestServer(t)

	from, to := wideWindow()
	all, err := client.GetTrades("ACC001", from, to, 0)
	if err != nil {
		t.Fatalf("unlimited call failed: %v", err)
	}
	if len(all) < 2 {
		t.Fatalf("fixture has %d trades, need at least 2", len(all))
	}

	limited, err := client.GetTrades("ACC001", from, to, 1)
	if err != nil {
		t.Fatalf("limited call failed: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("got %d trades with limit=1, want exactly 1", len(limited))
	}

	// Truncation keeps the newest record, so a backwards walk can move its
	// boundary and ask again for what it did not receive.
	newest := all[0]
	for _, tr := range all {
		if tr.Timestamp.After(newest.Timestamp) {
			newest = tr
		}
	}
	if limited[0].ID != newest.ID {
		t.Errorf("truncated to %s, want the newest record %s", limited[0].ID, newest.ID)
	}
}

// TestGetTradesIntegration_ServerCap stands in for a silent server-side cap:
// the client asks for 1000 and gets the cap instead, without an error. This is
// the shape that would defeat a naive "len == limit means truncated" check, and
// the reason the loader logs the largest response it saw.
func TestGetTradesIntegration_ServerCap(t *testing.T) {
	client, server := setupTestServer(t)
	server.Accounts.TradesLimitCap = 2

	from, to := wideWindow()
	trades, err := client.GetTrades("ACC001", from, to, 1000)
	if err != nil {
		t.Fatalf("capped call failed: %v", err)
	}
	if len(trades) != 2 {
		t.Fatalf("got %d trades under a cap of 2, want 2", len(trades))
	}
	if server.Accounts.LastTradesRequest.GetLimit() != 1000 {
		t.Errorf("the client sent limit=%d; the cap must come from the server, not the client",
			server.Accounts.LastTradesRequest.GetLimit())
	}
}

// TestGetTradesIntegration_Error keeps the gRPC code intact through the wrap.
func TestGetTradesIntegration_Error(t *testing.T) {
	client, server := setupTestServer(t)
	server.Accounts.TradesError = status.Error(codes.ResourceExhausted, "quota exceeded")

	from, to := wideWindow()
	if _, err := client.GetTrades("ACC001", from, to, 0); status.Code(err) != codes.ResourceExhausted {
		t.Errorf("status code = %v, want ResourceExhausted", status.Code(err))
	}

	// The History tab's wrapper reports the same failure.
	if _, err := client.GetTradeHistory("ACC001"); err == nil {
		t.Error("GetTradeHistory swallowed the error")
	}
}
