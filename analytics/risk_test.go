package analytics

import (
	"math"
	"testing"

	"finam-terminal/models"
)

func mcAccount(equity string, initial, maintenance float64) models.AccountInfo {
	return models.AccountInfo{
		Equity:            equity,
		PortfolioKind:     "MC",
		HasMarginData:     true,
		AvailableCash:     10000,
		InitialMargin:     initial,
		MaintenanceMargin: maintenance,
	}
}

// TestRiskMetrics_MC computes the three numbers that answer "how close am I to
// a margin call": utilisation, the cushion above the maintenance level, and
// leverage.
func TestRiskMetrics_MC(t *testing.T) {
	got := RiskMetrics(RiskInput{
		Account:       mcAccount("500000", 200000, 100000),
		GrossExposure: 750000,
	})

	if got.Kind != "MC" {
		t.Errorf("Kind = %q, want MC", got.Kind)
	}

	if !got.Utilization.Valid {
		t.Fatal("Utilization.Valid = false, want true")
	}
	if math.Abs(got.Utilization.Value-0.4) > 1e-9 {
		t.Errorf("Utilization = %v, want 0.4 (200000/500000)", got.Utilization.Value)
	}

	if !got.Cushion.Valid {
		t.Fatal("Cushion.Valid = false, want true")
	}
	if math.Abs(got.Cushion.Value-0.8) > 1e-9 {
		t.Errorf("Cushion = %v, want 0.8 ((500000-100000)/500000)", got.Cushion.Value)
	}

	if !got.Leverage.Valid {
		t.Fatal("Leverage.Valid = false, want true")
	}
	if math.Abs(got.Leverage.Value-1.5) > 1e-9 {
		t.Errorf("Leverage = %v, want 1.5 (750000/500000)", got.Leverage.Value)
	}
}

// TestRiskMetrics_FORTS reports margin use from money_reserved and refuses to
// invent a leverage figure the derivatives portfolio does not report.
func TestRiskMetrics_FORTS(t *testing.T) {
	got := RiskMetrics(RiskInput{
		Account: models.AccountInfo{
			Equity:        "100000",
			PortfolioKind: "FORTS",
			HasMarginData: true,
			AvailableCash: 75000,
			MoneyReserved: 25000,
		},
	})

	if got.Kind != "FORTS" {
		t.Errorf("Kind = %q, want FORTS", got.Kind)
	}
	if !got.Utilization.Valid {
		t.Fatal("Utilization.Valid = false, want true")
	}
	if math.Abs(got.Utilization.Value-0.25) > 1e-9 {
		t.Errorf("Utilization = %v, want 0.25 (25000/100000)", got.Utilization.Value)
	}
	if got.Cushion.Valid {
		t.Error("Cushion.Valid = true: FORTS reports no maintenance margin")
	}
	if got.Leverage.Valid {
		t.Error("Leverage.Valid = true: FORTS reports no gross exposure basis")
	}
}

// TestRiskMetrics_NoMarginData covers MCT and an absent portfolio oneof. Both
// carry no numbers, so every derived figure must say so rather than show a
// zero that looks like a measurement.
func TestRiskMetrics_NoMarginData(t *testing.T) {
	tests := []struct {
		name     string
		account  models.AccountInfo
		wantKind string
	}{
		{
			name:     "MCT",
			account:  models.AccountInfo{Equity: "100000", PortfolioKind: "MCT"},
			wantKind: "MCT",
		},
		{
			name:     "empty oneof",
			account:  models.AccountInfo{Equity: "100000"},
			wantKind: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RiskMetrics(RiskInput{Account: tt.account, GrossExposure: 50000})

			if got.Kind != tt.wantKind {
				t.Errorf("Kind = %q, want %q", got.Kind, tt.wantKind)
			}
			if got.HasMarginData {
				t.Error("HasMarginData = true, want false")
			}
			if got.Utilization.Valid || got.Cushion.Valid || got.Leverage.Valid {
				t.Error("no derived figure may be valid without margin data")
			}
		})
	}
}

