package ui

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"finam-terminal/commodity"
	"finam-terminal/config"
	"finam-terminal/models"
	"github.com/gdamore/tcell/v2"
)

func TestCommoditiesWithoutLinkCannotOpenOrders(t *testing.T) {
	app := NewApp(&mockClient{}, []models.AccountInfo{{ID: "acc1"}})
	t.Cleanup(app.Stop)
	cfg := config.DefaultCommodities()
	for code, c := range cfg.Commodities {
		c.MOEXMask = ""
		cfg.Commodities[code] = c
	}
	app.ConfigureCommodities(cfg)
	updateCommoditiesTable(app)
	setupInputHandlers(app)
	app.portfolioView.TabbedView.SetTab(TabCommodities)
	app.app.SetFocus(app.activeTabTable())
	for _, orderType := range []string{models.OrderTypeMarket, models.OrderTypeLimit, models.OrderTypeSLTP} {
		if err := app.SubmitOrder(OrderSubmission{Instrument: "NG@XNYM", OrderType: orderType}); err == nil {
			t.Fatalf("read-only tab accepted %s order", orderType)
		}
	}
	if err := app.SubmitClosePosition(1); err == nil {
		t.Fatal("read-only tab accepted a close")
	}
	app.OpenOrderModalWithTicker("NG@XNYM")
	if app.orderModal.GetInstrument() != "" {
		t.Fatal("read-only tab armed an order")
	}
	app.pages.RemovePage("alert")
	app.profileOpen = true
	app.profileSymbol = "NG@XNYM"
	app.profilePanel.SetReadOnly(true)
	for _, key := range []rune{'a', 'A', 'ф', 'Ф'} {
		app.app.GetInputCapture()(tcell.NewEventKey(tcell.KeyRune, key, tcell.ModNone))
		if app.orderModal.GetInstrument() != "" {
			t.Fatalf("profile key %q armed an order", key)
		}
		app.pages.RemovePage("alert")
	}
	app.profilePanel.RestoreFooter()
	if strings.Contains(app.profilePanel.Footer.GetText(true), "Order") {
		t.Fatal("read-only profile advertises trading")
	}
	app.CloseProfile()
	if app.app.GetFocus() != app.portfolioView.TabbedView.CommoditiesTable {
		t.Fatal("profile did not return focus to commodities")
	}
	app.portfolioView.TabbedView.SetTab(TabIndex)
	app.OpenOrderModalWithTicker("SBER@MISX")
	if app.orderModal.GetInstrument() != "SBER@MISX" {
		t.Fatal("read-only state leaked into Index")
	}
}

func TestCommoditiesFallbackKeepsQuotesAndStopsOnRateLimit(t *testing.T) {
	oldDelay := indexSweepDelay
	indexSweepDelay = 0
	t.Cleanup(func() { indexSweepDelay = oldDelay })
	var asked []string
	mock := &mockClient{GetQuotesFunc: func(_ string, symbols []string) (map[string]*models.Quote, error) {
		s := symbols[0]
		asked = append(asked, s)
		switch s {
		case "NG@XNYM":
			return nil, errors.New("temporary outage")
		case "BZ@IFEU":
			return map[string]*models.Quote{s: {Symbol: s, Last: "102.61"}}, nil
		default:
			return nil, status.Error(codes.ResourceExhausted, "quota")
		}
	}}
	app := NewApp(mock, nil)
	t.Cleanup(app.Stop)
	app.ConfigureCommodities(config.CommoditiesConfig{
		Enabled: true, Order: []string{"NG", "BZ", "PA"},
		Commodities: map[string]config.Commodity{
			"NG": {Symbol: "NG@XNYM", Name: "Газ", Decimals: 3},
			"BZ": {Symbol: "BZ@IFEU", Name: "Brent", Decimals: 2},
			"PA": {Symbol: "PA@XNYM", Name: "Палладий", Decimals: 2},
		},
	})
	app.commodityQuotes["NG@XNYM"] = &models.Quote{Last: "3.039"}
	app.sweepCommodityQuotes(false, true)
	if !reflect.DeepEqual(asked, []string{"NG@XNYM", "BZ@IFEU", "PA@XNYM"}) {
		t.Fatalf("fallback requests = %v", asked)
	}
	if app.commodityQuotes["NG@XNYM"].Last != "3.039" || app.commodityQuotes["BZ@IFEU"].Last != "102.61" {
		t.Fatal("partial failure lost cached or successful quotes")
	}
	app.commodityLastPoll = time.Time{}
	if app.sweepCommodityQuotes(false, true) {
		t.Fatal("rate-limited automation kept requesting")
	}
	if !app.sweepCommodityQuotes(true, true) {
		t.Fatal("manual refresh was blocked by rate-limit latch")
	}
}

