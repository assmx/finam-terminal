package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"finam-terminal/models"

	"github.com/rivo/tview"
)

// A bond's face currency is named only by its payout calendar. GetAsset's
// bond_details.currency is "%" on every bond (reconnaissance 2026-09-10) — the
// unit of the price, not a currency — and quote_currency is the settlement
// currency, which for a replacement bond is the rouble although its face is in
// dollars. The profile therefore takes the face currency from the calendar it
// already loads, shows the face alone when the calendar names none, and never
// spends a request of its own to find out.

// replacementBond is «РФ ЗО 27 Д»: a 200 000 USD face settled in roubles, the
// bond on which the quote currency read as the face currency would be wrong.
const replacementBond = "RU000A10A851@MISX"

// faceValueRow renders profile and returns what its Face Value row says, with
// the label and colour tags removed; "" when the profile has no such row.
func faceValueRow(profile *models.InstrumentProfile) string {
	panel := NewProfilePanel(tview.NewApplication())
	panel.Update(profile)
	for _, line := range strings.Split(panel.InfoPanel.GetText(true), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "Face Value"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

func TestLoadProfileSync_BondFaceCurrency(t *testing.T) {
	coupon := time.Date(2026, 11, 11, 0, 0, 0, 0, time.Local)
	dollarCoupon := []models.BondEvent{{
		Date: "2026-11-11", When: coupon, Kind: models.BondEventCoupon,
		Value: "4250.0", Currency: "USD", RecordDate: "2026-11-10", Percent: "4.25", IsFuture: true,
	}}
	unnamedCoupon := []models.BondEvent{{
		Date: "2026-11-11", When: coupon, Kind: models.BondEventCoupon,
		Value: "4250.0", RecordDate: "2026-11-10", Percent: "4.25", IsFuture: true,
	}}
	timeout := errors.New("rpc error: code = DeadlineExceeded desc = context deadline exceeded")

	tests := []struct {
		name      string
		events    []models.BondEvent
		eventsErr error
		// face and faceKnown are what BondFaceCurrencyCached answers.
		face      string
		faceKnown bool
		want      string
	}{
		{
			name:   "the calendar names the currency",
			events: dollarCoupon, face: "USD", faceKnown: true,
			want: "200000.0 USD",
		},
		{
			// The overview looked the bond up earlier in the session, so the
			// client knows its face currency although this calendar timed out.
			name:      "the calendar failed but the session already knows",
			eventsErr: timeout, face: "USD", faceKnown: true,
			want: "200000.0 USD",
		},
		{
			// An answer in its own right: the client read the calendar and none
			// of its events names a currency.
			name:   "the calendar names no currency",
			events: unnamedCoupon, face: "", faceKnown: true,
			want: "200000.0",
		},
		{
			// Nothing to show — and in particular not the RUB of the Currency
			// row, which is the settlement currency.
			name:      "the calendar failed and nobody else asked",
			eventsErr: timeout, face: "", faceKnown: false,
			want: "200000.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockClient{}
			mock.GetLotSizeFunc = func(string) float64 { return 1 }
			mock.GetAssetInfoFunc = func(_, _ string) (*models.AssetDetails, error) {
				return &models.AssetDetails{
					Ticker: "RU000A10A851", MIC: "MISX", ISIN: "RU000A10A851",
					Name: "РФ ЗО 27 Д", Type: "BONDS", Board: "TQCB",
					QuoteCurrency: "RUB", LotSize: "1", BondFaceValue: "200000.0",
				}, nil
			}
			mock.GetBondEventsFunc = func(string) ([]models.BondEvent, error) {
				return tt.events, tt.eventsErr
			}
			// The real client answers from the day cache GetBondEvents fills,
			// so a read made before the calendar request finds nothing there.
			mock.BondFaceCurrencyCachedFunc = func(symbol string) (string, bool) {
				if symbol != replacementBond || mock.GetBondEventsCalls.Load() == 0 {
					return "", false
				}
				return tt.face, tt.faceKnown
			}

			app := NewApp(mock, []models.AccountInfo{{ID: "acc1"}})
			profile := app.loadProfileSync("acc1", replacementBond, 2)

			if got := faceValueRow(profile); got != tt.want {
				t.Errorf("Face Value = %q, want %q", got, tt.want)
			}
			if n := mock.GetBondFaceCurrencyCalls.Load(); n != 0 {
				t.Errorf("GetBondFaceCurrency called %d times, want 0: the profile must not spend a request on the face currency", n)
			}
			if n := mock.GetBondEventsCalls.Load(); n != 1 {
				t.Errorf("GetBondEvents called %d times, want 1 (the calendar the profile already loads)", n)
			}
		})
	}
}
