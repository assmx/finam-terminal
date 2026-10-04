package ui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestCommodityGridStaysFixedAcrossNativeRowsAndResize(t *testing.T) {
	view := newCommodityView()
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(screen.Fini)

	short := [len(commodityHeaders)]string{"NG-12.26", "Gas", "3.123", "+0.123", "+4.10%", "100"}
	long := [len(commodityHeaders)]string{"LONG-DERIVATIVE-12.26", "A commodity name that extends well beyond the available name column", "123456789.12", "-1234567.89", "-1234.56%", "123456789012"}
	setCommodityHeaders(view.Sources)
	setCommodityGridTestRow(view.Sources, 1, short)
	setCommodityGridTestRow(view.Sources, 2, long)

	type column struct{ x, width int }
	var previous [len(commodityHeaders)]column
	var firstWide [len(commodityHeaders)]column
	for step, terminalWidth := range []int{120, 160, 90, 160} {
		var baseline [len(commodityHeaders)]column
		for selected, native := range [][len(commodityHeaders)]string{short, long, short} {
			view.Sources.Select(selected%2+1, 0)
			view.Native.Clear()
			setCommodityHeaders(view.Native)
			setCommodityGridTestRow(view.Native, 1, native)
			screen.SetSize(terminalWidth, 22)
			screen.Clear()
			view.SetRect(0, 0, terminalWidth, 22)
			view.Draw(screen)

			for col, header := range commodityHeaders {
				x, _, width := view.Sources.GetCell(0, col).GetLastPosition()
				position := column{x, width}
				if selected == 0 {
					baseline[col] = position
				} else if position != baseline[col] {
					t.Fatalf("terminal %d, native %s, %s moved: %+v, want %+v", terminalWidth, native[0], header, position, baseline[col])
				}
				for _, table := range [...]*tview.Table{view.Sources, view.Native} {
					for row := range table.GetRowCount() {
						cell := table.GetCell(row, col)
						cx, cy, cw := cell.GetLastPosition()
						if (column{cx, cw}) != baseline[col] {
							t.Fatalf("terminal %d, row %d, %s: x/width %d/%d, want %+v", terminalWidth, row, header, cx, cw, baseline[col])
						}
						// Check the actual terminal cells, not just layout metadata:
						// labels start left and complete numeric strings end right.
						text := cell.Text
						if row == 0 {
							text = header
						}
						runes := []rune(text)
						if col < 2 {
							got, _, _, _ := screen.GetContent(cx, cy)
							if got != runes[0] {
								t.Fatalf("terminal %d, row %d, %s: label starts with %q", terminalWidth, row, header, got)
							}
						} else {
							for offset, want := range runes {
								got, _, _, _ := screen.GetContent(cx+cw-len(runes)+offset, cy)
								if got != want {
									t.Fatalf("terminal %d, row %d, %s: numeric text %q clipped or misaligned at %d: %q, want %q", terminalWidth, row, header, text, offset, got, want)
								}
							}
						}
					}
				}
			}
		}
		if step > 0 && baseline == previous {
			t.Fatal("terminal resize did not change the grid")
		}
		if step == 1 {
			firstWide = baseline
		}
		if step == 3 && baseline != firstWide {
			t.Fatalf("restoring terminal width did not restore the grid: %+v, want %+v", baseline, firstWide)
		}
		previous = baseline
	}
}

func setCommodityGridTestRow(table *tview.Table, row int, values [len(commodityHeaders)]string) {
	for col, text := range values {
		align := tview.AlignRight
		if col < 2 {
			align = tview.AlignLeft
		}
		table.SetCell(row, col, tview.NewTableCell(text).SetAlign(align))
	}
}

