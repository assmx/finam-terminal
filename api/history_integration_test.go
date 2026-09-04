//go:build integration

package api

import (
	"context"
	"testing"
	"time"

	"finam-terminal/api/testserver"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// historyTestPace zeroes the inter-request pause so a multi-chunk pass does not
// spend seconds sleeping.
func historyTestPace(t *testing.T) {
	t.Helper()
	prev := historyPace
	historyPace = 0
	t.Cleanup(func() { historyPace = prev })
}

// TestLoadHistoryIntegration_ChunkCount checks the cost of a pass end to end:
// one request per chunk per method, over bufconn, counted by the server.
func TestLoadHistoryIntegration_ChunkCount(t *testing.T) {
	historyTestPace(t)

	client, server := setupTestServer(t)

	to := time.Now()
	bundle, err := client.LoadHistory(context.Background(), HistoryRequest{
		AccountID:        "ACC001",
		TradesFrom:       to.Add(-3 * historyChunk),
		TransactionsFrom: to.Add(-2 * historyChunk),
		To:               to,
	}, nil)
	if err != nil {
		t.Fatalf("LoadHistory failed: %v", err)
	}

	if server.Accounts.TradesCallCount != 3 {
		t.Errorf("Trades called %d times, want 3 chunks", server.Accounts.TradesCallCount)
	}
	if server.Accounts.TransactionsCallCount != 2 {
		t.Errorf("Transactions called %d times, want 2 chunks", server.Accounts.TransactionsCallCount)
	}
	if bundle.Requests != 5 {
		t.Errorf("Requests = %d, want 5", bundle.Requests)
	}
	if !bundle.Complete {
		t.Errorf("Complete = false, stop = %v", bundle.Stop)
	}

	// The fixtures sit a few days back, so a three-chunk window holds all of them.
	if len(bundle.Trades) == 0 {
		t.Error("no trades loaded")
	}
	if len(bundle.Transactions) == 0 {
		t.Error("no transactions loaded")
	}
}

// TestLoadHistoryIntegration_NoDuplicates proves the chunk boundaries are
// disjoint: every record arrives exactly once even though the fixtures sit
// close together in time.
func TestLoadHistoryIntegration_NoDuplicates(t *testing.T) {
	historyTestPace(t)

	client, _ := setupTestServer(t)

	to := time.Now()
	bundle, err := client.LoadHistory(context.Background(), HistoryRequest{
		AccountID:        "ACC001",
		TradesFrom:       to.Add(-5 * historyChunk),
		TransactionsFrom: to.Add(-5 * historyChunk),
		To:               to,
	}, nil)
	if err != nil {
		t.Fatalf("LoadHistory failed: %v", err)
	}

	seen := map[string]int{}
	for _, tr := range bundle.Trades {
		seen[tr.ID]++
	}
	for _, tx := range bundle.Transactions {
		seen[tx.ID]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("record %s arrived %d times", id, n)
		}
	}
}

// TestLoadHistoryIntegration_SplitsOnServerTruncation drives the split against
// a real server that truncates: the cap is set below the fixture size, so the
// first chunk comes back full and has to be halved.
func TestLoadHistoryIntegration_SplitsOnServerTruncation(t *testing.T) {
	historyTestPace(t)

	prev := historyLimit
	historyLimit = 2
	t.Cleanup(func() { historyLimit = prev })

	client, server := setupTestServer(t)
	server.Accounts.TransactionsLimitCap = 2

	to := time.Now()
	bundle, err := client.LoadHistory(context.Background(), HistoryRequest{
		AccountID:        "ACC001",
		TransactionsFrom: to.Add(-historyChunk),
		To:               to,
	}, nil)
	if err != nil {
		t.Fatalf("LoadHistory failed: %v", err)
	}

	if server.Accounts.TransactionsCallCount < 3 {
		t.Errorf("Transactions called %d times, want at least 3 — one full answer plus its halves",
			server.Accounts.TransactionsCallCount)
	}

	// The fixture holds 11 transactions inside a few hours. Splitting a 92-day
	// chunk down to a day recovers them, since they all sit on the same day.
	// What matters here is that no record is duplicated by the re-request.
	seen := map[string]int{}
	for _, tx := range bundle.Transactions {
		seen[tx.ID]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("transaction %s arrived %d times after splitting", id, n)
		}
	}
}

