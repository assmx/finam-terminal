package ui

import (
	"github.com/gdamore/tcell/v2"
	"strings"
	"testing"
	"time"
)

func TestCommodityDetailGroupsEqualWidthsAndAlignedWithName(t *testing.T) {
	for _, terminalWidth := range []int{120, 160, 220} {
		app := commodityDetailsApp(t)
		view := app.portfolioView.TabbedView.Commodities
		sourceAt := time.Date(2026, 10, 2, 15, 1, 2, 0, time.Local)
		nativeAt := sourceAt.Add(time.Minute)
		app.commodityQuotes["ES@XCME"].Timestamp = sourceAt
		app.commodityQuotes["SFZ6@RTSX"].Timestamp = nativeAt
		screen := tcell.NewSimulationScreen("UTF-8")
		if err := screen.Init(); err != nil {
			t.Fatal(err)
		}
		screen.SetSize(terminalWidth, 40)
		view.SetRect(30, 0, terminalWidth-30, 40)
		var conversionX, updatesX, conversionWidth int
		for index, price := range []string{"776.8", "999999999.123", "1.2"} {
			app.commodityQuotes["SFZ6@RTSX"].Last = price
			view.Sources.Select(2, 0)
			updateCommodityDetails(app)
			view.Draw(screen)
			_, _, contractWidth, _ := view.DetailGroups[0].GetRect()
			x, _, w, _ := view.surface.headings[1].GetRect()
			updateX, _, _, _ := view.surface.headings[2].GetRect()
			_, _, actualConversionWidth, _ := view.DetailGroups[1].GetRect()
			nameX, _, _ := view.Native.GetCell(0, 1).GetLastPosition()
			_, contractY, _, _ := view.surface.headings[0].GetRect()
			_, conversionY, _, _ := view.surface.headings[1].GetRect()
			if conversionY == contractY && nameX != x {
				t.Fatalf("width=%d Name starts at %d, conversion at %d", terminalWidth, nameX, x)
			}
			if contractWidth != actualConversionWidth {
				t.Fatalf("width=%d contract=%d conversion=%d", terminalWidth, contractWidth, actualConversionWidth)
			}
			for row := range 2 {
				label := view.DetailGroups[0].GetCell(row, 0)
				value := view.DetailGroups[0].GetCell(row, 1)
				labelX, labelY, _ := label.GetLastPosition()
				valueX, valueY, _ := value.GetLastPosition()
				line := commodityScreenLine(screen, labelY, terminalWidth)
				plainValue := strings.TrimSuffix(strings.TrimPrefix(value.Text, "[white]"), "[-]")
				if labelY != valueY || valueX <= labelX || !strings.Contains(line, label.Text) || !strings.Contains(line, plainValue) {
					t.Fatalf("contract row %d label/value not inline: %s", row, line)
				}
			}
			if index == 0 {
				conversionX, updatesX = x, updateX
				_, _, conversionWidth, _ = view.DetailGroups[1].GetRect()
			} else {
				_, _, actualWidth, _ := view.DetailGroups[1].GetRect()
				if x != conversionX || updateX != updatesX || actualWidth != conversionWidth {
					t.Fatalf("width=%d detail layout shifted for %s", terminalWidth, price)
				}
			}
			if w != conversionWidth {
				t.Fatal("heading and conversion table widths differ")
			}
			for row, want := range []string{"02.10.2026 15:01:02", "02.10.2026 15:02:02"} {
				cell := view.DetailGroups[2].GetCell(row, 1)
				cellX, y, _ := cell.GetLastPosition()
				line := commodityScreenLine(screen, y, terminalWidth)
				if !strings.Contains(line, want) {
					t.Fatalf("width=%d full timestamp clipped at x=%d: %s", terminalWidth, cellX, line)
				}
			}
		}
		screen.Fini()
	}
}