func TestCommodityDetailsRenderedGroupsAtPortfolioWidths(t *testing.T) {
	for _, terminalWidth := range []int{120, 160} {
		t.Run(strconv.Itoa(terminalWidth), func(t *testing.T) {
			screen := tcell.NewSimulationScreen("UTF-8")
			if err := screen.Init(); err != nil {
				t.Fatal(err)
			}
			defer screen.Fini()
			screen.SetSize(terminalWidth, 22)
			view := newCommodityView()
			setCommodityHeaders(view.Sources)
			setCommodityHeaders(view.Native)
			values := [len(commodityHeaders)]string{"NG-12.26@FORTS", "Gas", "7768.00", "+20.00", "+0.26%", "100"}
			setCommodityGridTestRow(view.Sources, 1, values)
			setCommodityGridTestRow(view.Native, 1, values)
			view.setDetails(commodityDetailData{
				Contract: "NG-12.26@FORTS", Expiry: "[red]07.10.2026[-]",
				Converted: "7768.00", Delta: "+20.00",
				SourceUpdated: "12:34:56", NativeUpdated: "03.10 23:59",
			})
			// The application gives the account sidebar thirty columns.
			view.SetRect(30, 0, terminalWidth-30, 22)
			view.Draw(screen)
			if len(commodityHeaders) != 6 {
				t.Fatalf("quote grid has %d columns, want six", len(commodityHeaders))
			}
			for _, table := range []*tview.Table{view.Sources, view.Native} {
				if table.GetColumnCount() != 6 {
					t.Fatalf("quote table has %d columns, want six", table.GetColumnCount())
				}
			}
			for y := range 22 {
				if strings.Contains(commodityScreenLine(screen, y, terminalWidth), "Updated") {
					t.Fatal("obsolete Updated header remains on screen")
				}
			}
			_, nativeY, _ := view.Native.GetCell(1, 0).GetLastPosition()
			innerX, _, innerWidth, _ := view.details.GetInnerRect()
			for x := innerX; x < innerX+innerWidth; x++ {
				got, _, _, _ := screen.GetContent(x, nativeY+1)
				if got != tview.Borders.Horizontal {
					t.Fatalf("separator at x=%d: got %q", x, got)
				}
			}
			var firstY, previousRight int
			for group, table := range view.DetailGroups {
				labelX, rowY, _ := table.GetCell(0, 0).GetLastPosition()
				if group == 0 {
					firstY = rowY
				} else if rowY != firstY || labelX <= previousRight {
					t.Fatalf("detail group %d is not aligned beside the previous group", group)
				}
				for row := range 2 {
					cell := table.GetCell(row, 1)
					x, y, width := cell.GetLastPosition()
					text := strings.TrimSuffix(strings.TrimPrefix(cell.Text, "[red]"), "[-]")
					for offset, want := range []rune(text) {
						got, _, _, _ := screen.GetContent(x+offset, y)
						if got != want {
							t.Fatalf("group=%d row=%d value %q clipped at %d: %q", group, row, text, offset, got)
						}
					}
					if x+width > innerX+innerWidth {
						t.Fatal("detail value extends beyond its frame")
					}
					previousRight = max(previousRight, x+width-1)
					if group == 0 {
						_, _, style, _ := screen.GetContent(x, y)
						fg, _, _ := style.Decompose()
						want := tcell.ColorWhite
						if row == 1 {
							want = tcell.ColorRed
						}
						if fg.Hex() != want.Hex() {
							t.Fatalf("contract/expiry row %d foreground %s, want %s", row, fg, want)
						}
					}
				}
			}
		})
	}
}

func TestCommodityDetailsStackWithoutOverdrawingFrame(t *testing.T) {
	for _, width := range []int{48, 30} {
		screen := tcell.NewSimulationScreen("UTF-8")
		if err := screen.Init(); err != nil {
			t.Fatal(err)
		}
		screen.SetSize(width, 24)
		view := newCommodityView()
		setCommodityHeaders(view.Sources)
		setCommodityHeaders(view.Native)
		view.setDetails(commodityDetailData{
			Contract: "NG-12.26@FORTS", Expiry: "[red]07.10.2026[-]",
			Converted: "7768.00", Delta: "+20.00",
			SourceUpdated: "12:34:56", NativeUpdated: "03.10 23:59",
		})
		view.SetRect(0, 0, width, 24)
		view.Draw(screen)
		_, firstY, _ := view.DetailGroups[0].GetCell(0, 1).GetLastPosition()
		_, lastY, _ := view.DetailGroups[2].GetCell(0, 1).GetLastPosition()
		if lastY <= firstY {
			t.Fatalf("width %d: detail groups did not stack", width)
		}
		for _, table := range view.DetailGroups {
			for row := range 2 {
				cell := table.GetCell(row, 1)
				x, y, _ := cell.GetLastPosition()
				text := strings.TrimSuffix(strings.TrimPrefix(cell.Text, "[red]"), "[-]")
				for offset, want := range []rune(text) {
					got, _, _, _ := screen.GetContent(x+offset, y)
					if got != want {
						t.Fatalf("width %d: stacked value %q is not readable", width, cell.Text)
					}
				}
			}
		}
		for _, frame := range []*tview.Box{view.Sources.Box, view.details.Box} {
			x, y, frameWidth, frameHeight := frame.GetRect()
			for row := 1; row < frameHeight-1; row++ {
				for _, edgeX := range []int{x, x + frameWidth - 1} {
					got, _, _, _ := screen.GetContent(edgeX, y+row)
					if got != tview.Borders.Vertical {
						t.Fatalf("width %d: frame edge at %d/%d overwritten by %q", width, edgeX, y+row, got)
					}
				}
			}
			for column := 1; column < frameWidth-1; column++ {
				got, _, _, _ := screen.GetContent(x+column, y+frameHeight-1)
				if got != tview.Borders.Horizontal {
					t.Fatalf("width %d: bottom frame overwritten by %q", width, got)
				}
			}
		}
		screen.Fini()
	}
}

func commodityScreenLine(screen tcell.Screen, y, width int) string {
	var line strings.Builder
	for x := range width {
		r, _, _, _ := screen.GetContent(x, y)
		line.WriteRune(r)
	}
	return line.String()
}
