package ui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

var commodityHeaders = [...]string{"Ticker", "Name", "Price", "Chg", "Chg%", "Volume"}

type commodityDetailData struct {
	Contract, Expiry, Converted, Delta, SourceUpdated, NativeUpdated, Status string
}

// CommodityView keeps the source list and the selected native contract on the
// same column grid. Widths depend only on the terminal width, never on the
// selected contract or the quotes currently visible in either panel.
type CommodityView struct {
	*tview.Flex
	Sources      *tview.Table
	Native       *tview.Table
	DetailGroups [3]*tview.Table
	Status       *tview.TextView
	details      *tview.Flex
	detailData   commodityDetailData
	surface      *commodityDetailSurface
	rule         *tview.Box
	rebuilding   bool
}

func newCommodityView() *CommodityView {
	v := &CommodityView{
		Flex:    tview.NewFlex().SetDirection(tview.FlexRow),
		Sources: createCommoditiesTable(),
		Native:  tview.NewTable().SetFixed(1, 0),
		Status:  tview.NewTextView().SetDynamicColors(true).SetWrap(false),
		details: tview.NewFlex().SetDirection(tview.FlexRow),
		rule:    analyticsRule(),
	}
	v.Native.SetBackgroundColor(tcell.ColorBlack)
	v.Status.SetBackgroundColor(tcell.ColorBlack)
	v.surface = &commodityDetailSurface{
		Box:    tview.NewBox().SetBackgroundColor(tcell.ColorBlack),
		status: v.Status,
	}
	for group, title := range [...]string{"Контракт MOEX", "Конвертация", "Обновление"} {
		table := tview.NewTable().SetEvaluateAllRows(true)
		table.SetBackgroundColor(tcell.ColorBlack)
		v.DetailGroups[group] = table
		v.surface.groups[group] = table
		v.surface.headings[group] = tview.NewTextView().SetText(title).SetWrap(false).SetTextColor(tcell.ColorLightCyan)
		v.surface.headings[group].SetBackgroundColor(tcell.ColorBlack)
	}
	v.setDetails(commodityDetailData{})
	v.details.SetBorder(true).SetTitle(" MOEX ").SetBackgroundColor(tcell.ColorBlack)
	v.details.AddItem(v.Native, 2, 0, false).AddItem(v.rule, 1, 0, false).AddItem(v.surface, 0, 1, false)
	v.AddItem(v.Sources, 0, 1, true).AddItem(v.details, 8, 0, false)
	return v
}

func (v *CommodityView) Draw(screen tcell.Screen) {
	_, _, width, height := v.GetInnerRect()
	widths := v.columnWidths(width - 2)
	_, _, detailRows := v.surface.layout(max(0, width-2))
	detailHeight := min(detailRows+5, max(0, height-3))
	v.ResizeItem(v.details, detailHeight, 0)
	// Fixed-size Flex children otherwise keep drawing past a short frame.
	innerHeight := max(0, detailHeight-2)
	nativeHeight := min(2, innerHeight)
	ruleHeight := min(1, max(0, innerHeight-nativeHeight))
	v.details.ResizeItem(v.Native, nativeHeight, 0)
	v.details.ResizeItem(v.rule, ruleHeight, 0)
	for _, table := range [...]*tview.Table{v.Sources, v.Native} {
		for row := range table.GetRowCount() {
			for col := range commodityHeaders {
				cell := table.GetCell(row, col)
				changedWidth := cell.MaxWidth != widths[col]
				cell.SetMaxWidth(widths[col]).SetExpansion(0)
				if row == 0 && changedWidth {
					// Fixed headers enforce identical natural widths in both
					// tables, even when a long data row scrolls out of view.
					if col < 2 {
						cell.SetText(padTaggedRight(commodityHeaders[col], widths[col]))
					} else {
						cell.SetText(padTagged(commodityHeaders[col], widths[col]))
					}
				}
			}
		}
	}
	v.Flex.Draw(screen)
}