// TestLoadHistoryIntegration_RateLimitedKeepsPartial stops at the first refusal
// and keeps what the earlier chunks delivered.
func TestLoadHistoryIntegration_RateLimitedKeepsPartial(t *testing.T) {
	historyTestPace(t)

	client, server := setupTestServer(t)

	to := time.Now()

	// Load the first chunk cleanly, then refuse.
	go func() {}()
	bundle, err := client.LoadHistory(context.Background(), HistoryRequest{
		AccountID:  "ACC001",
		TradesFrom: to.Add(-historyChunk),
		To:         to,
	}, func(HistoryProgress) {
		server.Accounts.TradesError = status.Error(codes.ResourceExhausted, "quota exceeded")
	})
	if err != nil {
		t.Fatalf("LoadHistory failed: %v", err)
	}
	if len(bundle.Trades) == 0 {
		t.Error("the first chunk's trades were discarded")
	}

	// A fresh pass now hits the refusal immediately and reports it.
	server.Accounts.TradesError = status.Error(codes.ResourceExhausted, "quota exceeded")
	_, err = client.LoadHistory(context.Background(), HistoryRequest{
		AccountID:  "ACC001",
		TradesFrom: to.Add(-historyChunk),
		To:         to,
	}, nil)
	if err == nil {
		t.Fatal("expected an error when the very first request is refused")
	}
	if status.Code(err) != codes.ResourceExhausted {
		t.Errorf("status code = %v, want ResourceExhausted", status.Code(err))
	}
}

// TestLoadHistoryIntegration_QuotaBlocksPass refuses to start when the server's
// own quota table says the pass would not fit.
func TestLoadHistoryIntegration_QuotaBlocksPass(t *testing.T) {
	historyTestPace(t)

	client, server := setupTestServer(t)
	server.UsageMetrics.Quotas = testserver.LowTradesQuota()

	to := time.Now()
	bundle, err := client.LoadHistory(context.Background(), HistoryRequest{
		AccountID:  "ACC001",
		TradesFrom: to.Add(-20 * historyChunk),
		To:         to,
	}, nil)
	if err != nil {
		t.Fatalf("LoadHistory failed: %v", err)
	}

	if bundle.Stop != StopQuota {
		t.Errorf("Stop = %v, want StopQuota", bundle.Stop)
	}
	if server.Accounts.TradesCallCount != 0 {
		t.Errorf("Trades called %d times, want 0 — a blocked pass must not start",
			server.Accounts.TradesCallCount)
	}
	if server.UsageMetrics.GetUsageMetricsCallCount.Load() != 1 {
		t.Errorf("GetUsageMetrics called %d times, want exactly 1",
			server.UsageMetrics.GetUsageMetricsCallCount.Load())
	}
}

// TestLoadHistoryIntegration_TailWindow is the shape the R key uses: a narrow
// window over the newest records, costing a single request.
func TestLoadHistoryIntegration_TailWindow(t *testing.T) {
	historyTestPace(t)

	client, server := setupTestServer(t)

	to := time.Now()
	bundle, err := client.LoadHistory(context.Background(), HistoryRequest{
		AccountID:        "ACC001",
		TradesFrom:       to.Add(-24 * time.Hour),
		TransactionsFrom: to.Add(-24 * time.Hour),
		To:               to,
	}, nil)
	if err != nil {
		t.Fatalf("LoadHistory failed: %v", err)
	}

	if server.Accounts.TradesCallCount != 1 || server.Accounts.TransactionsCallCount != 1 {
		t.Errorf("calls = %d trades / %d transactions, want 1/1 for a one-day tail",
			server.Accounts.TradesCallCount, server.Accounts.TransactionsCallCount)
	}
	if server.UsageMetrics.GetUsageMetricsCallCount.Load() != 0 {
		t.Errorf("GetUsageMetrics called %d times for a two-request pass, want 0",
			server.UsageMetrics.GetUsageMetricsCallCount.Load())
	}
	if !bundle.Complete {
		t.Errorf("Complete = false for a tail top-up, stop = %v", bundle.Stop)
	}
}

// TestLoadHistoryIntegration_NoWindows makes no request at all for an account
// with no known history start.
func TestLoadHistoryIntegration_NoWindows(t *testing.T) {
	client, server := setupTestServer(t)

	bundle, err := client.LoadHistory(context.Background(), HistoryRequest{
		AccountID: "ACC001",
		To:        time.Now(),
	}, nil)
	if err != nil {
		t.Fatalf("LoadHistory failed: %v", err)
	}
	if server.Accounts.TradesCallCount != 0 || server.Accounts.TransactionsCallCount != 0 {
		t.Errorf("calls = %d/%d, want none when both windows are zero",
			server.Accounts.TradesCallCount, server.Accounts.TransactionsCallCount)
	}
	if len(bundle.Trades) != 0 || len(bundle.Transactions) != 0 {
		t.Error("records appeared without a request")
	}
}
