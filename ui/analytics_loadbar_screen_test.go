package ui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"finam-terminal/api"
	"finam-terminal/models"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// gateHistory makes every later pass of mock stop after its progress reports
// and wait until the test closes the returned channel: the in-flight screen is
// what these tests look at. The pass then answers with bundle and err.
func gateHistory(mock *mockClient, bundle *api.HistoryBundle, err error) chan struct{} {
	release := make(chan struct{})
	mock.LoadHistoryFunc = func(ctx context.Context, _ api.HistoryRequest) (*api.HistoryBundle, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return bundle, err
	}
	return release
}

// waitLoading waits until acc1's pass is in flight and has reported at least
// windows windows.
func waitLoading(t *testing.T, app *App, windows int) {
	t.Helper()
	if !waitFor(func() bool {
		app.dataMutex.RLock()
		defer app.dataMutex.RUnlock()
		data := app.analytics.byAccount["acc1"]
		return data != nil && data.loading && data.load.windows >= windows
	}) {
		t.Fatalf("the pass never reached %d window(s) in flight", windows)
	}
}

// loadBarApp is historyApp with the overlay pages registered, so the global
// S and Escape handlers see the front page they see at runtime.
func loadBarApp(t *testing.T, mock *mockClient, accounts ...models.AccountInfo) *App {
	t.Helper()
	app, _ := historyApp(t, mock, accounts...)
	app.pages.AddPage("main", tview.NewBox(), true, true)
	app.pages.AddPage("modal", app.orderModal.Layout, true, false)
	app.pages.AddPage("close_modal", app.closeModal.Layout, true, false)
	app.pages.AddPage("search_modal", app.searchModal.Layout, true, false)
	return app
}

// press feeds a key the way tview does: the application's capture first, then
// whatever holds focus.
func press(app *App, key tcell.Key, r rune) {
	event := app.app.GetInputCapture()(tcell.NewEventKey(key, r, tcell.ModNone))
	if event == nil {
		return
	}
	if handler := app.app.GetFocus().InputHandler(); handler != nil {
		handler(event, func(p tview.Primitive) { app.app.SetFocus(p) })
	}
}