func TestCommoditiesStreamPreservesPrecisionWithoutAccount(t *testing.T) {
	app := NewApp(&mockClient{}, nil)
	t.Cleanup(app.Stop)
	app.ConfigureCommodities(config.CommoditiesConfig{
		Enabled: true, Commodities: map[string]config.Commodity{
			"NG": {Symbol: "NG@XNYM", Name: "Газ", Decimals: 3},
		},
	})
	app.portfolioView.TabbedView.SetTab(TabCommodities)
	app.profileOpen = true
	app.profileSymbol = "NG@XNYM"
	app.profilePanel.Update(&models.InstrumentProfile{Symbol: app.profileSymbol})
	app.quoteInbox = map[string]*models.Quote{"NG@XNYM": {Symbol: "NG@XNYM", Last: "3.039", Change: "0.068"}}
	app.flushQuoteInbox()
	if app.profilePanel.GetProfile().Quote.Last != "3.039" {
		t.Fatal("accountless profile did not consume stream quote")
	}
	if got := app.portfolioView.TabbedView.CommoditiesTable.GetCell(1, 2).Text; got != "3.039" {
		t.Fatalf("gas price lost precision: %s", got)
	}
	if got := app.portfolioView.TabbedView.CommoditiesTable.GetCell(1, 3).Text; got != "+0.068" {
		t.Fatalf("gas change lost precision: %s", got)
	}
}

