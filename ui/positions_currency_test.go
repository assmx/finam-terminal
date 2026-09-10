package ui

import (
	"testing"
	"time"

	"finam-terminal/models"
)

// positionsApp is an app on the Positions tab for acc1 with the given
// positions, the mock describing the instruments.
func positionsApp(t *testing.T, mock *mockClient, positions ...models.Position) *App {
	t.Helper()
	app := NewApp(mock, []models.AccountInfo{mcTestAccount()})
	t.Cleanup(app.Stop)
	setPositions(app, positions...)
	return app
}

// firstRowCell reads a cell of the first position's row; every test here holds
// one position.
func firstRowCell(app *App, col int) string {
	return app.portfolioView.TabbedView.PositionsTable.GetCell(1, col).Text
}

// Column indexes of the Positions table.
const (
	colDaily      = 4
	colValue      = 5
	colUnrealized = 6
)

// TestPositions_BondValueThroughTheFace: a bond's price is a percentage of face,
// so ten ЯНДЕКС1Р1 at 100.72 are worth 10 072, not 1 007 as before.
func TestPositions_BondValueThroughTheFace(t *testing.T) {
	app := positionsApp(t, currencyMock(
		map[string]models.InstrumentCurrency{"RU000A10BF48@MISX": {Quote: "RUB", FaceValue: 1000}},
		map[string]models.UnitValue{"RU000A10BF48@MISX": {Currency: "RUB", Value: 1019.24}},
		nil,
	), models.Position{Symbol: "RU000A10BF48@MISX", Ticker: "RU000A10BF48", Quantity: "10", AveragePrice: "100.89", CurrentPrice: "100.72", DailyPnL: "1.0", UnrealizedPnL: "-17.0"})

	updatePositionsTable(app)

	if got := firstRowCell(app, colValue); got != "10072.00" {
		t.Errorf("Value = %q, want 10072.00 through the face", got)
	}
	if got := firstRowCell(app, colUnrealized); got != "-17.0" {
		t.Errorf("Unreal P&L = %q, want the rouble figure without a code", got)
	}
}

// TestPositions_ForeignCellsCarryTheirCode: a dollar holding says so in every
// money cell, so a column never mixes currencies silently.
func TestPositions_ForeignCellsCarryTheirCode(t *testing.T) {
	app := positionsApp(t, currencyMock(map[string]models.InstrumentCurrency{"AMZN@XNGS": {Quote: "USD"}}, nil, nil),
		models.Position{Symbol: "AMZN@XNGS", Ticker: "AMZN", Quantity: "10", AveragePrice: "250", CurrentPrice: "251.68", DailyPnL: "5.00", UnrealizedPnL: "16.80"})

	updatePositionsTable(app)

	if got := firstRowCell(app, colValue); got != "2516.80 USD" {
		t.Errorf("Value = %q, want 2516.80 USD", got)
	}
	if got := firstRowCell(app, colDaily); got != "+5.00 USD" {
		t.Errorf("Daily P&L = %q, want +5.00 USD", got)
	}
	if got := firstRowCell(app, colUnrealized); got != "+16.80 USD" {
		t.Errorf("Unreal P&L = %q, want +16.80 USD", got)
	}
}

// TestPositions_ConvertedPnLCarriesTheBase: when the check finds the broker
// already sent a dollar position's result in roubles, the cells say roubles —
// the value is still in dollars.
func TestPositions_ConvertedPnLCarriesTheBase(t *testing.T) {
	app := positionsApp(t, currencyMock(map[string]models.InstrumentCurrency{"AMZN@XNGS": {Quote: "USD"}}, nil, nil),
		// 10 × (110 − 100) = 100 USD, reported as 8 400.
		models.Position{Symbol: "AMZN@XNGS", Ticker: "AMZN", Quantity: "10", AveragePrice: "100", CurrentPrice: "110", DailyPnL: "420", UnrealizedPnL: "8400"})
	setRates(app, map[string]models.FXRate{"USD": {Currency: "USD", Rate: 84, At: time.Now()}})

	updatePositionsTable(app)

	if got := firstRowCell(app, colValue); got != "1100.00 USD" {
		t.Errorf("Value = %q, want 1100.00 USD", got)
	}
	if got := firstRowCell(app, colUnrealized); got != "+8400" {
		t.Errorf("Unreal P&L = %q, want +8400 in roubles, uncoded", got)
	}
}

// TestPositions_UnresolvedBondAtThePerPieceValue: until the calendar names a
// replacement bond's face currency, it is valued at the broker's per-piece
// figure in roubles — never 85 times lower.
func TestPositions_UnresolvedBondAtThePerPieceValue(t *testing.T) {
	app := positionsApp(t, currencyMock(
		map[string]models.InstrumentCurrency{"RU000A10A851@MISX": {Quote: "RUB", FaceValue: 200000}},
		map[string]models.UnitValue{"RU000A10A851@MISX": {Currency: "RUB", Value: 16633960.33}},
		nil,
	), models.Position{Symbol: "RU000A10A851@MISX", Ticker: "RU000A10A851", Quantity: "1", AveragePrice: "96", CurrentPrice: "97.25", DailyPnL: "10", UnrealizedPnL: "2500"})

	updatePositionsTable(app)

	if got := firstRowCell(app, colValue); got != "16633960.33" {
		t.Errorf("Value = %q, want the per-piece value 16633960.33", got)
	}
}

// TestPositions_ResolvedBondInItsFaceCurrency: with the face currency known,
// the bond is valued through its face in dollars.
func TestPositions_ResolvedBondInItsFaceCurrency(t *testing.T) {
	app := positionsApp(t, currencyMock(
		map[string]models.InstrumentCurrency{"RU000A10A851@MISX": {Quote: "RUB", FaceValue: 200000}},
		map[string]models.UnitValue{"RU000A10A851@MISX": {Currency: "RUB", Value: 16633960.33}},
		map[string]string{"RU000A10A851@MISX": "USD"},
	), models.Position{Symbol: "RU000A10A851@MISX", Ticker: "RU000A10A851", Quantity: "1", AveragePrice: "96", CurrentPrice: "97.25", DailyPnL: "10", UnrealizedPnL: "2500"})

	updatePositionsTable(app)

	if got := firstRowCell(app, colValue); got != "194500.00 USD" {
		t.Errorf("Value = %q, want 194500.00 USD", got)
	}
	if got := firstRowCell(app, colUnrealized); got != "+2500 USD" {
		t.Errorf("Unreal P&L = %q, want +2500 USD", got)
	}
}
