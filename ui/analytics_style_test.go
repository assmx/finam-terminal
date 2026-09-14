package ui

import (
	"strings"
	"testing"

	"github.com/rivo/tview"
)

func TestShareBarLength(t *testing.T) {
	tests := []struct {
		name  string
		share float64
		width int
		want  int // visible cells
	}{
		{"zero draws nothing", 0, 10, 0},
		{"half fills half", 0.5, 10, 5},
		{"full fills the width", 1, 10, 10},
		{"above one is clamped", 1.5, 10, 10},
		{"negative draws nothing", -0.2, 10, 0},
		{"zero width draws nothing", 0.5, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shareBar(tt.share, tt.width)
			if w := tview.TaggedStringWidth(got); w != tt.want {
				t.Errorf("shareBar(%v, %d) width = %d, want %d (%q)", tt.share, tt.width, w, tt.want, got)
			}
		})
	}
}

// A share too small to fill a cell must still leave a mark. Rendering it as
// nothing would say the position does not exist, which is a different fact
// from "it is tiny".
func TestShareBarTinyShareStillShows(t *testing.T) {
	got := shareBar(0.007, 10)
	if got == "" {
		t.Fatal("a non-zero share rendered as an empty bar")
	}
	if w := tview.TaggedStringWidth(got); w != 1 {
		t.Errorf("tiny share width = %d, want 1 (%q)", w, got)
	}
}

// The bar carries no background fill: the old ░ padding turned every row into
// grey noise and is what the redesign removes.
func TestShareBarHasNoBackgroundFill(t *testing.T) {
	if got := shareBar(0.3, 10); strings.ContainsRune(got, '░') {
		t.Errorf("shareBar still draws a ░ background: %q", got)
	}
}

func TestLeaderRowFillsExactWidth(t *testing.T) {
	got := leaderRow("Использование маржи", "2.0%", 40)
	if w := tview.TaggedStringWidth(got); w != 40 {
		t.Errorf("leaderRow width = %d, want 40 (%q)", w, got)
	}
	if !strings.Contains(got, "Использование маржи") || !strings.Contains(got, "2.0%") {
		t.Errorf("leaderRow lost its content: %q", got)
	}
	if !strings.ContainsRune(got, '·') {
		t.Errorf("leaderRow drew no leader dots: %q", got)
	}
}

// A coloured value is measured by what it shows, not by the tags it carries.
func TestLeaderRowMeasuresTaggedValue(t *testing.T) {
	plain := leaderRow("Результат", "1 234.00", 40)
	tagged := leaderRow("Результат", "[green]1 234.00[-]", 40)

	if tview.TaggedStringWidth(plain) != tview.TaggedStringWidth(tagged) {
		t.Errorf("colour changed the row width: plain %q vs tagged %q", plain, tagged)
	}
}

// Too narrow to fit is a layout the renderer must survive, not a panic or an
// overflowing row of dots.
func TestLeaderRowDegradesWhenNarrow(t *testing.T) {
	got := leaderRow("Очень длинная подпись строки", "999 999.00", 10)
	if strings.ContainsRune(got, '·') {
		t.Errorf("leaderRow padded a row that does not fit: %q", got)
	}
	if !strings.Contains(got, "999 999.00") {
		t.Errorf("leaderRow dropped the value: %q", got)
	}
}

// When the row is nearly wide enough, the label gives way rather than the
// figure. The number is the information; the label is recoverable from the
// rows around it, and cutting the number instead is what turned 100 000.00
// into "100 000." on a narrow panel.
func TestLeaderRowSacrificesTheLabelNotTheValue(t *testing.T) {
	got := leaderRow("Начальная маржа", "100 000.00", 24)

	if !strings.Contains(got, "100 000.00") {
		t.Errorf("leaderRow cut the figure instead of the label: %q", got)
	}
	if w := tview.TaggedStringWidth(got); w > 24 {
		t.Errorf("leaderRow overflowed: width %d, want at most 24 (%q)", w, got)
	}
	if strings.Contains(got, "Начальная маржа") {
		t.Errorf("leaderRow found room it does not have: %q", got)
	}
}