func (v *CommodityView) setDetails(data commodityDetailData) {
	v.detailData = data
	labels := [...][2]string{
		{"Контракт:", "Экспирация:"},
		{"Цена:", "Дельта:"},
		{"Commodity:", "MOEX:"},
	}
	values := [...][2]string{
		{data.Contract, data.Expiry},
		{data.Converted, data.Delta},
		{data.SourceUpdated, data.NativeUpdated},
	}
	for group, table := range v.DetailGroups {
		for row := range 2 {
			value := values[group][row]
			if value == "" {
				value = "—"
			}
			table.SetCell(row, 0, tview.NewTableCell(labels[group][row]).SetTextColor(tcell.ColorGray))
			table.SetCell(row, 1, tview.NewTableCell(value).SetTextColor(tcell.ColorWhite))
		}
	}
	v.Status.SetText(data.Status)
}

// commodityDetailSurface places three key-value groups beside each other when
// they fit, then stacks them in two or one columns. Its height is established
// before the containing Flex draws, and every child is clipped to that frame.
type commodityDetailSurface struct {
	*tview.Box
	groups   [3]*tview.Table
	headings [3]*tview.TextView
	status   *tview.TextView
}

func (s *commodityDetailSurface) layout(width int) (widths [3]int, columns, rows int) {
	// Keep every group compact, including timestamps. Spare terminal width
	// belongs to the quote tables, not gaps between detail fields.
	widths = [3]int{26, 26, 32}
	columns = 1
	if widths[0]+widths[1]+widths[2]+4 <= width {
		columns = 3
	} else if max(widths[0]+widths[1], widths[2])+2 <= width {
		columns = 2
	}
	rows = 3 * ((len(s.groups) + columns - 1) / columns)
	if s.status.GetText(false) != "" {
		rows++
	}
	return
}

func (s *commodityDetailSurface) Draw(screen tcell.Screen) {
	s.DrawForSubclass(screen, s)
	x, y, width, height := s.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}
	widths, columns, rows := s.layout(width)
	for group, table := range s.groups {
		row, column := group/columns, group%columns
		groupY := y + row*3
		if groupY >= y+height {
			continue
		}
		groupX := x
		for previous := group - column; previous < group; previous++ {
			groupX += widths[previous] + 2
		}
		groupWidth := min(widths[group], x+width-groupX)
		labelWidth := [...]int{11, 7, 10}[group]
		for detailRow := range 2 {
			table.GetCell(detailRow, 0).SetMaxWidth(labelWidth)
			table.GetCell(detailRow, 1).SetMaxWidth(max(1, groupWidth-labelWidth-1))
		}
		heading := s.headings[group]
		heading.SetRect(groupX, groupY, groupWidth, 1)
		heading.Draw(screen)
		if tableHeight := min(2, y+height-groupY-1); tableHeight > 0 {
			table.SetRect(groupX, groupY+1, groupWidth, tableHeight)
			table.Draw(screen)
		}
	}
	if s.status.GetText(false) != "" && rows <= height {
		s.status.SetRect(x, y+rows-1, width, 1)
		s.status.Draw(screen)
	}
}

func (v *CommodityView) columnWidths(available int) [len(commodityHeaders)]int {
	// Reserve readable numeric fields and enough ticker space for the broker's
	// derivative names. On a narrower terminal the rightmost columns clip;
	// shrinking numbers to accommodate labels would hide the useful data.
	groupWidths, _, _ := v.surface.layout(max(0, available))
	// The one-cell table separator and two-cell group gap place Name and
	// Conversion at the same x coordinate.
	widths := [len(commodityHeaders)]int{groupWidths[0] + 1, 13, 12, 11, 9, 12}
	total := len(commodityHeaders) - 1 // tview's one-cell column separators
	for _, width := range widths {
		total += width
	}
	// On a narrow terminal, the name gives way before any numeric value.
	nameShrink := min(max(0, total-available), widths[1]-4)
	widths[1] -= nameShrink
	total -= nameShrink
	// Name absorbs spare width; ticker and numeric columns never stretch.
	widths[1] += max(0, available-total)
	return widths
}

func setCommodityHeaders(table *tview.Table) {
	for col, header := range commodityHeaders {
		align := tview.AlignRight
		if col < 2 {
			align = tview.AlignLeft
		}
		table.SetCell(0, col, tview.NewTableCell(header).SetSelectable(false).SetAlign(align).
			SetStyle(tcell.StyleDefault.Background(tcell.ColorDarkBlue).Foreground(tcell.ColorWhite).Bold(true)))
	}
}
