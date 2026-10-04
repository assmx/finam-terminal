package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestCommodityExpiryColourStrictWarningBoundary(t *testing.T) {
	now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.Local)
	boundary := now.AddDate(0, 0, 5)
	for _, tc := range []struct {
		name        string
		expiry      time.Time
		warningDays int
		red         bool
	}{
		{"before", boundary.Add(-time.Nanosecond), 5, true},
		{"exact", boundary, 5, false},
		{"after", boundary.Add(time.Nanosecond), 5, false},
		{"now", now, 5, false},
		{"expired", now.Add(-time.Nanosecond), 5, false},
		{"absent", time.Time{}, 5, false},
		{"disabled", now.AddDate(0, 0, 1), 0, false},
		{"custom inside", now.AddDate(0, 0, 6), 7, true},
		{"custom boundary", now.AddDate(0, 0, 7), 7, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			screen := tcell.NewSimulationScreen("UTF-8")
			if err := screen.Init(); err != nil {
				t.Fatal(err)
			}
			defer screen.Fini()
			screen.SetSize(120, 18)
			view := newCommodityView()
			view.setDetails(commodityDetailData{Expiry: commodityExpiryDate(tc.expiry, now, tc.warningDays)})
			view.SetRect(0, 0, 120, 18)
			view.Draw(screen)
			x, y, _ := view.DetailGroups[0].GetCell(1, 1).GetLastPosition()
			char, _, style, _ := screen.GetContent(x, y)
			fg, _, _ := style.Decompose()
			if got := fg.Hex() == tcell.ColorRed.Hex(); got != tc.red {
				t.Fatalf("rendered %q foreground=%s, want red=%v", char, fg, tc.red)
			}
		})
	}
}

func TestCommodityExpiryRemainsVisibleWithSelectionChange(t *testing.T) {
	app := commodityDetailsApp(t)
	for i := range app.commodities {
		app.commodities[i].ExpiryWarningDays = 5
	}
	view := app.portfolioView.TabbedView.Commodities
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	for _, width := range []int{160, 120, 75} {
		for _, tc := range []struct {
			source    string
			row, days int
			red       bool
		}{{"ES@XCME", 2, 6, false}, {"NG@XNYM", 1, 3, true}, {"ES@XCME", 2, 3, true}} {
			expiry := time.Now().AddDate(0, 0, tc.days)
			future := app.commodityFutures.bindings[tc.source]
			future.Expiration = expiry
			app.commodityFutures.bindings[tc.source] = future
			view.Sources.Select(tc.row, 0)
			screen.SetSize(width, 22)
			view.SetRect(0, 0, width, 22)
			view.Draw(screen)
			date := expiry.Local().Format("02.01.2006")
			found, contractFound := false, false
			for y := range 22 {
				var line strings.Builder
				for x := range width {
					r, _, _, _ := screen.GetContent(x, y)
					line.WriteRune(r)
				}
				text := line.String()
				if at := strings.Index(text, future.Symbol); at >= 0 {
					x := len([]rune(text[:at]))
					_, _, style, _ := screen.GetContent(x, y)
					fg, _, _ := style.Decompose()
					if fg.Hex() != tcell.ColorWhite.Hex() {
						t.Fatalf("width=%d source=%s contract foreground=%s, want normal white", width, tc.source, fg)
					}
					contractFound = true
				}
				at := strings.Index(text, date)
				if at < 0 {
					continue
				}
				if strings.Contains(text, "Контракт:") {
					t.Fatal("expiry was merged into contract line")
				}
				x := len([]rune(text[:at]))
				_, _, style, _ := screen.GetContent(x, y)
				fg, _, _ := style.Decompose()
				if got := fg.Hex() == tcell.ColorRed.Hex(); got != tc.red {
					t.Fatalf("width=%d source=%s expiry foreground=%s, want red=%v", width, tc.source, fg, tc.red)
				}
				found = true
			}
			if !found {
				t.Fatalf("width=%d source=%s expiry was clipped by detail layout", width, tc.source)
			}
			if !contractFound {
				t.Fatalf("width=%d source=%s full contract symbol was clipped by detail layout", width, tc.source)
			}
		}
	}
}
