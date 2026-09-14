package analytics

import (
	"math"
	"testing"
)

func TestParseNumber(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		want   float64
		wantOK bool
	}{
		{"plain integer", "100", 100, true},
		{"decimal point", "285.50", 285.50, true},
		{"decimal comma", "285,50", 285.50, true},
		{"negative", "-300.25", -300.25, true},
		{"leading plus", "+12.5", 12.5, true},
		{"surrounding spaces", "  42 ", 42, true},
		{"zero", "0", 0, true},

		// The broker's own placeholder for "no value", used all over the
		// existing models, must not become a zero in an average.
		{"N/A", "N/A", 0, false},
		{"empty", "", 0, false},
		{"only spaces", "   ", 0, false},
		{"words", "нет данных", 0, false},

		// Nothing that would poison arithmetic downstream may get through:
		// ParseFloat happily accepts these spellings.
		{"NaN", "NaN", 0, false},
		{"Inf", "Inf", 0, false},
		{"+Inf", "+Inf", 0, false},
		{"-Inf", "-Inf", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseNumber(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("ParseNumber(%q) ok = %v, want %v", tt.in, ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("ParseNumber(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestSafeShare(t *testing.T) {
	tests := []struct {
		name  string
		part  float64
		whole float64
		want  float64
	}{
		{"half", 50, 100, 0.5},
		{"whole", 100, 100, 1},
		{"zero part", 0, 100, 0},
		{"zero whole", 5, 0, 0},
		{"negative whole", 5, -100, 0},
		{"negative part", -50, 100, -0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SafeShare(tt.part, tt.whole)
			if got != tt.want {
				t.Errorf("SafeShare(%v, %v) = %v, want %v", tt.part, tt.whole, got, tt.want)
			}
			if math.IsNaN(got) || math.IsInf(got, 0) {
				t.Errorf("SafeShare(%v, %v) produced %v", tt.part, tt.whole, got)
			}
		})
	}
}

func TestPositionValue(t *testing.T) {
	tests := []struct {
		name      string
		quantity  string
		last      string
		broker    string
		faceValue float64
		want      float64
		wantOK    bool
	}{
		{
			name:     "live quote wins",
			quantity: "100", last: "285.00", broker: "280.00",
			want: 28500, wantOK: true,
		},
		{
			// The overview and the Positions column must agree, and both fall
			// back to the broker's own valuation before giving up on a row.
			name:     "broker price when the quote has not arrived",
			quantity: "100", last: "", broker: "280.00",
			want: 28000, wantOK: true,
		},
		{
			name:     "broker price when the quote says N/A",
			quantity: "50", last: "N/A", broker: "160.30",
			want: 8015, wantOK: true,
		},
		{
			// A short keeps its sign here; callers that need a magnitude
			// (allocation shares, leverage) take the absolute value.
			name:     "short position keeps its sign",
			quantity: "-10", last: "150", broker: "",
			want: -1500, wantOK: true,
		},
		{
			name:     "no price at all",
			quantity: "100", last: "N/A", broker: "N/A",
			wantOK: false,
		},
		{
			name:     "unreadable quantity",
			quantity: "N/A", last: "285.00", broker: "",
			wantOK: false,
		},
		{
			// Bonds quoted as a percent of par: 98.5% of a 1000 par, 10 papers.
			name:     "percent of par with a face value",
			quantity: "10", last: "98.5", broker: "", faceValue: 1000,
			want: 9850, wantOK: true,
		},
		{
			// faceValue 0 keeps the plain form, which is what every caller
			// passes today: the reconnaissance never saw a bond position.
			name:     "face value zero keeps price times quantity",
			quantity: "10", last: "98.5", broker: "", faceValue: 0,
			want: 985, wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := PositionValue(tt.quantity, tt.last, tt.broker, tt.faceValue)
			if ok != tt.wantOK {
				t.Fatalf("PositionValue ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("PositionValue = %v, want %v", got, tt.want)
			}
		})
	}
}