func TestCommodityOrdersUseOnlySelectedMOEXLink(t *testing.T) {
	var sent []string
	mock := &mockClient{
		PlaceOrderFunc: func(_ string, symbol string, _ string, _ float64, _ *models.OrderParams) (string, error) {
			sent = append(sent, symbol)
			return "order", nil
		},
		PlaceSLTPOrderFunc: func(_, symbol, _ string, _, _, _, _ float64) (string, error) {
			sent = append(sent, symbol)
			return "linked-order", nil
		},
	}
	app := NewApp(mock, []models.AccountInfo{{ID: "acc1"}})
	t.Cleanup(app.Stop)
	app.ConfigureCommodities(config.CommoditiesConfig{
		Enabled: true, Commodities: map[string]config.Commodity{
			"NG": {Symbol: "NG@XNYM", Name: "Газ", Decimals: 3, MOEXMask: "NG*@RTSX", RollDays: 5},
		},
	})
	app.commodityFutures = &commodityFuturesState{
		bindings: map[string]models.FutureContract{"NG@XNYM": {Symbol: "NGZ6@RTSX", Expiration: time.Now().Add(60 * 24 * time.Hour), Decimals: 3}},
		errors:   make(map[string]string),
	}
	setupInputHandlers(app)
	app.portfolioView.TabbedView.SetTab(TabCommodities)
	updateCommoditiesTable(app)
	app.app.SetFocus(app.activeTabTable())
	for _, orderType := range []string{models.OrderTypeMarket, models.OrderTypeSLTP} {
		if err := app.SubmitOrder(OrderSubmission{Instrument: "NG@XNYM", OrderType: orderType, Quantity: 1}); err == nil {
			t.Fatal("international quote was accepted for trading")
		}
		if err := app.SubmitOrder(OrderSubmission{Instrument: "BRZ6@RTSX", OrderType: orderType, Quantity: 1}); err == nil {
			t.Fatal("edited unrelated MOEX contract was accepted")
		}
		if err := app.SubmitOrder(OrderSubmission{Instrument: "NGZ6@RTSX", Direction: "Buy", OrderType: orderType, Quantity: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(sent, []string{"NGZ6@RTSX", "NGZ6@RTSX"}) {
		t.Fatalf("broker received %v", sent)
	}
	app.portfolioView.TabbedView.CommoditiesTable.GetInputCapture()(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone))
	if app.orderModal.GetInstrument() != "NGZ6@RTSX" {
		t.Fatal("table A did not select configured MOEX contract")
	}
	app.profileOpen = true
	app.profileSymbol = "NG@XNYM"
	app.profilePanel.SetReadOnly(true)
	app.profilePanel.SetOrderSymbol("NGZ6@RTSX")
	app.app.GetInputCapture()(tcell.NewEventKey(tcell.KeyRune, 'ф', tcell.ModNone))
	if app.orderModal.GetInstrument() != "NGZ6@RTSX" {
		t.Fatal("profile Ф did not select configured MOEX contract")
	}
}

func TestDisabledCommoditiesDisappearFromNavigationAndStreams(t *testing.T) {
	var subscribed []string
	mock := &mockClient{SetQuoteSymbolsFunc: func(symbols []string) { subscribed = append([]string(nil), symbols...) }}
	app := NewApp(mock, nil)
	t.Cleanup(app.Stop)
	app.ConfigureCommodities(config.CommoditiesConfig{Enabled: false})
	setupInputHandlers(app)
	capture := app.app.GetInputCapture()
	for _, want := range []TabType{TabOrders, TabIndex, TabAnalytics, TabHistory, TabPositions} {
		capture(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone))
		if got := app.portfolioView.TabbedView.ActiveTab; got != want {
			t.Fatalf("got tab %v, want %v", got, want)
		}
	}
	if strings.Contains(app.portfolioView.TabbedView.Header.GetText(true), "Commodities") {
		t.Fatal("disabled tab remains visible")
	}
	app.portfolioView.TabbedView.SetTab(TabCommodities)
	if app.portfolioView.TabbedView.ActiveTab == TabCommodities {
		t.Fatal("disabled page can be selected")
	}
	app.recomputeStreamSymbols()
	for _, symbol := range subscribed {
		if strings.Contains(symbol, "XNYM") || strings.Contains(symbol, "XCEC") || strings.Contains(symbol, "IFUS") || strings.Contains(symbol, "IFEU") {
			t.Fatalf("disabled commodity remains subscribed: %s", symbol)
		}
	}
	if app.sweepCommodityQuotes(true, true) {
		t.Fatal("disabled commodity polled quotes")
	}
}

func TestCommodityConversionRevaluesWhenOnlyFXChanges(t *testing.T) {
	app := NewApp(&mockClient{}, []models.AccountInfo{{ID: "acc1"}})
	t.Cleanup(app.Stop)
	c := config.Commodity{
		Symbol: "CC@IFUS", Name: "Какао", Decimals: 2,
		MOEXMask: "CC*@RTSX", RollDays: 5,
		Conversion: commodity.Conversion{Multiplier: 1000, FXSymbol: "USD000UTSTOM@MISX", FXOperation: "divide"},
	}
	app.ConfigureCommodities(config.CommoditiesConfig{Enabled: true, Commodities: map[string]config.Commodity{"CC": c}})
	future := models.FutureContract{Symbol: "CCX6@RTSX", Expiration: time.Now().Add(60 * 24 * time.Hour), Decimals: 1}
	app.commodityFutures = &commodityFuturesState{
		bindings: map[string]models.FutureContract{c.Symbol: future},
	}
	app.portfolioView.TabbedView.SetTab(TabCommodities)
	app.quoteInbox = map[string]*models.Quote{
		c.Symbol:              {Symbol: c.Symbol, Last: "5609", Change: "9", Volume: "100", Timestamp: time.Now()},
		future.Symbol:         {Symbol: future.Symbol, Last: "481.3", Change: "2.3", Volume: "400", Timestamp: time.Now()},
		c.Conversion.FXSymbol: {Symbol: c.Conversion.FXSymbol, Last: "84.41", Timestamp: time.Now()},
	}
	app.flushQuoteInbox()
	view := app.portfolioView.TabbedView.Commodities
	if got := view.Native.GetCell(1, 2).Text; got != "481.3" {
		t.Fatalf("native MOEX price = %q", got)
	}
	if got := view.Native.GetCell(1, 3).Text; got != "+2.3" {
		t.Fatalf("native MOEX change was replaced by international change: %q", got)
	}
	if got := view.Native.GetCell(1, 5).Text; got != "400" {
		t.Fatalf("native MOEX volume was replaced by international volume: %q", got)
	}
	assertDetails := func(converted, delta string) {
		t.Helper()
		if got := view.Sources.GetCell(1, 2).Text; got != "5 609.00" {
			t.Fatalf("conversion changed source quote price to %q", got)
		}
		if got := view.Native.GetCell(1, 2).Text; got != "481.3" {
			t.Fatalf("conversion changed native quote price to %q", got)
		}
		if got := view.Native.GetCell(1, 3).Text; got != "+2.3" {
			t.Fatalf("conversion changed native quote change to %q", got)
		}
		if got := view.Native.GetCell(1, 5).Text; got != "400" {
			t.Fatalf("conversion changed native quote volume to %q", got)
		}
		if view.Sources.GetColumnCount() != 6 || view.Native.GetColumnCount() != 6 {
			t.Fatal("quote panels retained the obsolete Updated column")
		}
		if got := view.DetailGroups[1].GetCell(0, 1).Text; got != converted {
			t.Fatalf("converted MOEX price = %q; want %q", got, converted)
		}
		if got := view.DetailGroups[1].GetCell(1, 1).Text; got != delta {
			t.Fatalf("converted MOEX minus commodity delta = %q; want %q", got, delta)
		}
	}
	assertDetails("5 701.93", "+92.93")
	app.quoteInbox = map[string]*models.Quote{c.Conversion.FXSymbol: {Symbol: c.Conversion.FXSymbol, Last: "80", Timestamp: time.Now()}}
	app.flushQuoteInbox()
	assertDetails("6 016.25", "+407.25")
	app.quoteInbox = map[string]*models.Quote{c.Conversion.FXSymbol: {Symbol: c.Conversion.FXSymbol, Last: "N/A", Timestamp: time.Now()}}
	app.flushQuoteInbox()
	assertDetails(noIndexData, noIndexData)
}

func TestCommodityRolloverDropsOldQuoteAndRejectsArmedOrder(t *testing.T) {
	var subscribed []string
	var sent []string
	now := time.Now()
	old := models.FutureContract{Symbol: "NGV6@RTSX", Name: "NG-10.26", Expiration: now.Add(24 * time.Hour), Decimals: 3}
	next := models.FutureContract{Symbol: "NGX6@RTSX", Name: "NG-11.26", Expiration: now.Add(60 * 24 * time.Hour), Decimals: 3}
	mock := &mockClient{
		SetQuoteSymbolsFunc: func(symbols []string) { subscribed = symbols },
		GetMOEXFutureFunc: func(_, mask string, rollDays int) (models.FutureContract, error) {
			if mask != "NG*@RTSX" || rollDays != 5 {
				t.Errorf("rollover request mask=%q rollDays=%d", mask, rollDays)
			}
			return next, nil
		},
		PlaceOrderFunc: func(_, symbol, _ string, _ float64, _ *models.OrderParams) (string, error) {
			sent = append(sent, symbol)
			return "order", nil
		},
	}
	app := NewApp(mock, []models.AccountInfo{{ID: "acc1"}})
	c := config.Commodity{Symbol: "NG@XNYM", Name: "Газ", Decimals: 3, MOEXMask: "NG*@RTSX", RollDays: 5}
	app.ConfigureCommodities(config.CommoditiesConfig{Enabled: true, Commodities: map[string]config.Commodity{"NG": c}})
	app.commodityFutures = &commodityFuturesState{
		bindings:  map[string]models.FutureContract{c.Symbol: old},
		updatedAt: map[string]time.Time{c.Symbol: now},
	}
	app.portfolioView.TabbedView.SetTab(TabCommodities)
	app.refreshCommodityFutureSelection()
	if app.activeCommodityOrderSymbol() != old.Symbol {
		t.Fatal("nonexpired binding disappeared before a successful rollover")
	}
	app.commodityQuotes[old.Symbol] = &models.Quote{Last: "3.1"}
	app.commodityQuotes[next.Symbol] = &models.Quote{Last: "3.5"}
	drawn := runCommodityFuturesEventLoop(t, app)
	app.app.QueueUpdateDraw(func() { app.loadCommodityFutures(false, now) })
	waitCommodityFuturesSettled(t, app, drawn)
	app.app.QueueUpdateDraw(func() {
		if err := app.SubmitOrder(OrderSubmission{Instrument: old.Symbol, OrderType: models.OrderTypeLimit, Quantity: 1}); err == nil {
			t.Error("old armed order was accepted after successful rollover")
		}
		if len(sent) != 0 {
			t.Errorf("rejected rollover order reached the broker: %v", sent)
		}
		for _, symbol := range subscribed {
			if symbol == old.Symbol {
				t.Error("old contract remained in the comparison subscription")
			}
		}
		table := app.portfolioView.TabbedView.Commodities.Native
		if got := table.GetCell(1, 0).Text; got != next.Name {
			t.Errorf("detail did not roll: %q", got)
		}
		if got := table.GetCell(1, 2).Text; got != "3.500" {
			t.Errorf("new series was drawn with old series price: %q", got)
		}
		app.selectedIdx = 1
		if app.activeCommodityOrderSymbol() != "" {
			t.Error("contract leaked into no-account state")
		}
	})
}

func commodityDetailsApp(t *testing.T) *App {
	t.Helper()
	app := NewApp(&mockClient{}, []models.AccountInfo{{ID: "acc1"}})
	t.Cleanup(app.Stop)
	app.ConfigureCommodities(config.CommoditiesConfig{
		Enabled: true, Order: []string{"NG", "ES"},
		Commodities: map[string]config.Commodity{
			"NG": {Symbol: "NG@XNYM", Name: "Природный газ", Decimals: 3, MOEXMask: "NG*@RTSX", RollDays: 2},
			"ES": {Symbol: "ES@XCME", Name: "S&P 500", Decimals: 2, MOEXMask: "SF*@RTSX", RollDays: 2, Conversion: commodity.Conversion{Multiplier: 10}},
		},
	})
	app.commodityFutures = &commodityFuturesState{
		bindings: map[string]models.FutureContract{
			"NG@XNYM": {Symbol: "NGZ6@RTSX", Name: "NG-12.26", Expiration: time.Now().AddDate(0, 0, 60), Decimals: 3},
			"ES@XCME": {Symbol: "SFZ6@RTSX", Name: "SPY-12.26", Expiration: time.Now().AddDate(0, 0, 60), Decimals: 2},
		},
	}
	at := time.Now()
	app.commodityQuotes = map[string]*models.Quote{
		"NG@XNYM":   {Last: "3.123", Change: "0.123", Volume: "100", Timestamp: at},
		"NGZ6@RTSX": {Last: "3.223", Change: "0.023", Volume: "200", Timestamp: at},
		"ES@XCME":   {Last: "7748", Change: "48", Volume: "123456", Timestamp: at},
		"SFZ6@RTSX": {Last: "776.8", Change: "1.8", Volume: "321", Timestamp: at},
	}
	updateCommoditiesTable(app)
	return app
}

func TestCommoditySelectionRedrawsNativeDetailsImmediately(t *testing.T) {
	app := commodityDetailsApp(t)
	app.portfolioView.TabbedView.SetTab(TabCommodities)
	view := app.portfolioView.TabbedView.Commodities
	if got := view.Native.GetCell(1, 0).Text; got != "NG-12.26" {
		t.Fatalf("initial native ticker = %q", got)
	}
	// The startup catalogue (and History) can name a series differently
	// from GetAsset metadata; both screens must use the catalogue name.
	future := app.commodityFutures.bindings["ES@XCME"]
	future.Name = "SPYF-12.26"
	app.commodityFutures.bindings["ES@XCME"] = future
	app.client.(*mockClient).GetInstrumentNameFunc = func(symbol string) string {
		if symbol == "SFZ6@RTSX" {
			return "SPY-12.26"
		}
		return ""
	}
	view.Sources.Select(2, 0)
	if got := view.Native.GetCell(1, 0).Text; got != "SPY-12.26" {
		t.Fatalf("selection did not immediately display broker short ticker: %q", got)
	}
	if got := view.Native.GetCell(1, 1).Text; got != view.Sources.GetCell(2, 1).Text {
		t.Fatalf("native name %q does not match source name", got)
	}
	if got := view.Native.GetCell(1, 2).Text; got != "776.80" {
		t.Fatalf("native stream price = %q", got)
	}
	if got := view.DetailGroups[1].GetCell(0, 1).Text; got != "7 768.00" {
		t.Fatalf("native index conversion = %q; want 7 768.00", got)
	}
	if got := view.DetailGroups[1].GetCell(1, 1).Text; got != "+20.00" {
		t.Fatalf("converted native-minus-source delta = %q; want +20.00", got)
	}
	if text := app.statusBar.GetText(true); !strings.Contains(text, "SFZ6@RTSX") || strings.Contains(text, "NGZ6@RTSX") {
		t.Fatalf("order shortcut still targets the previously selected contract: %q", text)
	}
}

func TestCommodityDetailsWithoutNativeQuoteDoNotConvertSource(t *testing.T) {
	app := commodityDetailsApp(t)
	view := app.portfolioView.TabbedView.Commodities
	delete(app.commodityQuotes, "SFZ6@RTSX")
	view.Sources.Select(2, 0)
	if got := view.Native.GetCell(1, 2).Text; got != noIndexData {
		t.Fatalf("missing native quote produced price %q", got)
	}
	if got := view.DetailGroups[1].GetCell(0, 1).Text; got != noIndexData {
		t.Fatalf("source-only quote produced converted MOEX price %q", got)
	}
	if got := view.DetailGroups[1].GetCell(1, 1).Text; got != noIndexData {
		t.Fatalf("missing native quote produced delta %q", got)
	}
}

func TestCommodityDetailsConversionUsesAvailableFiniteNativeQuote(t *testing.T) {
	for _, tc := range []struct {
		name, source, native string
		conversion           commodity.Conversion
		converted, delta     string
	}{
		{"both quotes", "7748", "776.8", commodity.Conversion{Multiplier: 10}, "7 768.00", "+20.00"},
		{"round only after conversion", "7748", "776.81234", commodity.Conversion{Multiplier: 10}, "7 768.12", "+20.12"},
		{"negative delta", "7800", "776.8", commodity.Conversion{Multiplier: 10}, "7 768.00", "-32.00"},
		{"missing source", "", "776.8", commodity.Conversion{Multiplier: 10}, "7 768.00", noIndexData},
		{"zero source", "0", "776.8", commodity.Conversion{Multiplier: 10}, "7 768.00", noIndexData},
		{"nonfinite source", "NaN", "776.8", commodity.Conversion{Multiplier: 10}, "7 768.00", noIndexData},
		{"missing native", "7748", "", commodity.Conversion{Multiplier: 10}, noIndexData, noIndexData},
		{"zero native", "7748", "0", commodity.Conversion{Multiplier: 10}, noIndexData, noIndexData},
		{"nonfinite native", "7748", "+Inf", commodity.Conversion{Multiplier: 10}, noIndexData, noIndexData},
		{"missing FX", "7748", "776.8", commodity.Conversion{Multiplier: 10, FXSymbol: "USD000UTSTOM@MISX"}, noIndexData, noIndexData},
		{"overflow", "7748", "1e308", commodity.Conversion{Multiplier: 10}, noIndexData, noIndexData},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := commodityDetailsApp(t)
			for i := range app.commodities {
				if app.commodities[i].Symbol == "ES@XCME" {
					app.commodities[i].Conversion = tc.conversion
				}
			}
			setQuote := func(symbol, price string) {
				if price == "" {
					delete(app.commodityQuotes, symbol)
				} else {
					app.commodityQuotes[symbol] = &models.Quote{Last: price}
				}
			}
			setQuote("ES@XCME", tc.source)
			setQuote("SFZ6@RTSX", tc.native)
			updateCommoditiesTable(app)
			view := app.portfolioView.TabbedView.Commodities
			view.Sources.Select(2, 0)
			if got := view.DetailGroups[1].GetCell(0, 1).Text; got != tc.converted {
				t.Fatalf("converted MOEX price = %q; want %q", got, tc.converted)
			}
			if got := view.DetailGroups[1].GetCell(1, 1).Text; got != tc.delta {
				t.Fatalf("converted MOEX minus commodity delta = %q; want %q", got, tc.delta)
			}
			if got := view.DetailGroups[0].GetCell(0, 1).Text; got != "SFZ6@RTSX" {
				t.Fatalf("quote availability hid bound contract %q", got)
			}
		})
	}
}

