package ui

import (
	"fmt"
	"log"
	"strings"
	"time"

	"finam-terminal/analytics"
	"finam-terminal/api"
	"finam-terminal/models"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// payoutPace separates the per-instrument calendar requests.
//
// The broker refuses a burst rather than a volume — the same measurement the
// Index tab's quote sweep is built on — so a twenty-position account walks at
// three requests a second rather than firing forty at once.
//
// A var so tests can zero it.
var payoutPace = 150 * time.Millisecond

// payoutColumns are the forecast table's headings.
var payoutColumns = []string{"Дата", "Тикер", "Вид", "На бумагу", "Кол-во", "Сумма", "Вал."}

// payoutColumnExpansion shares out the width left over. Applied to every cell,
// header and data alike, for the reason the Index tab established: tview sizes
// a column from the rows on screen, so expansion on the header alone collapses
// once the header scrolls away.
var payoutColumnExpansion = []int{2, 2, 2, 2, 1, 2, 1}

// ensurePayoutsLoaded builds the forecast on the first visit for an account.
func (a *App) ensurePayoutsLoaded() {
	account, ok := a.activeAccount()
	if !ok {
		return
	}

	a.dataMutex.RLock()
	data := a.analytics.byAccount[account.ID]
	loaded := data != nil && (data.payoutsLoading || !data.payoutsAt.IsZero())
	a.dataMutex.RUnlock()

	if loaded {
		return
	}
	a.loadPayoutsAsync(account.ID)
}

// refreshPayouts is R on the forecast screen.
//
// It always rebuilds, and it is cheap to do so: the calendars behind it are
// cached per symbol for a day, so a refresh inside that window costs no
// requests at all. There is no cooldown for the same reason.
func (a *App) refreshPayouts() {
	account, ok := a.activeAccount()
	if !ok {
		return
	}
	a.loadPayoutsAsync(account.ID)
}

// loadPayoutsAsync walks the account's long positions off the event loop,
// asking each one for the calendar its instrument type has.
//
// An instrument whose type is unknown is not asked at all: there is no calendar
// to guess at, and guessing would cost a request per position per refresh. One
// symbol failing is recorded and the walk continues; a rate-limited refusal
// ends it, because continuing into a limit is how a limit becomes a ban.
func (a *App) loadPayoutsAsync(accountID string) {
	a.dataMutex.Lock()
	data := a.analyticsAccountLocked(accountID)
	if data.payoutsLoading {
		a.dataMutex.Unlock()
		return
	}
	data.payoutsLoading = true
	data.payoutsErr = ""
	positions := append([]models.Position(nil), a.positions[accountID]...)
	a.dataMutex.Unlock()

	go func() {
		dividends := make(map[string][]models.Dividend)
		events := make(map[string][]models.BondEvent)
		var missing []string
		var limited bool

		for i, p := range positions {
			qty, ok := analytics.ParseNumber(p.Quantity)
			if !ok || qty <= 0 {
				continue
			}

			kind := payoutCalendarKind(a.client.GetInstrumentType(p.Symbol))
			if kind == payoutCalendarNone {
				continue
			}

			if i > 0 && payoutPace > 0 {
				select {
				case <-a.ctx.Done():
					return
				case <-time.After(payoutPace):
				}
			}

			var err error
			switch kind {
			case payoutCalendarDividends:
				var divs []models.Dividend
				if divs, err = a.client.GetDividends(p.Symbol); err == nil {
					dividends[p.Symbol] = divs
				}
			case payoutCalendarBondEvents:
				var evs []models.BondEvent
				if evs, err = a.client.GetBondEvents(p.Symbol); err == nil {
					events[p.Symbol] = evs
				}
			}

			if err != nil {
				if api.IsRateLimited(err) {
					limited = true
					log.Printf("[WARN] Payout calendars stopped at %s: rate limited", p.Symbol)
					break
				}
				missing = append(missing, p.Ticker)
				log.Printf("[WARN] No payout calendar for %s: %v", p.Symbol, err)
			}
		}

		payouts, totals := analytics.ExpectedPayouts(positions, dividends, events, time.Now())

		a.dataMutex.Lock()
		data.payoutsLoading = false
		data.payoutsAt = time.Now()
		data.payouts = payouts
		data.payoutTotals = totals
		data.payoutsErr = payoutStatusText(missing, limited, totals.Skipped)
		a.dataMutex.Unlock()

		a.queueDraw(func() { updatePayoutScreen(a) })
	}()
}

// payoutCalendarKind decides which calendar an instrument type has.
type payoutCalendar int

const (
	payoutCalendarNone payoutCalendar = iota
	payoutCalendarDividends
	payoutCalendarBondEvents
)

func payoutCalendarKind(assetType string) payoutCalendar {
	switch strings.ToUpper(strings.TrimSpace(assetType)) {
	case "EQUITIES", "FUNDS":
		// Funds distribute too, and the dividend calendar is where the API puts
		// it.
		return payoutCalendarDividends
	case "BONDS":
		return payoutCalendarBondEvents
	default:
		return payoutCalendarNone
	}
}

// payoutStatusText is the line under the header after a pass.
func payoutStatusText(missing []string, limited bool, skipped int) string {
	var parts []string
	if limited {
		parts = append(parts, "[yellow]лимит API — список неполный, R повторить[-]")
	}
	if len(missing) > 0 {
		parts = append(parts, fmt.Sprintf("[yellow]нет данных: %s[-]", strings.Join(missing, ", ")))
	}
	if skipped > 0 {
		parts = append(parts, fmt.Sprintf("[yellow]%d записей без даты или суммы[-]", skipped))
	}
	return strings.Join(parts, "  ")
}

// updatePayoutScreen redraws the forecast from the cache.
func updatePayoutScreen(a *App) {
	view := a.analyticsView()

	_, data, ok := a.analyticsAccountSnapshot()
	if !ok {
		view.PayoutTotals.SetText("[gray]Счёт не выбран[-]")
		return
	}

	view.PayoutStatus.SetText(data.payoutsErr)
	view.PayoutTotals.SetText(renderPayoutTotals(data))
	renderPayoutTable(view.PayoutTable, data.payouts)
}

// renderPayoutTotals writes the two summary lines.
func renderPayoutTotals(data analyticsAccountData) string {
	if data.payoutsAt.IsZero() {
		return "[gray]Загрузка ожидаемых выплат…[-]"
	}
	if len(data.payouts) == 0 {
		return "[gray]По текущим позициям ожидаемых выплат нет[-]"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[white]Ближайшие 30 дней:[-] %s\n", formatCurrencyTotals(data.payoutTotals.In30))
	fmt.Fprintf(&b, "[white]Ближайшие 90 дней:[-] %s\n", formatCurrencyTotals(data.payoutTotals.In90))
	b.WriteString("[gray]суммы до налога[-]")
	return b.String()
}

// formatCurrencyTotals renders a per-currency map as one line, in a stable
// order. Currencies are never added together.
func formatCurrencyTotals(totals map[string]float64) string {
	if len(totals) == 0 {
		return "—"
	}

	currencies := make([]string, 0, len(totals))
	for currency := range totals {
		currencies = append(currencies, currency)
	}
	sortStrings(currencies)

	parts := make([]string, 0, len(currencies))
	for _, currency := range currencies {
		parts = append(parts, fmt.Sprintf("%s %s", formatAmount(totals[currency]), currency))
	}
	return strings.Join(parts, "   ")
}

// renderPayoutTable fills the table by date.
func renderPayoutTable(table *tview.Table, payouts []analytics.Payout) {
	table.Clear()

	for col, title := range payoutColumns {
		table.SetCell(0, col, headerCell(title, payoutColumnExpansion[col]))
	}

	for i, p := range payouts {
		amount := "—"
		if p.AmountValid {
			amount = formatAmount(p.Amount)
		}

		values := []string{
			p.When.Local().Format("02.01.2006"),
			p.Ticker,
			p.Kind,
			payoutPerUnit(p),
			formatNumber(p.Quantity, 0),
			amount,
			p.Currency,
		}
		for col, text := range values {
			cell := tview.NewTableCell(text).
				SetExpansion(payoutColumnExpansion[col]).
				SetTextColor(tcell.ColorWhite)
			if col == 5 && p.AmountValid {
				cell.SetTextColor(tcell.ColorGreen)
			}
			table.SetCell(i+1, col, cell)
		}
	}

	if len(payouts) > 0 {
		table.Select(1, 0)
	}
}

// payoutPerUnit renders the per-share amount, or a dash for an offer, which
// carries a date rather than a sum.
func payoutPerUnit(p analytics.Payout) string {
	if !p.AmountValid {
		return "—"
	}
	return formatNumber(p.PerUnit, 2)
}

// selectedPayoutSymbol is the full symbol of the highlighted row, resolved
// through the rendered order. The header row and a stale selection answer "".
func (a *App) selectedPayoutSymbol() string {
	row, _ := a.analyticsView().PayoutTable.GetSelection()
	if row <= 0 {
		return ""
	}

	_, data, ok := a.analyticsAccountSnapshot()
	if !ok {
		return ""
	}

	idx := row - 1
	if idx >= len(data.payouts) {
		return ""
	}
	return data.payouts[idx].Symbol
}