// TestRiskMetrics_ZeroEquity is the divide-by-zero case: with no equity every
// ratio is undefined, and none may come back as NaN or Inf.
func TestRiskMetrics_ZeroEquity(t *testing.T) {
	tests := []struct {
		name   string
		equity string
	}{
		{"zero", "0"},
		{"unreadable", "N/A"},
		{"empty", ""},
		{"negative", "-1000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RiskMetrics(RiskInput{
				Account:       mcAccount(tt.equity, 200000, 100000),
				GrossExposure: 750000,
			})

			if got.EquityValid {
				t.Error("EquityValid = true, want false")
			}
			for name, m := range map[string]Metric{
				"Utilization": got.Utilization,
				"Cushion":     got.Cushion,
				"Leverage":    got.Leverage,
			} {
				if m.Valid {
					t.Errorf("%s.Valid = true, want false", name)
				}
				if math.IsNaN(m.Value) || math.IsInf(m.Value, 0) {
					t.Errorf("%s.Value = %v", name, m.Value)
				}
			}
		})
	}
}

// TestUtilizationLevel pins the colour thresholds, boundaries included: below
// 50% is calm, 50-80% is a warning, above 80% is not.
func TestUtilizationLevel(t *testing.T) {
	tests := []struct {
		name  string
		value float64
		want  Level
	}{
		{"zero", 0, LevelGood},
		{"just below the warning", 0.4999, LevelGood},
		{"exactly at the warning", 0.5, LevelWarn},
		{"inside the warning band", 0.7, LevelWarn},
		{"just below the alarm", 0.7999, LevelWarn},
		{"exactly at the alarm", 0.8, LevelBad},
		{"over the limit", 1.2, LevelBad},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UtilizationLevel(tt.value); got != tt.want {
				t.Errorf("UtilizationLevel(%v) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

// TestCushionLevel pins the mirrored thresholds: a large cushion is good, and
// the danger is at the bottom of the scale.
func TestCushionLevel(t *testing.T) {
	tests := []struct {
		name  string
		value float64
		want  Level
	}{
		{"comfortable", 0.9, LevelGood},
		{"just above the warning", 0.5001, LevelGood},
		{"exactly at the warning", 0.5, LevelWarn},
		{"inside the warning band", 0.3, LevelWarn},
		{"just above the alarm", 0.2001, LevelWarn},
		{"exactly at the alarm", 0.2, LevelBad},
		{"nothing left", 0, LevelBad},
		{"under water", -0.1, LevelBad},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CushionLevel(tt.value); got != tt.want {
				t.Errorf("CushionLevel(%v) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

// TestRiskMetrics_NegativeCushion keeps the number when equity has already
// fallen below the maintenance margin — that is exactly when it matters.
func TestRiskMetrics_NegativeCushion(t *testing.T) {
	got := RiskMetrics(RiskInput{Account: mcAccount("100000", 90000, 120000)})

	if !got.Cushion.Valid {
		t.Fatal("Cushion.Valid = false: a breached maintenance level is still a measurement")
	}
	if math.Abs(got.Cushion.Value-(-0.2)) > 1e-9 {
		t.Errorf("Cushion = %v, want -0.2 ((100000-120000)/100000)", got.Cushion.Value)
	}
	if CushionLevel(got.Cushion.Value) != LevelBad {
		t.Error("a negative cushion must read as LevelBad")
	}
}

// TestRiskMetrics_ZeroGrossExposure covers an account holding only cash: there
// is no leverage to report, and zero would be a fair answer only if the
// portfolio had positions.
func TestRiskMetrics_ZeroGrossExposure(t *testing.T) {
	got := RiskMetrics(RiskInput{Account: mcAccount("500000", 0, 0), GrossExposure: 0})

	if !got.Leverage.Valid {
		t.Fatal("Leverage.Valid = false, want true: zero exposure is a real answer")
	}
	if got.Leverage.Value != 0 {
		t.Errorf("Leverage = %v, want 0", got.Leverage.Value)
	}
	if !got.Utilization.Valid || got.Utilization.Value != 0 {
		t.Errorf("Utilization = %+v, want a valid 0", got.Utilization)
	}
}

// TestRiskMetrics_Equity exposes the parsed equity, so the renderer does not
// have to parse the string a second time and risk disagreeing.
func TestRiskMetrics_Equity(t *testing.T) {
	got := RiskMetrics(RiskInput{Account: mcAccount("500000,50", 100000, 50000)})

	if !got.EquityValid {
		t.Fatal("EquityValid = false, want true")
	}
	if math.Abs(got.Equity-500000.50) > 1e-9 {
		t.Errorf("Equity = %v, want 500000.50", got.Equity)
	}
}
