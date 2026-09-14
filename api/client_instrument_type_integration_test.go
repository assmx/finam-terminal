//go:build integration

package api

import "testing"

// TestIntegration_GetInstrumentType proves the type map is filled by the same
// startup bulk call the terminal already makes, and resolves both by full
// symbol and by bare ticker.
func TestIntegration_GetInstrumentType(t *testing.T) {
	client, ts := setupTestServer(t)

	tests := []struct {
		name string
		key  string
		want string
	}{
		{"equity by ticker", "SBER", "EQUITIES"},
		{"equity by symbol", "SBER@TQBR", "EQUITIES"},
		{"bond by symbol", "LKOH@TQBR", "BONDS"},
		{"future by ticker", "YNDX", "FUTURES"},
		{"asset without a type", "ROSN", ""},
		{"instrument outside the list", "NOPE@TQBR", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := client.GetInstrumentType(tt.key); got != tt.want {
				t.Errorf("GetInstrumentType(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}

	// The cache is loaded once at startup; resolving types must not add to it.
	if got := ts.Assets.AssetsCallCount.Load(); got != 1 {
		t.Errorf("Assets called %d times, want exactly 1", got)
	}
}
