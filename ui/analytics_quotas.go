package ui

import (
	"fmt"
	"time"

	"finam-terminal/analytics"
	"finam-terminal/models"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// quotaHeaders are the columns of the API sub-screen.
var quotaHeaders = []string{"Метод", "Лимит", "Остаток", "Сброс", "Использовано"}

// quotaLevelColor maps a quota's level to a cell colour.
var quotaLevelColor = map[analytics.Level]tcell.Color{
	analytics.LevelGood: tcell.ColorGreen,
	analytics.LevelWarn: tcell.ColorYellow,
	analytics.LevelBad:  tcell.ColorRed,
}

// updateQuotaTable redraws the quota table from the cached answer. It reads
// state only — the fetch happens once on entry and on R, never here, so the
// five-second tick can repaint this screen for free.
func updateQuotaTable(app *App) {
	table := app.portfolioView.TabbedView.Analytics.QuotaTable
	table.Clear()

	headerStyle := tcell.StyleDefault.
		Background(tcell.ColorDarkBlue).
		Foreground(tcell.ColorWhite).
		Bold(true)

	for i, h := range quotaHeaders {
		align := tview.AlignRight
		if i == 0 {
			align = tview.AlignLeft
		}
		table.SetCell(0, i, tview.NewTableCell(h).
			SetStyle(headerStyle).
			SetAlign(align).
			// Expansion must sit on every cell, header and data alike: tview
			// derives column widths from the rows currently visible, and this
			// table scrolls.
			SetExpansion(1))
	}

	app.dataMutex.RLock()
	quotas := app.analytics.quotas
	app.dataMutex.RUnlock()

	now := time.Now()
	for row, q := range analytics.SortQuotas(quotas) {
		color := quotaLevelColor[analytics.QuotaLevel(q)]
		rowNum := row + 1

		setQuotaCell(table, rowNum, 0, q.Name, tcell.ColorLightYellow, tview.AlignLeft)
		setQuotaCell(table, rowNum, 1, formatQuotaCount(q.Limit), tcell.ColorWhite, tview.AlignRight)
		setQuotaCell(table, rowNum, 2, fmt.Sprintf("%d", q.Remaining), color, tview.AlignRight)
		setQuotaCell(table, rowNum, 3, analytics.FormatReset(q.ResetAt, now), tcell.ColorWhite, tview.AlignRight)
		setQuotaCell(table, rowNum, 4, quotaUsageBar(q), color, tview.AlignRight)
	}
}

// setQuotaCell writes one cell with the shared styling.
func setQuotaCell(table *tview.Table, row, col int, text string, color tcell.Color, align int) {
	table.SetCell(row, col, tview.NewTableCell(text).
		SetTextColor(color).
		SetAlign(align).
		SetExpansion(1))
}

// formatQuotaCount renders a limit, showing a dash where the API reports none
// rather than a zero that would read as "no calls allowed".
func formatQuotaCount(limit int64) string {
	if limit <= 0 {
		return "—"
	}
	return fmt.Sprintf("%d", limit)
}

// quotaUsageBar draws how much of the window is spent. A quota with no limit
// has no share to draw, so it gets a dash instead of an empty bar that would
// read as "nothing used".
func quotaUsageBar(q models.QuotaUsage) string {
	if q.Limit <= 0 {
		return "—"
	}
	used := 1 - analytics.QuotaRemainingShare(q)
	return fmt.Sprintf("%s %s", shareBar(used), formatPercent(used))
}

// updateQuotaStatus redraws the sub-screen's status line: loading, an error
// with the retry key, the empty answer, or when the numbers were fetched.
//
// The fetch time matters because nothing refreshes these on its own — the user
// needs to know how old they are.
func updateQuotaStatus(app *App) {
	view := app.portfolioView.TabbedView.Analytics

	app.dataMutex.RLock()
	loading := app.analytics.quotasLoading
	loaded := app.analytics.quotasLoaded
	errText := app.analytics.quotasErr
	count := len(app.analytics.quotas)
	at := app.analytics.quotasAt
	app.dataMutex.RUnlock()

	switch {
	case loading:
		view.QuotaStatus.SetText("[yellow]Загрузка квот…[-]")
	case errText != "":
		view.QuotaStatus.SetText("[red]" + errText + "[-]")
	case loaded && count == 0:
		view.QuotaStatus.SetText("[gray]квоты не получены[-]")
	case loaded:
		view.QuotaStatus.SetText(fmt.Sprintf("[gray]квот: %d, запрос %s — R обновить[-]",
			count, at.Local().Format("15:04:05")))
	default:
		view.QuotaStatus.SetText("")
	}
}