func TestCommodityDetailsKeepSeparateQuoteUpdateTimes(t *testing.T) {
	app := commodityDetailsApp(t)
	view := app.portfolioView.TabbedView.Commodities
	sourceAt := time.Now().AddDate(0, 0, -2)
	nativeAt := sourceAt.Add(-time.Hour)
	app.commodityQuotes["ES@XCME"].Timestamp = sourceAt
	app.commodityQuotes["SFZ6@RTSX"].Timestamp = nativeAt
	view.Sources.Select(2, 0)
	if got := view.DetailGroups[2].GetCell(0, 1).Text; got != sourceAt.Local().Format("02.01.2006 15:04:05") {
		t.Fatalf("commodity update = %q; want its source timestamp", got)
	}
	if got := view.DetailGroups[2].GetCell(1, 1).Text; got != nativeAt.Local().Format("02.01.2006 15:04:05") {
		t.Fatalf("MOEX update = %q; want its native timestamp", got)
	}
	app.commodityQuotes["SFZ6@RTSX"].Timestamp = time.Time{}
	delete(app.commodityQuotes, "ES@XCME")
	updateCommodityDetails(app)
	for row := range 2 {
		if got := view.DetailGroups[2].GetCell(row, 1).Text; got != noIndexData {
			t.Fatalf("missing quote/timestamp in update row %d = %q; want dash", row, got)
		}
	}
	if view.Sources.GetColumnCount() != 6 || view.Native.GetColumnCount() != 6 {
		t.Fatal("quote panels retained the obsolete Updated column")
	}
}