// A label carrying colour tags is left whole: cutting one would cut a tag in
// half and spill markup onto the screen.
func TestLeaderRowNeverCutsATaggedLabel(t *testing.T) {
	got := leaderRow("[red]заём RUB[-]", "100 000.00", 18)

	if strings.Contains(got, "..") {
		t.Errorf("leaderRow truncated a tagged label: %q", got)
	}
	if !strings.Contains(got, "[red]заём RUB[-]") {
		t.Errorf("leaderRow damaged the tagged label: %q", got)
	}
}

func TestPadTaggedRightAligns(t *testing.T) {
	got := padTagged("[green]+12.50[-]", 12)
	if w := tview.TaggedStringWidth(got); w != 12 {
		t.Errorf("padTagged width = %d, want 12 (%q)", w, got)
	}
	if !strings.HasPrefix(got, " ") {
		t.Errorf("padTagged did not right-align: %q", got)
	}
}

func TestPadTaggedLeavesOversizedValueAlone(t *testing.T) {
	value := "[green]123 456 789.00[-]"
	if got := padTagged(value, 4); got != value {
		t.Errorf("padTagged truncated an oversized value: %q", got)
	}
}

func TestSectionTitleCarriesName(t *testing.T) {
	got := sectionTitle("Секторы")
	if !strings.Contains(got, "Секторы") {
		t.Errorf("sectionTitle lost the name: %q", got)
	}
	if !strings.Contains(got, analyticsAccentTag) {
		t.Errorf("sectionTitle is not drawn in the accent colour: %q", got)
	}
}

func TestKPIRowRendersEveryTile(t *testing.T) {
	got := kpiRow(60,
		kpiTile{Caption: "РЕЗУЛЬТАТ", Value: "[green]+12 480.50[-]"},
		kpiTile{Caption: "ПРИБЫЛЬНЫХ", Value: "64%"},
		kpiTile{Caption: "ПРОФИТ-ФАКТОР", Value: "1.84"},
	)

	for _, want := range []string{"РЕЗУЛЬТАТ", "ПРИБЫЛЬНЫХ", "ПРОФИТ-ФАКТОР", "+12 480.50", "64%", "1.84"} {
		if !strings.Contains(got, want) {
			t.Errorf("kpiRow is missing %q:\n%s", want, got)
		}
	}

	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("kpiRow drew %d lines, want 2 (captions over values):\n%s", len(lines), got)
	}
}

func TestKPIRowWithNoTilesIsEmpty(t *testing.T) {
	if got := kpiRow(60); got != "" {
		t.Errorf("kpiRow() with no tiles = %q, want empty", got)
	}
}

func TestSignedBarColoursBySign(t *testing.T) {
	if got := signedBar(50, 100, 10); !strings.Contains(got, "green") {
		t.Errorf("a positive amount is not green: %q", got)
	}
	if got := signedBar(-50, 100, 10); !strings.Contains(got, "red") {
		t.Errorf("a negative amount is not red: %q", got)
	}
	if got := signedBar(0, 100, 10); tview.TaggedStringWidth(got) != 0 {
		t.Errorf("a zero amount drew a bar: %q", got)
	}
}

// The scale is the largest magnitude in the block, so the widest bar fills the
// column and every other bar is read against it.
func TestSignedBarScalesToMax(t *testing.T) {
	full := signedBar(100, 100, 10)
	if w := tview.TaggedStringWidth(full); w != 10 {
		t.Errorf("the largest amount filled %d cells, want 10 (%q)", w, full)
	}
	half := signedBar(-50, 100, 10)
	if w := tview.TaggedStringWidth(half); w != 5 {
		t.Errorf("half the largest amount filled %d cells, want 5 (%q)", w, half)
	}
}

// A zero or absent scale must not divide by zero or draw a full-width bar.
func TestSignedBarWithoutScaleDrawsNothing(t *testing.T) {
	if got := signedBar(50, 0, 10); tview.TaggedStringWidth(got) != 0 {
		t.Errorf("signedBar with a zero scale drew %q", got)
	}
}