// analyticsScreenshot draws the tab onto a 110×24 simulation screen — the size
// the track's snapshot of the old screen was taken at — and returns every cell.
func analyticsScreenshot(t *testing.T, view *AnalyticsView) [][]barCell {
	t.Helper()
	const width, height = 110, 24

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("simulation screen init: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(width, height)

	view.SetRect(0, 0, width, height)
	view.Draw(screen)

	cells := make([][]barCell, height)
	for y := range height {
		cells[y] = make([]barCell, width)
		for x := range width {
			str, style, _ := screen.Get(x, y)
			fg, bg, _ := style.Decompose()
			r := ' '
			if str != "" {
				r = []rune(str)[0]
			}
			cells[y][x] = barCell{r, fg, bg}
		}
	}
	return cells
}

func screenText(cells [][]barCell) string {
	var b strings.Builder
	for _, row := range cells {
		for _, c := range row {
			b.WriteRune(c.Char)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// assertOnlyTheBar checks that the sub-screen area holds the progress bar and
// nothing else: the middle row is the bar across the full width, and every
// other row is bare background — no status line, no frame, no table, no
// "История ещё не загружена".
func assertOnlyTheBar(t *testing.T, view *AnalyticsView, cells [][]barCell, fraction float64) {
	t.Helper()

	x, y, w, h := view.Pages.GetRect()
	if w <= 0 || h <= 0 {
		t.Fatalf("the sub-screen area is %dx%d", w, h)
	}
	middle := y + (h-1)/2

	want := barCells(fraction, w)
	for col := range w {
		if got := cells[middle][x+col]; got != want[col] {
			t.Fatalf("bar cell %d = %q %v on %v, want %q %v on %v (fraction %v)",
				col, got.Char, got.Fg, got.Bg, want[col].Char, want[col].Fg, want[col].Bg, fraction)
		}
	}
	for row := y; row < y+h; row++ {
		if row == middle {
			continue
		}
		for col := x; col < x+w; col++ {
			if c := cells[row][col]; c.Char != ' ' || c.Bg != tcell.ColorBlack {
				t.Fatalf("row %d of the sub-screen carries %q on %v; only the bar's row may be painted:\n%s",
					row-y, c.Char, c.Bg, screenText(cells))
			}
		}
	}
}

// TestLoadBar_TradesShowsOnlyTheBar: while the first pass runs, the Trades
// screen is the bar and nothing else, drawn to the pass's real progress; the
// tab strip stays, P changes the period without touching the bar, and focus
// sits on the bar. Afterwards the screen shows the data and focus returns to
// the table.
func TestLoadBar_TradesShowsOnlyTheBar(t *testing.T) {
	mock := historyMock()
	mock.LoadHistoryProgress = []api.HistoryProgress{{Done: 1, Total: 2}}
	release := gateHistory(mock, tradesBundle(), nil)

	app := loadBarApp(t, mock)
	view := app.analyticsView()

	press(app, tcell.KeyRune, '2')
	waitLoading(t, app, 1)
	updateAnalyticsHistoryScreens(app) // the redraw the progress report queued

	cells := analyticsScreenshot(t, view)
	assertOnlyTheBar(t, view, cells, 0.25) // one window of two, no bar windows yet
	text := screenText(cells)
	for _, gone := range []string{"История ещё не загружена", "Сделки за период", "По инструментам"} {
		if strings.Contains(text, gone) {
			t.Errorf("%q is on screen while the pass runs", gone)
		}
	}
	for _, kept := range []string{"Сделки", "Период"} {
		if !strings.Contains(text, kept) {
			t.Errorf("the tab strip lost %q while the pass runs", kept)
		}
	}
	if got := app.app.GetFocus(); got != tview.Primitive(view.LoadBar) {
		t.Errorf("focus is on %T while the bar is shown, want the bar", got)
	}

	// P changes the period; the bar stays.
	before := app.AnalyticsPeriod()
	press(app, tcell.KeyRune, 'P')
	if app.AnalyticsPeriod() == before {
		t.Fatal("P did not change the period during the pass")
	}
	cells = analyticsScreenshot(t, view)
	assertOnlyTheBar(t, view, cells, 0.25)
	if !strings.Contains(screenText(cells), app.AnalyticsPeriod().Label()) {
		t.Errorf("the tab strip does not show the new period %q", app.AnalyticsPeriod().Label())
	}

	close(release)
	waitHistory(t, app, mock, 1)
	updateAnalyticsHistoryScreens(app) // the redraw the end of the pass queued

	if view.ShowsLoadBar() {
		t.Fatal("the bar is still up after the pass ended")
	}
	text = screenText(analyticsScreenshot(t, view))
	for _, want := range []string{"Сделки за период", "По инструментам", "SBER"} {
		if !strings.Contains(text, want) {
			t.Errorf("%q is not on screen after the pass:\n%s", want, text)
		}
	}
	if got := app.app.GetFocus(); got != tview.Primitive(view.TradesTable) {
		t.Errorf("focus is on %T after the pass, want the trades table", got)
	}
}

// TestLoadBar_MoneyShowsOnlyTheBar: the same on the Money screen, whose focus
// belongs to its columns rather than a table.
func TestLoadBar_MoneyShowsOnlyTheBar(t *testing.T) {
	mock := historyMock()
	release := gateHistory(mock, tradesBundle(), nil)

	app := loadBarApp(t, mock)
	view := app.analyticsView()

	press(app, tcell.KeyRune, '3')
	waitLoading(t, app, 0)
	updateAnalyticsHistoryScreens(app)

	cells := analyticsScreenshot(t, view)
	assertOnlyTheBar(t, view, cells, 0)
	text := screenText(cells)
	for _, gone := range []string{"История ещё не загружена", "За период", "С открытия счёта"} {
		if strings.Contains(text, gone) {
			t.Errorf("%q is on screen while the pass runs", gone)
		}
	}
	if got := app.app.GetFocus(); got != tview.Primitive(view.LoadBar) {
		t.Errorf("focus is on %T while the bar is shown, want the bar", got)
	}

	// The overview is not the history's to cover.
	press(app, tcell.KeyRune, '1')
	if view.ShowsLoadBar() {
		t.Error("the bar covers the overview")
	}
	press(app, tcell.KeyRune, '3')
	if !view.ShowsLoadBar() {
		t.Error("the bar did not come back on returning to Money mid-pass")
	}

	close(release)
	waitHistory(t, app, mock, 1)
	updateAnalyticsHistoryScreens(app)

	text = screenText(analyticsScreenshot(t, view))
	for _, want := range []string{"За период", "С открытия счёта"} {
		if !strings.Contains(text, want) {
			t.Errorf("%q is not on screen after the pass:\n%s", want, text)
		}
	}
	if got := app.app.GetFocus(); got != tview.Primitive(view.money) {
		t.Errorf("focus is on %T after the pass, want the Money columns", got)
	}
}

// TestLoadBar_RefreshHidesTheData: R on a loaded history hides it behind the
// bar until the top-up is in. Enter, A and the arrows do nothing meanwhile —
// the table is hidden but still filled, and a key reaching it would open a
// profile or an order for a row nobody can see.
func TestLoadBar_RefreshHidesTheData(t *testing.T) {
	mock := historyMock()
	mock.LoadHistoryFunc = func(context.Context, api.HistoryRequest) (*api.HistoryBundle, error) {
		return tradesBundle(), nil
	}
	app := loadBarApp(t, mock)
	view := app.analyticsView()

	press(app, tcell.KeyRune, '2')
	waitHistory(t, app, mock, 1)
	updateAnalyticsHistoryScreens(app)
	if view.TradesTable.GetRowCount() < 2 {
		t.Fatal("the first pass left the table empty; the test needs rows to hide")
	}
	view.TradesTable.Select(1, 0)

	app.dataMutex.Lock()
	app.analytics.byAccount["acc1"].historyAt = time.Now().Add(-2 * analyticsRefreshCooldown)
	app.dataMutex.Unlock()
	release := gateHistory(mock, tradesBundle(), nil)

	press(app, tcell.KeyRune, 'R')

	assertOnlyTheBar(t, view, analyticsScreenshot(t, view), 0)
	if got := app.app.GetFocus(); got != tview.Primitive(view.LoadBar) {
		t.Errorf("focus is on %T after R, want the bar", got)
	}

	press(app, tcell.KeyEnter, 0)
	press(app, tcell.KeyRune, 'A')
	press(app, tcell.KeyDown, 0)
	if app.IsProfileOpen() {
		t.Error("Enter opened a profile from the hidden table")
	}
	if got := app.orderModal.GetInstrument(); got != "" {
		t.Errorf("A opened an order for %q from the hidden table", got)
	}
	if row, _ := view.TradesTable.GetSelection(); row != 1 {
		t.Errorf("the arrows moved the hidden table's selection to row %d", row)
	}

	close(release)
	waitHistory(t, app, mock, 2)
	updateAnalyticsHistoryScreens(app)

	if view.ShowsLoadBar() {
		t.Fatal("the bar is still up after the top-up")
	}
	if got := app.app.GetFocus(); got != tview.Primitive(view.TradesTable) {
		t.Fatalf("focus is on %T after the top-up, want the trades table", got)
	}
	// And now the keys reach the table again.
	press(app, tcell.KeyEnter, 0)
	if !app.IsProfileOpen() {
		t.Error("Enter on the table after the pass did not open the profile")
	}
}

// TestLoadBar_SearchOpenedDuringLoadKeepsFocus: the end of a pass hands focus
// back to the screen only when it is still on the bar. A search window opened
// meanwhile keeps it, and closing it lands on the screen.
func TestLoadBar_SearchOpenedDuringLoadKeepsFocus(t *testing.T) {
	mock := historyMock()
	release := gateHistory(mock, tradesBundle(), nil)

	app := loadBarApp(t, mock)
	view := app.analyticsView()

	press(app, tcell.KeyRune, '2')
	waitLoading(t, app, 0)

	press(app, tcell.KeyRune, 'S')
	if !app.IsSearchModalOpen() {
		t.Fatal("S did not open the search window during the pass")
	}

	close(release)
	waitHistory(t, app, mock, 1)
	updateAnalyticsHistoryScreens(app)

	if got := app.app.GetFocus(); got != tview.Primitive(app.searchModal.Input) {
		t.Fatalf("the end of the pass took focus from the search window to %T", got)
	}

	press(app, tcell.KeyEscape, 0)
	if app.IsSearchModalOpen() {
		t.Fatal("Escape did not close the search window")
	}
	if got := app.app.GetFocus(); got != tview.Primitive(view.TradesTable) {
		t.Errorf("closing the search window put focus on %T, want the trades table", got)
	}
}

// TestLoadBar_FollowsTheActiveAccount: every redraw decides from the account on
// screen. Switching away from an account mid-pass shows the new account's
// state, a progress report from the first one does not bring its bar back, and
// switching back does.
func TestLoadBar_FollowsTheActiveAccount(t *testing.T) {
	mock := historyMock()
	mock.LoadHistoryProgress = []api.HistoryProgress{{Done: 1, Total: 2}, {Done: 2, Total: 2}}

	// Hold the pass between its two reports.
	step := make(chan struct{})
	var mu sync.Mutex
	observed := 0
	mock.LoadHistoryObserve = func() {
		mu.Lock()
		observed++
		n := observed
		mu.Unlock()
		if n == 2 {
			<-step
		}
	}
	release := gateHistory(mock, tradesBundle(), nil)

	app := loadBarApp(t, mock,
		models.AccountInfo{ID: "acc1", FirstTradeDate: time.Now().AddDate(-1, 0, 0)},
		models.AccountInfo{ID: "acc2", FirstTradeDate: time.Now().AddDate(-1, 0, 0)},
	)
	view := app.analyticsView()

	press(app, tcell.KeyRune, '2')
	waitLoading(t, app, 1)

	selectAccount := func(idx int) {
		app.dataMutex.Lock()
		app.selectedIdx = idx
		app.dataMutex.Unlock()
		updateAnalyticsHistoryScreens(app)
	}

	selectAccount(1)
	if view.ShowsLoadBar() {
		t.Fatal("the bar of acc1's pass is shown over acc2")
	}
	if !strings.Contains(screenText(analyticsScreenshot(t, view)), "История ещё не загружена") {
		t.Error("acc2's own state is not on screen")
	}

	// acc1 reports its second window; the redraw it queues reads acc2.
	close(step)
	waitLoading(t, app, 2)
	updateAnalyticsHistoryScreens(app)
	if view.ShowsLoadBar() {
		t.Error("a progress report from acc1 brought its bar back over acc2")
	}

	selectAccount(0)
	if !view.ShowsLoadBar() {
		t.Fatal("back on acc1 mid-pass, the bar is not shown")
	}
	assertOnlyTheBar(t, view, analyticsScreenshot(t, view), 0.5)

	close(release)
	waitHistoryFor(t, app, mock, "acc1", 1)
}

// TestLoadBar_ErrorReplacesTheBar: a pass that fails ends with the screen and
// its explanation, not with a bar left standing.
func TestLoadBar_ErrorReplacesTheBar(t *testing.T) {
	mock := historyMock()
	release := gateHistory(mock, nil, errors.New("connection reset"))

	app := loadBarApp(t, mock)
	view := app.analyticsView()

	press(app, tcell.KeyRune, '2')
	waitLoading(t, app, 0)
	if !view.ShowsLoadBar() {
		t.Fatal("no bar while the pass runs")
	}

	close(release)
	waitHistory(t, app, mock, 1)
	updateAnalyticsHistoryScreens(app)

	if view.ShowsLoadBar() {
		t.Fatal("the bar is still up after the pass failed")
	}
	if text := screenText(analyticsScreenshot(t, view)); !strings.Contains(text, "R повторить") {
		t.Errorf("the failure is not explained on screen:\n%s", text)
	}
}

// TestLoadBar_NoAccountShowsNoBar: with no account on screen there is nothing
// to load and nothing to say; a bar left up from before would be a lie.
func TestLoadBar_NoAccountShowsNoBar(t *testing.T) {
	app := NewApp(historyMock(), nil)
	view := app.analyticsView()
	view.SetScreen(AnalyticsTrades)
	view.SetHistoryLoad(true, 0.5)
	view.TradeStatus.SetText("старая строка")

	updateAnalyticsHistoryScreens(app)

	if view.ShowsLoadBar() {
		t.Error("the bar stays up with no account on screen")
	}
	if got := view.TradeStatus.GetText(true); got != "" {
		t.Errorf("status = %q with no account on screen, want it cleared", got)
	}
}

// TestLoadBar_NoHistoryStartSaysSo: an account whose history start is unknown
// has no pass and no bar, and the screen says why rather than showing an empty
// status line.
func TestLoadBar_NoHistoryStartSaysSo(t *testing.T) {
	mock := historyMock()
	app := loadBarApp(t, mock, models.AccountInfo{ID: "acc1"})
	view := app.analyticsView()

	press(app, tcell.KeyRune, '2')

	if view.ShowsLoadBar() {
		t.Error("a bar is shown for an account with nothing to load")
	}
	if got := view.TradeStatus.GetText(true); !strings.Contains(got, "нет данных о начале истории") {
		t.Errorf("status = %q, want it to say the history start is unknown", got)
	}
	if n := mock.LoadHistoryCalls.Load(); n != 0 {
		t.Errorf("LoadHistory called %d times, want 0", n)
	}
}