func TestCommodityQuoteUpdatedUsesFullLocalDateTime(t *testing.T) {
	now := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.Local)
	for _, tc := range []struct {
		name string
		q    *models.Quote
		want string
	}{
		{"no quote", nil, noIndexData},
		{"no timestamp", &models.Quote{}, noIndexData},
		{"today", &models.Quote{Timestamp: now.Add(-time.Hour).UTC()}, "04.10.2026 11:00:00"},
		{"yesterday", &models.Quote{Timestamp: now.AddDate(0, 0, -1).UTC()}, "03.10.2026 12:00:00"},
		{"different year", &models.Quote{Timestamp: now.AddDate(-1, 0, 0).UTC()}, "04.10.2025 12:00:00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := commodityQuoteUpdated(tc.q); got != tc.want {
				t.Fatalf("update timestamp = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestCommodityDetailsShowBindingStatusWithoutHidingSource(t *testing.T) {
	for _, tc := range []struct {
		name, message, want string
		loading, noMask     bool
		noState, bound      bool
	}{
		{name: "healthy", bound: true},
		{name: "no binding", want: "MOEX: контракт не найден"},
		{name: "loading", loading: true, want: "MOEX: загрузка…"},
		{name: "error", message: "ошибка [red]котировки[-]", want: "MOEX: ошибка [red]котировки[-]"},
		{name: "no mask", noMask: true, want: "MOEX: связь не настроена"},
		{name: "no state", noState: true, want: "MOEX: нет данных"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := commodityDetailsApp(t)
			sourceAt := app.commodityQuotes["ES@XCME"].Timestamp
			if !tc.bound {
				delete(app.commodityFutures.bindings, "ES@XCME")
			}
			if tc.noState {
				app.commodityFutures = nil
			} else {
				app.commodityFutures.loading = tc.loading
				app.commodityFutures.errors = map[string]string{"ES@XCME": tc.message}
			}
			if tc.noMask {
				for i := range app.commodities {
					if app.commodities[i].Symbol == "ES@XCME" {
						app.commodities[i].MOEXMask = ""
					}
				}
			}
			view := app.portfolioView.TabbedView.Commodities
			view.Sources.Select(2, 0)
			if got := view.Status.GetText(true); got != tc.want {
				t.Fatalf("binding status = %q; want %q", got, tc.want)
			}
			if got := view.DetailGroups[2].GetCell(0, 1).Text; got != commodityQuoteUpdated(&models.Quote{Timestamp: sourceAt}) {
				t.Fatalf("binding status hid source timestamp %q", got)
			}
			if tc.bound {
				return
			}
			if got := view.DetailGroups[0].GetCell(0, 1).Text; got != noIndexData {
				t.Fatalf("missing binding displayed contract %q", got)
			}
			if got := view.DetailGroups[1].GetCell(0, 1).Text; got != noIndexData {
				t.Fatalf("missing binding displayed converted price %q", got)
			}
			if got := view.DetailGroups[2].GetCell(1, 1).Text; got != noIndexData {
				t.Fatalf("missing binding displayed native timestamp %q", got)
			}
		})
	}
}

func TestCommodityDetailsClearWhenSourcesBecomeEmpty(t *testing.T) {
	app := commodityDetailsApp(t)
	view := app.portfolioView.TabbedView.Commodities
	view.Sources.Select(2, 0)
	app.commodities = nil
	updateCommoditiesTable(app)
	if got := view.Status.GetText(true); got != "Выберите международный инструмент" {
		t.Fatalf("empty source list status = %q", got)
	}
	for group, table := range view.DetailGroups {
		for row := range 2 {
			if got := table.GetCell(row, 1).Text; got != noIndexData {
				t.Fatalf("empty source list retained group %d row %d value %q", group, row, got)
			}
		}
	}
	if view.Native.GetRowCount() != 1 {
		t.Fatal("empty source list retained the previous native quote")
	}
}
