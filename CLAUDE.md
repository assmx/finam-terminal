# Finam Terminal Project

## Project Overview

**finam-terminal** is a Go-based Terminal User Interface (TUI) application designed to interact with the Finam Trade API. It demonstrates how to authenticate, retrieve account information, and fetch market data (quotes, positions) using gRPC.

### Key Technologies
*   **Language:** Go (v1.26)
*   **API Protocol:** gRPC
*   **TUI Library:** `github.com/rivo/tview`
*   **Configuration:** `github.com/joho/godotenv`
*   **SDK:** `github.com/FinamWeb/finam-trade-api/go`
*   **Testing:** `google.golang.org/grpc/test/bufconn` (in-process gRPC for integration tests)

## Architecture

The project follows a clean modular structure:

*   **`main.go`**: The entry point. Handles configuration loading, API client initialization, and starting the UI loop. Also owns the update flow: `updater.CleanupStaleBackup()` at launch, `offerPendingUpdate()` after the splash (reads the cache only — no network — and shows `ui.NewUpdatePromptApp` when a newer release is known), `go updater.Run(...)` alongside the TUI, and `installUpdate()` after `app.Run()` returns when `app.UpdateRequested()`. Every update failure prints a readable message, pauses 2s and falls through to a normal launch.
*   **`api/`**: Contains the `Client` struct and methods for interacting with the Finam gRPC services. Encapsulates the complexity of the raw API calls.
    *   `client.go`: Core client — `NewClient` creates a TLS connection, `newClientFromConn` initializes service clients (including `corporateActionsClient` for the CorporateActionsService), authenticates, starts the JWT renewal stream, and loads the asset cache. `newClientFromConn` is also used by integration tests to create clients via `bufconn` without TLS. Both `Auth` and `SubscribeJwtRenewal` requests carry `SourceAppId` set to the `sourceAppID` constant (`"finam-terminal"`). After the initial `authenticate()` call (needed for the first JWT and account list via `TokenDetails`), `subscribeJwtRenewal` keeps the token fresh by consuming the `SubscribeJwtRenewal` server stream instead of a timer — it reconnects with exponential backoff (1s, capped at 30s) if the stream drops, and stops silently when the client's context is cancelled via `Close()`. The stream is only the fast path — `token_watch.go` runs alongside it. Two API contracts shape the auth path: the connection is built with `grpc.WithDisableServiceConfig()` because Finam publishes no `_grpc_config.<host>` TXT record and the resolver would otherwise stall the first `Auth` call, and `AuthService.TokenDetails` must be called through `getUnauthenticatedContext()` — it rejects requests that also carry the `Authorization` header with `InvalidArgument` (`getContext()` stays the default for every other service). The session token is an opaque `tapi_ak_...` string, not a JWT, so its lifetime is not parsed from the token: `fetchTokenExpiry` reads `TokenDetails.expires_at` after the initial auth and on every renewal-stream token, and `TokenExpiry()` exposes it.
    *   `client.go` (Analytics mappings): three layers added by the Analytics track, all of them free — the data already arrived and was being discarded. `mapCashBalances` turns `GetAccountResponse.cash` into `models.CashBalance` (google.type.Money keeps units and nanos at the same sign, so a borrowed −300.25 simply adds up; a nil entry is dropped). `applyPortfolio` reads the `portfolio` oneof and sets `HasMarginData` **only** for MC and FORTS — `MCT` is an empty message in the proto and an absent oneof carries nothing, so a zero there means "not reported", never "zero roubles". `timestampOrZero` guards the dates, because `AsTime()` on a nil timestamp answers the Unix epoch, which reads as a real date on screen. `assetTypeCache` + `GetInstrumentType` file `Asset.Type` from the same bulk list under both the ticker and the full symbol; it is a pure cache read, which is what lets the Analytics overview call it on every tick.
    *   `client.go` (lot resolution): `getFullSymbol` resolves a position's symbol and both lot tiers, falling back to `resolveAssetLot` (`GetAsset`) for a ticker the bulk list does not know. A failed lookup is not cached, so the next miss retries — **except a refusal by construction**. `GetAccount` returns some positions without a MIC (on 2026-09-10 `FXRL`, a blocked FinEx fund, and `RU000A10AA02`, a zero-quantity bond), and when the bulk list has no entry for them `GetAsset` answers `InvalidArgument: Mic must not be empty`. Retried, that was a wasted request per such position on every five-second tick, and a second one whenever the Positions tab polled its quotes (`GetQuotes` resolves the same symbol). So `resolveAssetLot` files the ticker in `refusedSymbolCache` when a symbol **without a MIC** is refused with `InvalidArgument`, and for `refusedSymbolTTL` (24h, a var so tests can expire it) `getFullSymbol` answers the bare ticker without a request — and without the cache-miss `[DEBUG]` line. The code classifies, not the message: `InvalidArgument` says the request itself is wrong, and the same request gets the same answer. Two limits keep it narrow: a symbol that carries a MIC is never filed (an `InvalidArgument` there is about something else, the account say, and filing it would strip a MIC the bulk list knows for a day), and every other failure — `Unavailable`, `DeadlineExceeded` — is retried on the next miss as before. The position stays on screen exactly as it did, under the ticker the broker sent, with no MIC, quote or lot.
    *   `currency.go`: the instrument's money, filed from answers the terminal already receives — **zero new requests**. `storeInstrumentCurrency` keeps `models.InstrumentCurrency{Quote, FaceValue}` from every `GetAsset` (`resolveAssetLot`, `fetchAssetLotSize`, `GetAssetInfo`) under the requested keys, the answered ticker and `ticker@mic`; a successful answer is filed even when it names nothing, a failed call is not. `bond_details.currency` is deliberately **not read**: it is `"%"` on every bond (the unit of the price, not the face currency). `storeUnitValue` keeps `models.UnitValue{Currency, Value}` from every `GetAssetParams` (`fetchTradeLotSize`, `GetAssetParams`) as `long_initial_margin × 100 / long_risk_rate / trade_lot_size` (short pair as fallback; unusable → not stored) — on a bond it is the dirty price with the face and the conversion already applied. `GetInstrumentCurrency`/`GetUnitValue` are pure reads like `GetInstrumentType`. `GetBondFaceCurrency` answers a bond's face currency from, in order, the session cache, the day-long calendar cache (`calendarPeek`, free), one `GetFutureBondsEvents` and — only when nothing is scheduled — `GetPastBondsEvents` without an interval; a calendar naming no currency is an answer (`""`, cached), a failure is not. `BondFaceCurrencyCached` is its never-requesting read for redraws. `currencyCode` maps the calendar's symbols (`₽ $ € ¥`) and three-letter codes (RUR/SUR → RUB) onto ISO codes; `mapBondEvent` stores the code, so the Payouts screen shows `RUB` rather than `₽` and a rouble coupon and dividend share one total.
    *   `fx.go`: exchange rates — the Trade API has none, so a rate is the `LastQuote` of a pair to the rouble. `fxSymbols` (package var) is the reconnaissance table: `USD000UTSTOM@MISX`, `CNYRUB_TOM@MISX` (the broker values cash at the MOEX TOM trade — reconciliation to the kopeck), `EURRUB@#WWCP`, `INRRUB@#WWCP` (MISX EUR frozen since 2025-01-09), `KZT/AMD/KGS…RUB_TOM` quoted **per 100**, `BYN/TRY…RUB_TOM`. `rateFromQuote` takes `last`, else `close`, divides by the pair's unit count and refuses a non-positive/NaN price and a quote older than `fxRateMaxAge` (14d — a frozen pair still answers with its old price). `GetFXRates(currencies)` makes one `lastQuote` per currency with a pair, in the order asked, nothing for RUB/blanks/repeats/unknown codes; an ordinary failure costs that currency only, `ResourceExhausted` ends the walk and returns what arrived. It calls `lastQuote` **directly**: `GetQuotes` would resolve each pair's lot first (a `GetAsset` + `GetAssetParams` per rate).
    *   `history.go`: The account history loader. `LoadHistory` walks trades then transactions backwards from `To` in `historyChunk` (92d) windows with `historyLimit` (1000) and a `historyPace` (150ms) gap. Windows are **disjoint** — each ends one nanosecond before the previous begins — because the API interval is inclusive at both ends and a record on a shared boundary would be fetched, and FIFO-counted, twice. A response of exactly `limit` means the window was cut: the chunk is halved and both halves re-requested (the cut chunk's records are dropped first), down to `historyMinChunk` (1 day), which is accepted as-is with a `[WARN]` and marks the bundle incomplete. `historyMaxRequests` (80) bounds a pass. Nothing retries: `ResourceExhausted` → `StopRateLimited`, any other failure → `StopError`, the guard → `StopGuard`, and the partial result is always kept — only a pass that loaded nothing reports an error. `checkHistoryQuota` spends one `GetUsageMetrics` when an estimate exceeds `historyQuotaProbe` (5) and refuses the pass when a method's remaining quota is below its estimate plus `historyQuotaReserve` (20); anything unclear fails open. `quotaMatchesMethod` matches the **dot-qualified** suffix case-insensitively — the reconnaissance found `OrdersService.subscribeTrades` and `MarketDataService.latestTrades`, which a bare-name match would wrongly claim. `combinedBoundary` takes the **later** of the per-method boundaries (history is only as deep as its shallowest source), and `dedupeByID` is the backstop for a record the API sends without a timestamp, which `dropRange` cannot locate. The one `[INFO]` line per pass carries the **largest single response**: that number is what would expose a silent server-side `limit` cap, the one risk the reconnaissance could not close.
    *   `calendar_cache.go`: `calendarCached` — a per-symbol, per-kind cache with `calendarCacheTTL` (24h) behind `GetDividends`/`GetSplits`/`GetBondEvents`. A failed load is **not** cached (a bad moment would cost a day without a calendar); an empty successful one **is** (an instrument that pays nothing is an answer). It is a free generic function because Go methods cannot take type parameters. `dateValue` converts a `date.Date` to an instant for `Dividend.When`/`BondEvent.When`, answering the zero time for an absent or incomplete date so "undated" stays recognisable.
    *   `index.go`: Index composition layer — `GetIndexConstituents` (cursor pagination guarded at `maxConstituentPages`, 24h `indexCacheTTL` cache per index symbol, stale-on-error), `mapConstituent` (drops components without a full `ticker@mic` symbol), and `IsRateLimited` (a `ResourceExhausted` check the UI uses to stop automatic polling).
    *   `token_watch.go`: Session expiry watchdog — `watchTokenExpiry` re-authenticates `tokenRenewLead` (2 min) before `TokenExpiry()` and checks every `tokenWatchInterval` (30s); `shouldReauthenticate` is the pure predicate (a zero expiry means `TokenDetails` never answered, so nothing is renewed and the stream keeps the lead). It exists because `SubscribeJwtRenewal` alone is not enough: the broker counts its ~14 min delivery schedule from the subscribe and sends nothing when a stream is reopened, so every reconnect pushes the next token back while the current one keeps expiring — and a machine that slept through the session wakes with a dead token and `Unauthenticated` on every RPC. Both lead and interval are vars so tests can shorten them.
    *   `quote_stream.go` also owns the sharded subscription: `defaultQuoteShardSize` (15, measured), `maxQuoteShards`, `shardSymbols`, `reconcileShards` (diffs shards by content, so a symbol change restarts only what it affects), `quoteShard`/`runShard`, `refreshStreamState`, `SubscribedSymbols()` (symbols of *live* shards) and the adaptive safety net `IsSymbolLimitError`/`reducedSymbolCap`/`QuoteSymbolCap()`. Each shard reconnects on its own schedule via `nextShardBackoff(current, delivered)`: a subscription that actually delivered a message restarts at 1s, because a blip on a stream that had been healthy for hours is not evidence the broker is refusing it; only a subscription that never delivered anything doubles its way up to 30s. `subscriptionOutcome` carries the three facts `runShard` needs back from one subscription — voluntary (re-shard), dropped (back off), delivered (reset the delay).
    *   `quote_stream.go`: Realtime quote layer — `quoteToModel` (shared by `GetQuotes` and the stream), `mergeQuote` (snapshot replaces state, increment overwrites only non-nil fields), and the `SubscribeQuote` stream manager (`StartQuoteStream`, `SetQuoteSymbols`, `runQuoteStream`, `normalizeSymbols`, `getStreamContext`).
*   **`api/testserver/`**: In-process mock gRPC server for integration testing (see [Testing](#testing) section).
*   **`ui/`**: Manages the Terminal User Interface.
    *   `app.go`: Main `App` struct, state management, tabbed view (Positions/History/Orders/Index), and lifecycle (Run/Stop). `CloseProfile` returns focus via `activeTabTable()`, so leaving a profile lands back on the tab it was opened from. `warmLotSizeAsync` resolves the trade lot off the event loop for all three order-modal open paths (ticker/search, positions, modify), and `applyOrderModalLot`/`applyModifyModalLot` apply it only while the modal still shows the same instrument.
    *   `render.go` / `components.go`: Responsible for drawing UI elements (tables, lists, headers). `updateIndexTable` draws the Index tab from memory only (no client call from the renderer); `indexRowFromQuote` is the pure row formatter that derives the previous close as `last - change` and renders a dash instead of NaN/Inf when that close is 0. `components.go` derives the tab set from the single `tabLabels` list (`TabCount()`), so ←/→ cycle every tab without a hardcoded count, and `createIndexTable` pins the header with `SetFixed(1, 0)`. `indexColumnExpansion` is applied to **every** cell, header and data alike: tview derives column widths from the rows currently visible, so expansion living on the header alone collapses the table once the header scrolls away. `headerLabel(current, latest)` is the pure header renderer: name + running version, plus a `[yellow]⚡ <latest>[-]` segment when `updater.IsNewer` says a newer release exists (the header has `SetDynamicColors(true)`). The indicator is informational only — it deliberately carries no "press U" call to action.
    *   `data.go`: Data fetching logic for trades history and active orders. `loadDataAsync` skips quote polling for the account the live stream owns (`shouldSkipQuotePolling`) and writes results via `applyAccountData`, which keeps the cached quote map instead of replacing it with an empty one; `refreshProfileQuoteAndBars` skips only the quote leg while the stream is live (bars keep polling). `loadProfileAsync` fetches `GetAssetInfo` first, then fans out the remaining fetches in parallel; the instrument type gates which corporate-action calendars are loaded (equities: dividends + splits; bonds: bond events; each non-fatal).
    *   `index.go`: Index tab logic — the `indexList` constant (`{IMOEX@RTSX, "Индекс МосБиржи"}`; the index symbol is absent from the bulk asset list, so it cannot be discovered), `ensureIndexLoaded`/`loadIndexAsync`/`loadIndexSync`, `sortConstituents` (weight desc, alphabetical when the API sends no weights), `selectedIndexSymbol` (resolves through the rendered order), and the paced sweep that fills what the stream cannot carry: `indexPollCooldown` (60s), `indexSweepDelay`, `uncoveredIndexSymbols`, the pure `shouldPollIndexQuotes` predicate and `sweepIndexQuotes`/`pollIndexQuotesAsync`.
    *   `stream.go`: Realtime quote consumption — coalescing inbox (`onStreamQuote`/`flushQuoteInbox`, one queued `QueueUpdateDraw` at a time, quotes dropped after `Stop()`; index symbols are additionally filed into the account-independent `indexQuotes` map), `onStreamState` (`streamLive` + `[INFO]` log + guard failure counting), `shouldSkipQuotePolling` (the live stream owns the active account only), and `computeStreamSymbols`/`recomputeStreamSymbols` (positions of the active account ∪ open profile symbol ∪ the index composition **only while the Index tab is active**). The positions-stream guard lives here too: `shouldDisableIndexStream` (pure) plus `evaluateIndexStreamHealth`, which drops the index symbols for the session if the subscription does not come up within `indexStreamGuardWindow` (60s) or fails `indexStreamMaxFailures` (3) times after they joined. `onStreamState(up)` also stops the guard's clock and sets `indexStreamProven`: once the subscription has come up *while carrying the composition*, the question the guard exists to ask is settled, and a later outage with an unrelated cause (a sleeping machine, an expired session) must not be blamed on the index — without the flag every recompute restarted the window.
    *   `analytics.go`: Analytics tab state and loading. `analyticsState` holds the per-account map plus the period, which is session-wide because it describes how the user wants to look rather than what they are looking at. `SetAnalyticsScreen`/`EnterAnalyticsTab` drive the sub-screens, `analyticsScreenForDigit` maps a digit key (out of range → ignored), and `RefreshAnalytics` splits `R` per sub-screen so the overview cannot spend the history pass that belongs to Trades and Money.
    *   `analytics_render.go`: `AnalyticsView` — the whole tab **framed like every other section of the terminal**: the same border, the same title, and the same double rule tview draws when it holds focus (`Flex.HasFocus` reports true when any child does, so the frame lights up with the panel inside it). Without the frame the sub-screen bar floated at the top-left of the screen with nothing to belong to and the panels started hard against the top edge. Inside it: the sub-screen tab strip (`renderSubTabs`, width-aware so the period sits against the right edge), the `analyticsRule` under it, then a `tview.Pages`. `analyticsScreens` is the single source of truth the strip renders and the digit keys index; `Focusable()` answers which primitive takes focus, which is why `activeTabTable()` returns a `tview.Primitive`.
        `analyticsStack` is the column type, and it sizes its panels **at draw time**. Sizing them ahead of time worked until the sum of their content exceeded the space: the last panel carried on drawing past the bottom and painted over the frame. `panelRows` is what a panel asks for (logical lines + 2 border rows, capped per panel), `distributeHeights` hands the column out first come first served — priority order, since a portfolio's composition matters more than the summary under it — and a panel that cannot be given `minPanelHeight` is given nothing at all rather than a stump. A stack keeps `minTableHeight` back for the table under its panels, and `showTable` collapses a table with no rows to zero height (returning those rows to the panels) rather than drawing column titles over an empty screen. Whatever is left over goes to an unframed `analyticsSpacer` — a border drawn around empty space reads as a broken panel, the same emptiness unframed reads as margin. Panels set `SetWrap(false)`: `panelRows` counts logical lines, so a wrapped row would be screen height the panel never reserved, and a folded row destroys the column of figures it belongs to — on a terminal too narrow for the numbers, clipping at the edge is the honest degradation.
    *   `analytics_overview.go`: `updateAnalyticsOverview` builds `analytics.StructureInput`/`RiskInput`/`ValuationInput` from memory only and issues no request (a test redraws it 20 times and asserts three mock counters stay at zero). It writes **seven panels** through `setOverview` — Структура, Валюты and Секторы on the left with Итог с открытия under them, Оценка, Маржа и риск and Концентрация on the right — each rendered by its own function and then re-fitted to what it now holds. `renderValuation` leads the right column with the four figures the broker's own terminal opens its account summary with (value at the start of the day, now, the day's result, the result on open positions); `resultWithShare` puts each result's percentage in a fixed `valuationShareWidth` column so the two rows' percentages line up. The equity row moved there from `renderRisk` — the same number in two frames three rows apart was noise — and `renderRisk` no longer lists foreign balances (they moved to Валюты — one number, one frame) but keeps the red `заём <валюта>` lines and, on an MC account whose positions report it, a `ГО FORTS` row. `renderValuation` adds `без курса: N` for a day's result in a currency without a rate. The blank line between reported and derived figures is written only when something was reported above it. `positionValue` is `analytics.ValuePosition` fed the same cached money facts (`instrumentMoney` — `GetInstrumentCurrency`, `GetUnitValue`, `BondFaceCurrencyCached`, all memory reads), which the Positions table also uses, so the two screens cannot disagree on value or currency; `withCurrency` appends a non-base code to a money cell, and the P&L cells take theirs from `analytics.PnLCurrency`. `renderCurrencies` draws the Валюты panel: share rows (base first, then by value, the `номинал в валюте` row, no-rate currencies last in yellow in their own money), then a grey detail line per foreign currency — amount, rate, and `rateTimeLabel` (time today, date otherwise, yellow `курс на …` once `analytics.RateIsStale`) — and the unknown-currency / unchecked-face counters. `fxRatesLocked` copies the account-independent rates for the calculation; the redraw itself still issues no request; `sectorMapLocked` reports a missing composition explicitly rather than letting everything fall into Прочее. `writeMetric` marks a risk level with a coloured `●` as well as the colour, so the reading survives a monochrome terminal and red-green colour blindness.
    *   `analytics_fx.go`: the currency data's schedule. `fxState` (in `analyticsState`, **not** per account — a second account reuses a fresh rate) holds `rates`, `fetchedAt` (last success), `askedAt` (last attempt), `faceAskedAt` per bond, the two in-flight flags and the session latch `limited`. `ensureCurrencyData` runs on entering the overview and on every account tick while the overview is on screen; it asks for `analytics.RatesToFetch` currencies not asked within `fxRateTTL` (10 min, var) and looks up `analytics.BondsNeedingFace` bonds one at a time paced by `payoutPace`. A failure waits a TTL (never an immediate retry); `ResourceExhausted` from either loader latches the schedule with `лимит API: курсы не обновляются, R — вручную` in `OverviewStatus`. `refreshCurrencyData` is `R`: every rate not *fetched* within the TTL and every unresolved bond, whatever the schedule and the latch — a fresh rate is never re-asked. Results arriving after `Stop()` (`a.ctx.Err()`) are written nowhere; `fxStatusLocked` renders `курсы: загрузка…`/the latch.
    *   `analytics_history.go`: The per-account history cache and its loader. `ensureHistoryLoaded` fires only when the Trades or Money screen becomes visible — never on the five-second tick — and `loadAnalyticsHistoryAsync` runs one pass per account at a time under the application context, which `Stop` now cancels. `refreshHistoryTail` is `R`: a `historyTailWindow` (24h) re-read merged by id, behind an `analyticsRefreshCooldown` (10s) that *reports* how long is left rather than doing nothing. `historyHorizon` walks trades from `FirstTradeDate` and transactions from the earlier of that and `FirstNonTradeDate`, because money arrives before the first purchase. `loadBenchmark` takes two `benchmarkWindow` (14d) bar windows alongside the walk and is non-fatal. `mergeHistory` keeps the cached `Stop` reason when a fresh pass reports none — a clean top-up must not erase the fact that the original walk was truncated. `historyStatusText` renders every state; `formatHistoryDate` shows a dash for a boundary never established.
    *   `analytics_trades.go`: `updateAnalyticsHistoryScreens` is the single redraw entry point for everything reading the history cache, and issues **no** request. `renderTradeStats` leads with a `kpiRow` of the three figures the eye lands on first (result, win rate, profit factor) over two aligned columns of detail — it replaced seven ragged "Подпись: значение" pairs, which carried the same information but made finding the result mean reading every label. `maxKPIColumn` and `maxTradeDetailWidth` cap how far these spread: unlike the rows of a breakdown, a headline block does not want the whole width — three figures 40 columns apart stop being a group and become three unrelated numbers. `renderTradeTable` returns whether it drew any rows, so the caller can collapse an empty table; `selectedTradeSymbol` resolves through the *rendered* order (header row and stale selection answer `""`); `analyticsWindow`. `tradeColumnExpansion` **and** `tradeColumnAlign` are applied to every cell, header and data alike — the Index tab's lesson for the width, plus the same rule for the edge: a right-aligned column of numbers under a left-aligned heading strands the title at the far side of its own column.
    *   `analytics_money.go`: `renderPeriodMoney` (the period's flows, with the `без нереализованной части` caveat beside the result) and `writeSinceOpenBlock`/`writeBenchmarkBlock`, which the overview reuses through `renderSinceOpenSummary` so the two screens cannot disagree. `writeFlowGroups` draws each group as a bar against `largestFlow`, the biggest magnitude in the block: a column of signed numbers says which line dominated the period only after the reader compares them one by one. A securities transfer gets no bar — it carries a quantity, not money, and measuring it against a rouble scale would be meaningless. `writeSinceOpenBlock` carries no heading of its own (both panels showing it are already named by their border title) and puts a refusal's reason on its own line, because appended to the row it ran past the panel and wrapped. `sinceOpen` starts the horizon at the history's **boundary** when a pass was truncated: measuring a partial history against the full current equity would credit the account with money it cannot account for.
    *   `analytics_payouts.go`: `loadPayoutsAsync` walks long positions at `payoutPace` (150ms), asking each for the calendar its `GetInstrumentType` has (`EQUITIES`/`FUNDS` → dividends, `BONDS` → bond events, anything else → nothing). A per-symbol failure is named by ticker and the walk continues; `ResourceExhausted` ends it. `refreshPayouts` has no cooldown and does not need one — the calendars behind it are cached for a day. `renderPayoutTotals` puts both windows on one line (they are the same fact measured twice and read better side by side than stacked), and `renderPayoutTable` reports whether it drew anything so an account with nothing coming shows one sentence instead of a sentence plus an empty full-height table.
    *   `analytics_format.go`: shared rendering — `analyticsBaseCurrency`, `amountColour`/`amountTag`/`colouredAmount`, `signedAmount` (a change carries its `+`; `colouredAmount` is for totals) and `signedPercent` (two decimals, since a day's move is routinely a fraction of a percent), and the three refusals the calculations flag rather than compute (`formatShareOrNA`, `formatProfitFactor` for ∞ and Н/Д, `formatBestWorst`). A flat result is white: zero is neither good nor bad news.
    *   `analytics_style.go`: `analyticsPanel` plus the tab's visual primitives, all measured with `tview.TaggedStringWidth` so a colour tag never counts as width. **`analyticsPanel` keeps the render function rather than its output** and re-runs it whenever the width tview hands it changes, because tview only settles a panel's width while drawing: a renderer that builds its rows ahead of time has to guess, and a fixed guess is wrong in both directions — on a wide terminal the rows stop two thirds of the way across and leave the panel looking half empty, on a narrow one they run off the edge. `SetRender` renders immediately at the last known width so the caller can size the panel without waiting for a draw, and `SetStatic` is the form for content that does not care (an empty state, an error, a hint). The line count never depends on the width — a renderer only changes how a row is spread across its columns — which is what lets `fitPanel` fix the height before the width is known. `shareRow` is the breakdown row (name, value, share, bar) with the **bar taking whatever the fixed columns leave**, so a wide panel gets a long bar with the resolution to match; too narrow for a bar worth drawing and the row keeps the numbers and drops it. `sectionTitle`/`muted` fix the palette on the one `ui/profile.go` already established (cyan for structure, grey for anything the screen says about itself), so the two full-screen views read as the same application. `leaderRow` joins a label to its figure with dim leader dots across the panel's full width — the gap was the problem, a label on the left and a number fourteen spaces away being two pieces of information rather than one row. When the row will not fit, **the label gives way, never the figure**: the number is the information and the label is recoverable from the rows around it, though a label carrying colour tags is left whole because cutting one would cut a tag in half. `shareBar`/`signedBar` draw sub-cell blocks (`▏▎▍▌▋▊▉█`) with **no background fill**: the old `░` padding gave every row the same full-width grey block and turned a column of bars into noise, and a share too small to fill a cell still leaves the thinnest mark, because "tiny" and "absent" are different facts. `kpiRow` lays headline figures side by side, captions over bold values. `padTagged`/`padTaggedRight` never truncate an oversized value — breaking the column beats truncating a number.
    *   `search.go`: Dedicated search window for finding securities.
    *   `profile.go`: Full-screen instrument profile overlay with asset details, trading parameters, and chart. Renders instrument-type-specific fields (futures: expiration + contract size; options: + strike; bonds: face value + currency) and open interest in the Quote section for derivatives. Renders compact corporate-action calendar sections (equities: Dividends/Splits; bonds: Coupons/Amortization/Offers), each capped at 3 past + 3 future with a `…` overflow hint via the generic `capCalendar` helper. Instrument type is classified by `isEquityDetails`/`isBondDetails`.
    *   `chart.go`: Unicode candlestick chart renderer with smart time labels.
    *   `input.go`: Keyboard input handlers for all views (navigation, shortcuts, order actions).
    *   `modal.go`: Order placement modal with dynamic fields for Market/Limit/Stop/TP/SL+TP order types.
    *   `utils.go`: UI utility functions (number formatting, account ID masking).
    *   `update_prompt.go`: pre-TUI dialog (`NewUpdatePromptApp(current, latest).Run() bool`) shown after the splash and before `RunStartupSteps`. Every non-explicit path (Esc, default focus, a draw failure) resolves to "continue".
    *   `update_flow.go`: `RunUpdateFlow(rel)` — console progress bar in the `RunStartupSteps` style, readable Russian errors including `ManualUpdateCommand()` on `ErrNotWritable`. Returns the executable path to restart.
    *   `update_indicator.go`: `SetUpdateAvailable`/`NotifyUpdateAvailable` (the goroutine-safe variant that marshals onto the event loop and drops notifications after `Stop()`), `LatestVersion`, the `U`-key modal lifecycle, and `ConfirmUpdate`/`UpdateRequested` — the flag `main.go` acts on after `app.Run()` returns, since the process cannot replace itself while tview owns the terminal.
*   **`analytics/`**: Pure portfolio calculations over `models` — no I/O, no tview, no import of `api` or `ui`. Two rules hold throughout: nothing panics on empty or malformed broker data, and no result is ever NaN or Inf (an uncomputable value is reported through a `Valid` flag so the renderer prints `Н/Д`).
    *   `number.go`: `ParseNumber` (comma separator, rejects `N/A`, blanks and the NaN/Inf spellings `strconv` would otherwise accept), `SafeShare` (refuses a non-positive base), and `PositionValue` — the single place a holding market value is computed. The live quote wins, the broker `current_price` is the fallback, and the sign is kept. Its `faceValue` argument is the percent-of-face formula for bonds (price / 100 × face × quantity), confirmed by the 2026-09-10 reconnaissance on a live bond position; `ValuePosition` decides when it applies.
    *   `structure.go`: `Structure` — the allocation. The base is positions plus base-currency cash, **not** the broker equity (which includes margin money and would stop the shares adding up to the portfolio). Exposure is taken by magnitude so a short cannot cancel a long; a negative cash line is a loan and stays out of the base while still being reported (`Borrowed`); an empty line is reported nowhere. Every holding is valued in its own currency (`ValuePosition` over `StructureInput.Instruments`) and converted by `RateTo` over `StructureInput.Rates`, so the base is one currency: a positive foreign cash line with a rate joins the base under «Валюта» (as the broker files it), one without a rate — and a position without one (`NoRateCount`, still printed as `в других валютах`) — stays out and is shown in its own money. `Allocation.Currencies` (`CurrencyRow`: native, base value, share, rate, stale) is the breakdown by currency over the **same base** — base first, then by value with ties by code, then the `Unresolved` row of foreign-face bonds still being looked up, then the unrated currencies without a share; a property test over 500 random portfolios pins that both breakdowns share one base, that unrated holdings never move it, and that nothing is NaN/Inf. `Exposure` is the positions' part of the base, what leverage uses. Counters: `UnknownCurrencyCount`, `FaceUncheckedCount`, `FaceUnresolvedCount`. `typeGroups` is a var-table of `Asset.Type` → group with a fixed `groupOrder` (sorting by value would make rows jump between five-second ticks).
    *   `valuation.go`: `Valuation` → `Worth` — current value, the day's result (Σ `positions[].daily_pnl`), the value at the start of the day (current − day), the unrealised result with its share of cost (Σ |average price × quantity|), and `FortsMargin` (Σ `positions[].maintenance_margin`). Everything is a `Metric`, so each figure can be refused on its own. The broker leaves `daily_pnl` and `average_price` empty for FORTS positions, and that drives the rules: a partial day keeps its amount with `DailyUnreported` counting the gaps but refuses the opening value (it would be wrong by exactly the missing part); an account holding nothing has a real zero day; one position without a cost refuses the unrealised **share**, because the broker's figure covers every position; no position reporting a maintenance margin leaves `FortsMargin` invalid rather than zero. The opening value is exact only on a day no money moved — the API has no such figure, which the manual says out loud. `finiteMetric` refuses a sum or product that overflowed, and an overflowed cost refuses the share rather than dividing it down to 0%. With `Instruments`/`Rates` each position's `daily_pnl` joins the day at its value currency's rate, and the cost goes through the face for a bond and through the rate otherwise; a position whose result cannot be converted (no rate, or an unresolved face) is `DailyNoRate` — the day is partial and the opening refused, like `DailyUnreported`. `pnlConversion` holds decision 3: the broker's P&L follows quantity × price move × multiplier, so it is taken to be in the position's own currency — unless `pnlAlreadyInBase` finds the unrealised figure matches the formula only **with** the rate (2% or a kopeck tolerance), in which case the broker already converted it. `PnLCurrency` exposes that verdict for the Positions table.
    *   `currency.go`: the currency rules. `Instrument{Quote, FaceValue, Unit, Face, FaceChecked}` is the cached money facts. `ValuePosition` values a position in its own currency with a `CurrencyState`: a non-bond in `Quote` (none → the base, `CurrencyUnknown`); a bond through its face in, in order, the calendar's face currency; the quote currency when the per-piece value is within [`faceSameCurrencyMin` 0.8, `faceSameCurrencyMax` 1.3] of price × face / 100 (the gap is accrued interest); otherwise `FaceUnresolved`, valued at quantity × the broker's per-piece value (already converted — never the ~85× understatement); with nothing to check, `FaceUnchecked` in the quote currency. `NeedsFaceCurrency`/`BondsNeedingFace` say which bonds only a calendar can settle (ordinary rouble and TQOY yuan bonds cost nothing). `RateTo` converts one unit into the base — a cross through the rouble when the base is not RUB, stamped with the older leg's time. `RatesToFetch` lists the rates a portfolio needs (held currencies but RUB and the base, plus the base's own leg), so a rouble account needs none. `RateIsStale` with `FXRateStaleAfter` = 12h.
    *   `risk.go`: `RiskMetrics`, `UtilizationLevel`, `CushionLevel`. Equity is checked once up front, so all three derived figures fall invalid together. MC yields all three; FORTS yields utilisation only (it reports no maintenance level and no basis for leverage); MCT and an absent oneof yield none. A figure exactly on a threshold takes the more cautious colour, and the cushion bands mirror the utilisation boundary for boundary.
    *   `fifo.go`: `MatchFIFO` pairs each exit with the oldest open lot per symbol, sorting its input first (the loader delivers trades newest-first, in pieces) with the trade id as the tiebreak. A trade's accrued interest is spread over its quantity and folded into the per-unit price — paid on a purchase, received on a sale — so a bond round trip at an unchanged quote still shows a result. A trade larger than what is open closes what it can and opens the other way (a reversal, not a gap); `Unmatched` counts only a sale that found nothing open, which is the symptom of history starting mid-position. Two limits are documented at the field: a genuine opening short is indistinguishable from that and is counted too, and the mirrored case (a purchase covering a short whose sale predates the history) is not counted at all, because an opening long and a truncated cover look identical. `Reconcile` reports where the implied position disagrees with the broker's and never corrects it — the disagreement is the signal.
    *   `period.go`: `Preset` (Month/Quarter/Year/YTD/All), `DefaultPreset` = Quarter, `Label`, `Next`, `Range`, `InRange`. YTD turns at the **viewer's** midnight, not UTC's. `PresetAll` falls back to a twenty-year horizon for a zero or future opening date, because a reversed interval is what the API rejects outright and an empty one shows nothing with no explanation. `InRange` rejects the zero time, so one mis-dated record cannot land in every period at once.
    *   `stats.go`: `Stats` (by currency, never summed across them — the API carries no exchange rates) and `PerInstrument`. Closed trades are filtered on their **exit**. A flat trade is excluded from the win rate and included in expectancy. The profit factor carries two flags rather than a sentinel: infinite (gains, no losses) and invalid (nothing either side) are different states and print differently. `Stats` takes a `baseCurrency` the plan's signature did not have — the reconnaissance fixture shows `AccountTrade.Currency` populated on a bond and blank on both equities, so a blank must land somewhere named.
    *   `merge.go`: `MergeTrades`/`MergeTransactions` over a shared generic body. The **cached** copy wins a duplicate id: it came from a pass that completed, while the tail comes from a narrow re-read that a rate limit can cut short. A record with no id is always kept — collapsing them would delete real trades to remove a duplicate that may not exist.
    *   `cashflow.go`: `FlowGroup` (**not** `Group`, which is already an allocation row), `FlowGroupOrder`, `Classify`, `Flows`. The trade rule comes **first** in `Classify`: a transaction carrying a trade goes to `GroupTrade` whatever its category, which is what stops the realised result being counted twice. Signs are kept as the API sends them. `TransferQty` sits on `CashFlow` rather than inside a currency — shares are not money, and a quantity in a currency bucket is one careless sum away from becoming a deposit.
    *   `xirr.go`: `XIRR` by bisection over [−0.99, 10] to 1e-7, ACT/365 from the earliest flow. Bisection rather than Newton's method because it cannot diverge or find a second root. The bracket deliberately excludes −100%: a total loss has no finite annual rate and the boundary would read as actionable. `XIRRStatus` names every refusal (short horizon, no sign change, no root) so the screen never prints a bare `Н/Д`. `SinceOpenResult` treats **only** deposits and withdrawals as flows — a commission or dividend never crosses the account boundary — converts the sign at that one boundary, and reports other currencies separately rather than folding them in.
    *   `benchmark.go`: `BenchmarkFromBars` from two narrow windows, built directly on two reconnaissance facts: the daily timeframe refuses an interval wider than **366 days**, and a **seven-day** window over the Russian New Year holidays returns zero bars. The window is therefore 14 days, which in turn means bars *before* the horizon arrive and must be skipped rather than taken as the opening price. Every degenerate input answers `false` — a screen saying "нет данных" beats a comparison that is quietly wrong.
    *   `payouts.go`: `ExpectedPayouts` over long positions only (a short pays rather than receives). The **undated check runs before the past check**, and the order is load-bearing: a zero time is before every date, so testing for the past first files an undated record as "already paid" and drops it silently. Dates compare against `dayStart(now)` so a payout dated today does not vanish at midnight. `applyPerUnit` is the single place the money side is filled in, and it refuses a **non-positive** value: a sum the broker reports as zero is not a payment of nothing, it is a payment whose size is not set yet — which is how a floating-rate bond's next coupon arrives, dated but not yet priced. Such a payout keeps its date, goes without an amount, stays out of the 30/90 totals and is **not** counted as skipped, because it is shown rather than dropped. An offer is listed the same way for the same reason, being a date the holder may act on rather than a payment. Amounts are gross, because the API reports nothing about the withholding rate.
*   **`config/`**: Handles loading environment variables from `.env` or system environment.
*   **`models/`**: Shared data structures used across the application to represent accounts, quotes, positions, trades, and orders. Key fields include `LotSize` and `Name` for instrument metadata. `AssetParams.TradeLotSize` (int64, 2.18.1) carries the broker's trade lot; 0 means the API has no value. `AccountInfo.LoadError` is set when an account fails to load from the broker. `Position.DailyPnL` holds the position's daily P&L and `Position.MaintenanceMargin` its collateral; the broker fills the latter for FORTS positions only, and every absent decimal arrives as `"N/A"` — "not reported", never zero. `Order` includes extended fields for stop/limit prices, conditions, validity, and SL/TP quantities, plus `TriggeredOrderID` (the exchange order a stop spawned, 2.17.0). `Trade` carries `AccruedInterest` and `Currency` (bond НКД + price currency, 2.16.0). `Quote.Change` carries the broker's session change (`quote.change` = last − close), delivered with every `LastQuote` and `SubscribeQuote` message. `IndexConstituent{Symbol, Ticker, Name, Sector, Weight}` is one component of a stock index; `Weight` is 0 when the API sends none and is used only for ordering, since its normalisation is undocumented. `CashBalance{Currency, Amount}` is one currency line of `GetAccountResponse.cash` (negative = a margin loan), and `AccountInfo` carries it alongside `PortfolioKind`, `AvailableCash`, `InitialMargin`, `MaintenanceMargin`, `MoneyReserved`, `HasMarginData` and the two first-transaction dates; `HasMarginData` is false for MCT and an absent oneof, where a zero means "not reported". `SecurityInfo.Type` is `Asset.Type` verbatim. `Transaction`/`TransactionTrade` is one row of `AccountsService.Transactions`: `Category` is the **enum name** (`DEPOSIT`, `COMMISSION`, …) rather than the free-text field so grouping switches on a closed set, `Amount` keeps the API's sign, `ChangeQty` carries the securities count only a `TRANSFER` populates, and a non-nil `Trade` marks the money side of a deal that must stay out of every cash total. `Dividend.When`/`BondEvent.When` carry the date as an instant beside the formatted string, so the payout screen never reparses a display string; `BondEvent.Currency` is an ISO code (the calendar sends a symbol). `InstrumentCurrency{Quote, FaceValue}` (no face currency — `bond_details.currency` is `"%"`), `UnitValue{Currency, Value}` (one piece in its settlement currency, from the margin) and `FXRate{Currency, Rate, At}` (roubles per unit, the quote's time) carry the currency layer. `QuotaUsage{Name, Limit, Remaining, ResetAt}` is one row of the API quota table, read by the history loader's quota pre-check; a zero `ResetAt` means the API sent none, which is the normal state for an untouched quota. Corporate-action calendar types `Dividend`, `Split`, and `BondEvent` (flat pre-formatted strings; `BondEvent.Kind` ∈ {Coupon, Amortization, Offer} selects the populated detail group) are surfaced via `InstrumentProfile.Dividends`/`Splits`/`BondEvents`.
*   **`version/`**: Build-time version metadata. Exposes `Version`, `Commit`, and `BuildDate` as **package-level vars** (not consts — the linker can only override vars via `-ldflags -X`). `String()` returns the display string used by the UI header: a release tag verbatim (`v1.2.3`), or a dev build with VCS info (`dev (a1b2c3d)` or `dev (a1b2c3d, dirty)`), falling back through `runtime/debug.ReadBuildInfo()` when no commit is injected. `Info()` returns the raw tuple for diagnostics.
*   **`updater/`**: Self-update machinery, standard library only (no new `go.mod` dependency). Gated entirely on `updater.IsRelease(version.Version)` — a `dev` build performs no request, writes no file and shows nothing.
    *   `semver.go`: `IsRelease`, `Compare`, `IsNewer` — a hand-rolled semver parser (optional `v` prefix, pre-release below its release, build metadata ignored). `IsNewer` returns false unless **both** sides are release versions, so a dev build is never nagged and a locally-newer build is never asked to downgrade.
    *   `state.go`: `State` + `LoadState`/`SaveState` over `~/.finam-cli/update.json` (same directory as the token `.env`, resolved via `config.UserConfigDir()`). Written atomically (temp + rename); a missing, empty or corrupt file degrades to the zero state with a `[WARN]` so a bad cache can never block startup.
    *   `github.go`: `Release`/`Asset` + `FetchLatestRelease` against `/repos/updevru/finam-terminal/releases/latest`. Unauthenticated (60 req/h/IP vs one check per day), 10s timeout, `apiBaseURL` is a package var so tests point it at `httptest`.
    *   `checker.go`: `ShouldCheck(state, now)` (24h window, zero time = due) and `Run(ctx, current, onNewVersion)` — the background loop. A failed check deliberately does **not** advance `LastCheck`.
    *   `asset.go`: `AssetName(goos, goarch)` mirroring the `release.yml` build matrix; platforms outside it (`linux/arm64`, `windows/arm64`) return an error naming the platform.
    *   `download.go`: streaming download through `io.MultiWriter(file, sha256)`, verified against `checksums.txt` (parser handles both the `␠␠` and `␠*` sha256sum separators) with a fallback to the asset size for releases predating it. 5 minute timeout; the partial file is removed on every failure path.
    *   `apply.go`: `SelfUpdate` (resolve exe → `ensureWritable` → download to `.finam-terminal-update-<pid>.tmp` in the exe's own directory → verify → `chmod 0755` on Unix → replace), `replaceExecutable` (`exe→exe.old`, `tmp→exe`, rollback on failure; the backup is removed on Unix and left for `CleanupStaleBackup` on Windows), and `ManualUpdateCommand()`. **Invariant: on any failure the existing binary is byte-for-byte unchanged.**
    *   `restart.go` + `restart_unix.go`/`restart_windows.go`: `Restart(exePath)` — `syscall.Exec` on Unix (same PID and terminal), child process + exit on Windows. Build tags follow `platform/console_*.go`; the exec call sits behind the `execRestart` var so tests assert argv/env without replacing the process.

## Getting Started

### Prerequisites
*   Go 1.26 or higher
*   Finam Trade API Token (obtain from Finam Developer Portal)

### Installation

1.  Clone the repository.
2.  Install dependencies:
    ```bash
    go mod tidy
    ```

### Configuration

The application requires an API token, obtained from [api.finam.ru/tokens/](https://api.finam.ru/tokens/). New tokens use the short `tapi_sk_...` format; old long tokens are accepted too (Legacy) — `authenticate()` passes whatever the user entered as `Secret` unchanged, so no client-side format handling is needed.

1.  Copy the example configuration:
    ```bash
    cp .env.example .env
    ```
2.  Edit `.env` and add your token:
    ```env
    FINAM_API_TOKEN=your_actual_token_here
    ```

### Building and Running

**Run directly:**
```bash
go run main.go
```

**Run with specific account (by index):**
```bash
go run main.go -account 0
```

**Build executable:**
```bash
go build -o finam-trade.exe main.go
./finam-trade.exe
```

**Build with version metadata (recommended for local distribution):**
```bash
make build
```
The `build` target injects `git describe --tags --always --dirty` as `Version`, `git rev-parse HEAD` as `Commit`, and the current UTC time as `BuildDate` via `-ldflags -X` against the `version` package. The resulting binary shows the resolved version in the TUI header.

If you skip `make` and use a plain `go build .` (note the `.`, not `main.go` — `main.go` does not embed `vcs.*` settings), the binary still falls back to `runtime/debug.ReadBuildInfo()` and renders `dev (<short-sha>)` (or with `, dirty` when the working tree has changes).

### Releasing a New Version

To cut a release, just push a `vX.Y.Z` git tag — `.github/workflows/release.yml` is triggered on `push: tags: 'v*'` and will:

1. Build the binary for each `(GOOS, GOARCH)` matrix entry with `-ldflags "-X finam-terminal/version.Version=${{ github.ref_name }} -X finam-terminal/version.Commit=${{ github.sha }} -X finam-terminal/version.BuildDate=<UTC>"` so each artifact reports the tag in the UI header.
2. Upload the artifacts and create a GitHub Release with auto-generated notes.
3. Build and push the Docker image, tagged via `docker/metadata-action`.

**Steps:**
```bash
git tag v1.2.3
git push origin v1.2.3
```

That's it — no manual constant bumps anywhere in source.

## Development Conventions

*   **Style:** Standard Go formatting (`gofmt`).
*   **Logging:** Use standard `log` package with prefixes like `[INFO]` and `[ERROR]`.
*   **UI Updates:** The TUI is event-driven. Ensure UI updates happen on the main thread or using `app.QueueUpdateDraw` (implied by `tview` usage).
*   **Configuration:** Always use `config.Load()` to access settings; do not hardcode credentials.

## Testing

The project has two layers of automated tests: **unit tests** and **integration tests**.

### Running Tests

```bash
# Unit tests only (default, no build tags required)
go test ./...

# Integration tests (against mock gRPC server via bufconn)
go test -tags=integration ./api/...

# All tests together
go test ./... && go test -tags=integration ./api/...

# With race detector (requires CGO_ENABLED=1)
CGO_ENABLED=1 go test -race ./...
CGO_ENABLED=1 go test -tags=integration -race ./api/...
```

A `Makefile` is available with shortcuts: `make test`, `make test-integration`, `make test-all`, `make test-race`, `make coverage`, `make lint`.

### Unit Tests

Unit tests use manual mock structs that implement gRPC service client interfaces (defined in `api/client_test.go`). They test individual methods in isolation without network I/O.

### Integration Tests

Integration tests use build tag `//go:build integration` and are located in `api/client_*_integration_test.go`. They exercise the real `api.Client` lifecycle (connect, authenticate, cache, call methods, close) against an in-process mock gRPC server.

**Mock gRPC Server** (`api/testserver/`):
*   `server.go` — `TestServer` struct: creates a `grpc.Server` + `bufconn.Listener`, registers all 7 mock services, exposes `Start()`, `Stop()`, `Dial()`.
*   `auth_server.go` — `MockAuthServer`: validates tokens, generates JWTs with configurable expiry, tracks call count via `AuthCallCount` and notifies via `AuthCalled` channel. Supports `AuthOverride` for per-call error injection. `TokenDetails` mirrors the real API: it rejects calls carrying an `Authorization` header with `InvalidArgument` and reports `created_at`/`expires_at` (window controlled by `TokenExpiry`, default 1h), so the startup-auth regression is caught end to end.
*   `accounts_server.go` — `MockAccountsServer`: returns configurable positions, trade history and `TransactionHistory` per account ID (named for symmetry with `TradeHistory`, and because a field called `Transactions` would collide with the service method). `Trades` and `Transactions` **honour the request interval and limit**, without which the history loader's chunk splitting could not be tested at all; `TradesLimitCap`/`TransactionsLimitCap` stand in for the server-side cap the reconnaissance could not measure, and truncation drops the oldest records while restoring the fixture order (the real API's ordering was never observed, so no caller may depend on it). Per-method error injection, call counters and last-request capture. Plus `Portfolios` — an `AccountPortfolio` per account carrying the cash list, the portfolio oneof branch and the first-transaction dates. The oneof branches are held as concrete messages because the generated interface for them is unexported; `GetAccount` wraps whichever is set. `DefaultMCPortfolio()` (ACC001, two currencies with the second negative) and `DefaultFORTSPortfolio()` (ACC002) cover both shapes, and an account with no fixture answers with an empty oneof — itself a case worth serving.
*   `marketdata_server.go` — `MockMarketDataServer`: returns quotes and bars. Supports `QuoteOverride` for custom behavior. `SubscribeQuote` is driven by `QuoteStreamQueue` (`QuoteStreamItem{Quotes, StreamErr, Err}`, cap 100) like the JWT renewal mock, and records `QuoteStreamCallCount`, `QuoteStreamCalled`, `LastQuoteStreamSymbols`; `SubscribeQuoteOverride` replaces the default loop. `LastQuoteCallCount` and `LastQuoteCallsFor(symbol)` count unary quotes, overridden ones included — the rate budget promises one per currency.
*   `assets_server.go` — `MockAssetsServer`: returns bulk assets, per-symbol details, trading parameters, schedule, and index constituents. Supports error injection via `GetAssetError`, `GetAssetParamsError`, `ScheduleError`, `GetConstituentsError`, plus `EmptyConstituents`, `EndlessConstituents` (a cursor that never terminates) and the `GetConstituentsCallCount` counter that proves the composition cache prevents repeat RPCs.
*   `orders_server.go` — `MockOrdersServer`: records `PlaceOrder`, `PlaceSLTPOrder`, `CancelOrder` requests for assertion. Returns configurable active orders.
*   `usagemetrics_server.go` — `MockUsageMetricsServer`: the seventh service. Serves `DefaultQuotas()` (three quotas at different fill levels and one with no `reset_time`), with `GetUsageMetricsError` for injection and `GetUsageMetricsCallCount` for the history pre-check's "at most one probe per pass" promise.
*   `corporateactions_server.go` — `MockCorporateActionsServer`: implements all 6 CorporateActionsService methods, serving separate past/future fixtures per calendar kind with per-kind error injection (`DividendsError`/`SplitsError`/`BondEventsError`) and per-RPC counters (`DividendCalls`/`SplitCalls`/`BondEventCalls`) — the only way to show that a cached calendar costs nothing. `BondCalendars` overrides the bond fixtures per symbol, so each bond can carry its own face currency.
*   `testdata.go` — Fixture functions: `MakeJWT()`, `DefaultAssets()`, `DefaultAccountPositions()`, `DefaultQuote()`, `DefaultBars()`, `DefaultOrders()`, `DefaultTrades()`, `DefaultAssetInfo()`, `DefaultAssetParams()`, `DefaultSchedule()`, `DefaultConstituents(cursor)` (two chained pages, Russian names/sectors, weights out of API order, last entry weightless), `DefaultDividends()`, `DefaultSplits()`, `DefaultBondEvents()` (the last three return `(past, future)` and cover all BondEvent oneof branches + nil pointer wrappers), `DefaultStreamQuote(symbol, snapshot)` (full state with `IsDataSnapshot`, or a `Last`+`Timestamp` increment). `DefaultAssetParams()` reports `TradeLotSize: 5` deliberately against `DefaultAssetInfo`'s `LotSize: "10"`, so lot priority is proven end to end. `DefaultTransactions()` covers every category the cash-flow grouping switches on plus the four shapes that are easy to get wrong: a charge (negative money), a securities transfer (a quantity and no money), a transaction reflecting a trade, and a foreign-currency deposit. `LowTradesQuota()` is a quota table whose `AccountsService.trades` entry cannot cover a long pass, alongside the two names (`OrdersService.subscribeTrades`, `MarketDataService.latestTrades`) that make bare-suffix matching wrong. The currency reconnaissance instruments are served by `DefaultAssetInfo`/`DefaultAssetParams` for their symbols (`lookupCurrencyInstrument`, fresh messages per call): «ОФЗ 33 CNY» (TQOY, `quote_currency` CNY), «РФ ЗО 27 Д» (200 000 face, RUB settlement, margin ≈ 85× price × face / 100), «ГПБ3P6CNY» (yuan face, RUB settlement) and YDEX, each with its real margin pair; `CurrencyAccountPositions()` holds them, `CurrencyBondCalendars()` gives them `$`/`¥` coupons (one with nothing scheduled to force the past fallback), and `fxQuotes()` (behind `DefaultQuote`) serves live USD/CNY/EUR pairs, KZT per 100 and a frozen `EUR_RUB__TOM@MISX` from January 2025. `DefaultTrades()` and `DefaultTransactions()` are anchored **relative to now**: `GetTradeHistory` asks for the last 30 days and the mock now honours the interval, so a hard-coded date would silently age out of its own window.

**Test helper**: `setupTestServer(t)` in `api/client_integration_test.go` creates a `TestServer` + `Client` pair and registers cleanup.

**Integration test files**:
*   `client_integration_test.go` — Client lifecycle, accounts, market data, search, orders (20 tests).
*   `client_cache_integration_test.go` — Asset cache population, lot size on-demand fetch, name lookup (5 tests).
*   `client_token_refresh_integration_test.go` — Auto-refresh before expiry, retry on failure, stop on close (3 tests).
*   `client_token_watch_integration_test.go` — The expiry watchdog re-authenticates while the renewal stream stays silent (1 test).
*   `client_errors_integration_test.go` — Unauthenticated, NotFound, ServerUnavailable, DeadlineExceeded, empty response (5 tests).
*   `client_corporate_actions_integration_test.go` — Dividend/split/bond-event calendars: past+future merge, ascending date sort, `IsFuture` flags, oneof mapping (coupon/amortization/offer), nil-safe wrappers (3 tests).
*   `client_index_integration_test.go` — Index composition: pagination + API order, ticker/weight mapping, a cache hit costing 0 RPCs, stale-on-error, first-load error, empty-is-error without poisoning the cache, the page guard, plus `GetQuotes` rate-limit abort vs ordinary per-symbol skip (9 tests).
*   `client_account_fields_integration_test.go` — Cash, the portfolio oneof and the dates over bufconn: MC, FORTS, and an account with no fixture; plus the per-position maintenance margin, filled on a FORTS position and `"N/A"` on a stock one (4 tests).
*   `client_usage_metrics_integration_test.go` — Quota table: mapping with the API order preserved, a rate-limited error, an empty list (3 tests).
*   `client_instrument_type_integration_test.go` — `GetInstrumentType` by symbol and by ticker, blank and unknown, with the bulk call pinned at exactly one per session (1 test).
*   `client_transactions_integration_test.go` — Transactions over bufconn: every category plus the charge/transfer/trade/foreign-currency shapes, interval filtering, limit truncation, error code, unknown account (5 tests).
*   `client_trades_integration_test.go` — `GetTrades` and the `GetTradeHistory` wrapper: same window and records, interval filtering, truncation keeping the newest, a **server-side cap below the requested limit** (the shape that would defeat a naive `len == limit` check), error code (6 tests).
*   `history_integration_test.go` — `LoadHistory` end to end: one request per chunk per method, no duplicates across chunk boundaries, splitting against a server that truncates for real, a rate limit keeping partial data, a quota table blocking the pass, the `R` tail window, and no request at all for an account with no dates (7 tests).
*   `client_instrument_currency_integration_test.go` — quote currency, face and per-piece value available after `GetAccountDetails` with `GetAsset`/`GetAssetParams` pinned at one call per position (the count before the currency layer) and zero on a second load; an answer naming no currency cached as such (2 tests).
*   `client_face_currency_integration_test.go` — face currency from the calendar: one future request, the past fallback without an interval, free on repeat and free when the day calendar is already loaded; a rate limit recognisable and not cached (2 tests).
*   `client_fx_integration_test.go` — rates: one `LastQuote` per currency with a pair and none for RUB/HKD, no lot resolution dragged along, a frozen pair refused, a rate limit ending the walk while an ordinary failure costs one currency (3 tests).
*   `calendar_cache_integration_test.go` — the 24h calendar cache: a second call costing 0 RPCs, the three calendars keyed apart, a failure retried rather than cached, `When` populated and agreeing with both `Date` and the ascending order (4 tests).
*   `client_quote_stream_integration_test.go` — `SubscribeQuote` manager: snapshot delivery + up only after the first `Recv`, incremental merge, resubscribe on symbol change (no down event), reconnect after a drop, empty set never subscribes, `Close()` stops the manager, in-band `StreamError` keeps the stream (7 tests).

### CI Pipeline

The CI workflow (`.github/workflows/ci.yml`) has 4 jobs:
1.  **unit-test** — runs `go test -race -coverprofile` on all packages.
2.  **integration-test** — runs `go test -tags=integration -race -coverprofile` on `./api/...`.
3.  **coverage** — merges profiles from both jobs and reports via `go tool cover -func`.
4.  **lint** — runs `golangci-lint`.

## Directory Structure

*   `analytics/`: Pure portfolio calculations (structure, risk, trades, cash flows, payouts). No I/O, no tview.
*   `api/`: gRPC client wrapper (`index.go` holds the index composition layer).
    *   `testserver/`: Mock gRPC server for integration tests (bufconn-based, all 7 Finam services).
*   `updater/`: Update check + self-update (semver, state cache, GitHub client, scheduler, download, apply, restart).
*   `config/`: Configuration loader.
*   `models/`: Data types.
*   `ui/`: TUI implementation (views, controllers).
*   `.env`: Local configuration (git-ignored).

## API Implementation Details

### Retrieving Security Prices

1.  **Market Data (Quotes)**
    *   **Service:** `MarketDataServiceClient`
    *   **Method:** `LastQuote`
    *   **File:** `api/client.go` (`GetQuotes`)
    *   **Key Field:** `Last` (Last trade price)
    *   **Usage:** Ticker lookup, general price checks.

2.  **Security Search**
    *   **Service:** `InstrumentsServiceClient`
    *   **Method:** `GetSecurities`
    *   **File:** `api/client.go` (`SearchSecurities`)
    *   **Usage:** Finding assets by ticker or name.

3.  **Portfolio Positions**
    *   **Service:** `AccountsServiceClient`
    *   **Method:** `GetAccount`
    *   **File:** `api/client.go` (`GetAccountDetails`)
    *   **Key Field:** `CurrentPrice` (Broker's valuation price)
    *   **Usage:** Calculating equity, PnL, and position value. Positions are enriched with `LotSize` and human-readable `Name` from the instrument cache.

4.  **Trade History**
    *   **Service:** `AccountsServiceClient`
    *   **Method:** `GetTradeHistory`
    *   **File:** `api/client.go` (`GetTradeHistory`)
    *   **Usage:** Fetching completed trades for display in the History tab.

5.  **Active Orders**
    *   **Service:** `AccountsServiceClient`
    *   **Method:** `GetOrders`
    *   **File:** `api/client.go` (`GetActiveOrders`)
    *   **Usage:** Fetching pending/active orders for display in the Orders tab.

6.  **Asset Info**
    *   **Service:** `AssetsServiceClient`
    *   **Method:** `GetAsset`
    *   **File:** `api/client.go` (`GetAssetInfo`)
    *   **Usage:** Retrieving detailed instrument information (name, ISIN, type, board, currency, lot size, decimals, expiration).

7.  **Asset Trading Parameters**
    *   **Service:** `AssetsServiceClient`
    *   **Method:** `GetAssetParams`
    *   **File:** `api/client.go` (`GetAssetParams`)
    *   **Usage:** Fetching trading parameters (tradability, long/short availability, risk rates, margins) and `trade_lot_size` (2.18.1, mapped to `models.AssetParams.TradeLotSize`). The call also warms the trade lot cache, so opening an instrument profile primes order sizing for free.

8.  **Candlestick Bars**
    *   **Service:** `MarketDataServiceClient`
    *   **Method:** `Bars`
    *   **File:** `api/client.go` (`GetBars`)
    *   **Usage:** Fetching OHLCV candlestick data for chart rendering. Supports multiple timeframes (M5, H1, D, W).

9.  **Trading Schedule**
    *   **Service:** `AssetsServiceClient`
    *   **Method:** `Schedule`
    *   **File:** `api/client.go` (`GetSchedule`)
    *   **Usage:** Retrieving trading session times for an instrument.

10.  **Instrument Name Cache**
    *   **File:** `api/client.go` (`InstrumentCache`, `GetInstrumentName`, `UpdateInstrumentCache`)
    *   **Usage:** Centralized O(1) cache mapping ticker symbols to human-readable names. Populated during asset loading and search operations.

11.  **Place Order (Market, Limit, Stop, Take-Profit)**
    *   **Service:** `OrdersServiceClient`
    *   **Method:** `PlaceOrder`
    *   **File:** `api/client.go` (`PlaceOrder`)
    *   **Usage:** Places market, limit, stop-loss, and take-profit orders. Accepts optional `*OrderParams` to specify order type and prices. Quantity is in lots (auto-multiplied by lot size). Stop condition is auto-selected based on direction and order type.

12.  **Place SL/TP Linked Order**
    *   **Service:** `OrdersServiceClient`
    *   **Method:** `PlaceSLTPOrder`
    *   **File:** `api/client.go` (`PlaceSLTPOrder`)
    *   **Usage:** Places a linked stop-loss + take-profit order pair where one cancels the other. Supports placing with only SL, only TP, or both. Quantities are in lots. Defaults to GTC (Good Till Cancel) validity.

14.  **Cancel Order**
    *   **Service:** `OrdersServiceClient`
    *   **Method:** `CancelOrder`
    *   **File:** `api/client.go` (`CancelOrder`)
    *   **Usage:** Cancels an active order by account ID and order ID. Returns error if order is already executed or not found.

15.  **gRPC Error Logging**
    *   **File:** `api/client.go` (`logGRPCError`)
    *   **Usage:** Unified helper used by all gRPC calls to log errors in a structured format: `[ERROR] Service.Method failed | Param: value | gRPC code: <code> | Message: <msg> | Endpoint: <addr>`. Never logs secrets (tokens).

16.  **Dividend Calendar** (2.16.0)
    *   **Service:** `CorporateActionsServiceClient`
    *   **Methods:** `GetPastDividends` + `GetFutureDividends`
    *   **File:** `api/client.go` (`GetDividends`)
    *   **Usage:** Returns the merged past (last 12 months, DESC) + future (ASC) dividend calendar for a symbol, limit 20 each, sorted ascending by date with `IsFuture` flags. Surfaced only in the equity profile.

17.  **Split Calendar** (2.16.0)
    *   **Service:** `CorporateActionsServiceClient`
    *   **Methods:** `GetPastSplits` + `GetFutureSplits`
    *   **File:** `api/client.go` (`GetSplits`)
    *   **Usage:** Same past+future windows as dividends; maps ratio (old→new), new lot, and conversion type. Surfaced only in the equity profile.

18.  **Bond Event Calendar** (2.17.0)
    *   **Service:** `CorporateActionsServiceClient`
    *   **Methods:** `GetPastBondsEvents` + `GetFutureBondsEvents`
    *   **File:** `api/client.go` (`GetBondEvents`)
    *   **Usage:** Flattens the `oneof` event details (`CouponDetails`/`AmortizationDetails`/`OfferDetails`) into a `models.BondEvent` with `Kind` ∈ {Coupon, Amortization, Offer}. Surfaced in the bond profile and in the Analytics payout forecast. `Future*` requests take no date interval; all SDK pointer/wrapper fields are formatted nil-safe (`formatDate`, `formatDecimalOpt`, `formatInt32Value`).
    *   **Currency:** `BondEvent.currency` arrives as a symbol (`₽ $ € ¥`); `mapBondEvent` stores the ISO code (`currencyCode`), keeping an unknown symbol as sent. It is also the only source of a bond's face currency — see 33.
    *   **Future coupon amounts depend on the bond (reconnaissance 2026-09-09, real API):** a fixed-coupon bond reports `value` for its future coupons exactly as for its past ones (ОФЗ 26238: `value: 35.4`, `value_percent: 7.1`, past and future alike). A floating-rate bond does not: RU000A10BF48 reports real amounts for past coupons (`13.01`, `13.18` — they differ month to month) and `value: 0.0` for every future one, because the rate for the coming period is not set yet. Its `value_percent` is `0.0` even on past coupons, so the rate is never expressed as a percent and the amount only appears after the fact. This is why `analytics.applyPerUnit` refuses a non-positive value instead of rendering it: the terminal reads the right field, and the field is genuinely empty.
    *   **`GetPastBondsEvents` takes no interval either — it refuses `date_to` (reconnaissance 2026-09-09, real API):** this method, alone among the three calendars, rejects a `date_to` of today with `InvalidArgument: Invalid arguments:date_to`, and the width of the window makes no difference — 30 days, six months and a year were all refused. The same window ending **yesterday** is accepted, and so is a request carrying **no interval at all**, which the proto documents as defaulting to a year and which returned the identical set of events. So `fetchBondEvents` sends neither date and lets the server apply its own default; `pastYearRange()` stays in use for dividends and splits, which accept a `date_to` of today. This was not theoretical: `pastYearRange()` produces exactly the refused shape, so **every bond failed its calendar** and contributed nothing to the payout forecast, while the screen showed one unexplained line naming the instrument. The mock enforces the validation (`rejectDateToToday`) so the shape cannot regress.

19.  **Trade Lot Size / Lot Resolution** (2.18.1)
    *   **Service:** `AssetsServiceClient`
    *   **Method:** `GetAssetParams` (field `trade_lot_size`)
    *   **File:** `api/client.go` (`fetchTradeLotSize`, `storeTradeLotSize`, `lotSizeLocked`, `GetLotSize`, `EnsureLotSize`, `resolveAssetLot`, `refusedSymbolCache`)
    *   **Usage:** The trade lot is the lot the broker sizes orders by and takes priority over `GetAsset.lot_size`. `tradeLotCache` is a second cache tier keyed by ticker and full symbol; a stored `0` is a negative cache entry ("checked, the API has no value") that prevents a `GetAssetParams` call on every refresh tick, while a failed call is deliberately not cached so the next miss retries. The one failure that is remembered is a ticker without a MIC that `GetAsset` refused with `InvalidArgument` (`Mic must not be empty`, observed 2026-09-10): it is not asked about again for `refusedSymbolTTL` — see the `client.go` (lot resolution) note. `lotSizeLocked` is the single resolution point (trade[ticker] → trade[mic] → asset[ticker] → asset[mic]) and exists because `sync.RWMutex` is not reentrant — `GetAccountDetails` resolves lots while already holding the read lock. `EnsureLotSize` is the blocking warm-up the UI calls off the event loop before rendering the order modal.

20.  **Realtime Quotes (SubscribeQuote)** (2.19.0)
    *   **Service:** `MarketDataServiceClient`
    *   **Method:** `SubscribeQuote` (server stream)
    *   **File:** `api/quote_stream.go` (`StartQuoteStream`, `SetQuoteSymbols`, `runQuoteStream`, `mergeQuote`), `ui/stream.go` (consumption)
    *   **Usage:** Replaces the N+1 `LastQuote` poll for the active account. The subscription is **sharded**: one subscription accepts 15 symbols and the limit is per subscription, so larger sets are split across parallel streams (see "Index Tab Quote Budget"). `SetQuoteSymbols` declares the desired symbol set (normalized: `@`-filtered, deduped, sorted) and never blocks the UI thread; changing it cancels the current subscription and resubscribes without reporting an outage, while a real drop reconnects with 1s→30s backoff. Liveness is claimed only after the first received message (gRPC opens streams lazily), and `resp.Error` is logged as a warning without ending the stream. `getStreamContext` carries the current token with no unary timeout. `Quote.is_data_snapshot` decides the merge: a snapshot replaces the remembered state, an increment overwrites only its non-nil fields. While the stream is live the UI stops polling quotes for the active account and never replaces the cached quote map; inactive accounts and chart bars keep polling.

22.  **Update Check (GitHub Releases)**
    *   **Service:** GitHub REST API (not gRPC)
    *   **Endpoint:** `GET https://api.github.com/repos/updevru/finam-terminal/releases/latest`
    *   **File:** `updater/github.go` (`FetchLatestRelease`), `updater/checker.go` (`Run`, `ShouldCheck`)
    *   **Usage:** Background check once per 24h for release builds only; the result is cached in `~/.finam-cli/update.json`. Unauthenticated, 10s timeout, `[WARN]`-logged on any failure and retried on the normal schedule. `main.go` reads the cache at startup (no network) to decide whether to show the update dialog.

21.  **Session Token Details / Expiry**
    *   **Service:** `AuthServiceClient`
    *   **Method:** `TokenDetails`
    *   **File:** `api/client.go` (`fetchTokenExpiry`, `TokenExpiry`, `GetAccounts`), `api/token_watch.go` (`watchTokenExpiry`, `shouldReauthenticate`)
    *   **Usage:** Returns the account list and the session token expiry. Must be called **without** the `Authorization` metadata header (use `getUnauthenticatedContext()`) — the token travels in the request body and sending both is rejected with `InvalidArgument: Token is invalid or malformed`. The expiry is not decoration: `watchTokenExpiry` polls it every 30s and re-authenticates 2 minutes ahead, which is the only thing that recovers a session the renewal stream let lapse (a reopened stream resets the broker's own ~14 min schedule and delivers nothing on subscribe).

23.  **Index Constituents (GetConstituents)**
    *   **Service:** `AssetsServiceClient`
    *   **Method:** `GetConstituents`
    *   **File:** `api/index.go` (`GetIndexConstituents`, `fetchIndexConstituents`, `mapConstituent`), `ui/index.go` + `ui/render.go` (Index tab)
    *   **Usage:** Returns the composition of a stock index, collected across the `cursor`/`next_cursor` pagination (guarded at `maxConstituentPages` = 10 with a `[WARN]`) and cached in memory per index symbol for `indexCacheTTL` = 24h. A failed or empty refetch keeps serving the previous composition (**stale-on-error**); only a failure with nothing cached is reported, and nothing retries on its own — the tab offers a manual `R`. An empty response is an error and is never cached, so a bad answer cannot blank the tab for the rest of the TTL. Components without a full `ticker@mic` symbol are dropped with one `[WARN]`. API order is preserved; sorting is the UI's decision.
    *   **Reconnaissance (2026-08-26, real API):** `IMOEX@RTSX` is the only supported Russian index (46 components, `SBER@MISX`-style symbols, Russian names, sectors, weights). `IMOEX@MISX` and `MOEXBC@MISX` return `NotFound`; `NDX@_SCI` and `SPX@_SP` work but are out of scope. The index symbol itself is **absent from the bulk asset list**, so it cannot be discovered — hence the hardcoded `ui.indexList` constant.
    *   **Weight:** rendered raw, not as a percentage. The values do not sum to 1 and the scale is undocumented, so it is trustworthy for ordering but not for display as a share of the index.

24.  **Index Tab Quote Budget**
    *   **Files:** `ui/stream.go` (`computeStreamSymbols`, `evaluateIndexStreamHealth`), `ui/index.go` (`shouldPollIndexQuotes`, `pollIndexQuotesSync`)
    *   **Broker symbol cap (measured 2026-08-26, real API):** `SubscribeQuote` **does** limit how many symbols one subscription may carry — a 46-symbol subscription was accepted and then killed with `InvalidArgument: Maximum number of symbols exceeded`. The limit is undocumented and has no dedicated status code, so `api.IsSymbolLimitError` matches the message, `reducedSymbolCap` halves the refused count (46 → 23 → 11 → 5 → 2 → 1) and `applySymbolCap` truncates **from the end**. That makes the caller's order priority order — hence `normalizeSymbols` and `computeStreamSymbols` preserve it instead of sorting, and positions come before the index so portfolio quotes are never the ones dropped. A refused subscription resubscribes immediately and is not reported as an outage.
    *   **Measured limits (2026-08-26, one-off probe against the real API):** one subscription accepts **exactly 15 symbols** (16 → `InvalidArgument: Maximum number of symbols exceeded`), and the limit is **per subscription, not per connection** — three parallel 15-symbol streams delivered 45 symbols simultaneously, five streams also worked. So the index is covered by *more streams*, not by rationing symbols.
    *   **Live stream:** the **whole composition** joins the subscription while the Index tab is active and leaves it on exit. `shardSymbols` splits the priority-ordered set into shards of `defaultQuoteShardSize` = 15, and one worker per shard subscribes, reconnects and backs off independently; `maxQuoteShards` = 8 bounds the stream count. 46 index symbols plus positions cost four streams and **zero unary quote calls**.
    *   **Paced sweep (`sweepIndexQuotes`) — fallback only:** `SubscribedSymbols()` reports the symbols of *live* shards, `uncoveredIndexSymbols` is the complement, and the sweep walks it **one `LastQuote` per request, `indexSweepDelay` = 150ms apart**, repainting every `indexSweepRedrawEvery` = 8 rows. With all shards up the complement is empty and the sweep issues no request at all; it exists for shards that are down or symbols beyond `maxQuoteShards`. Spacing is the point: the broker refuses a *burst*, not the volume — 46 back-to-back `LastQuote` calls earned an immediate `ResourceExhausted`, the same 46 spread out do not. `indexSweepDelay` is a var so tests can zero it.
    *   **Cost bounds:** `indexPollCooldown` = 60s between automatic sweeps, tab must be on screen, and the session rate-limit latch. `shouldPollIndexQuotes` deliberately does **not** veto on stream liveness — liveness is per shard, so the sweep decides from the live symbol set instead.
    *   **Rate limited:** `GetQuotes` ends its batch on `ResourceExhausted`, and the sweep stops at the first refusal rather than walking the rest; the UI latches `indexPollDisabled` for the session, logs a `[WARN]` and says so in the status bar; manual `R` keeps working. An ordinary per-symbol error neither latches nor ends the pass — a blip must not cost the feature.
    *   **Positions-stream guard:** a third line of defence, behind sharding and the adaptive cap. If the stream does not come up within 60s of the composition joining it (or fails 3 times) for any reason those do not cover, `indexStreamDisabled` latches for the session, the index symbols are dropped and the positions stream recovers on its own. Shard isolation makes this largely redundant — a failing index shard no longer affects the positions shard — but it stays as a backstop. The latch is one-way and so is its opposite: a subscription that came up while carrying the composition sets `indexStreamProven` and the guard never arms again for the session. Portfolio quotes always win over the showcase tab.
    *   **Per-call deadlines:** `GetQuotes` gives every `LastQuote` its own deadline. It used to share one 30s context across the whole batch, so on a long batch the later symbols failed with `DeadlineExceeded` while the broker was answering normally.

25.  **Usage Metrics (GetUsageMetrics)**
    *   **Service:** `UsageMetricsServiceClient` (the seventh service client)
    *   **Method:** `GetUsageMetrics`
    *   **File:** `api/client.go` (`GetUsageMetrics`), `api/history.go` (`checkHistoryQuota`)
    *   **Usage:** Returns the Trade API quota table for the **session token, not an account**. Its only caller is the history loader's pre-check, which spends one probe before a long pass and refuses the pass when a method's remaining quota would not cover it. There is no screen for it — the terminal reads quotas to protect itself, not to show them.
    *   **Reconnaissance (2026-09-03, real API):** 39 quotas, names in `Service.methodCamelCase` form. `limit` is 200 for 38 of them; the exception is `ReportsService.createAccountReport` at 3. The window is **60 seconds**. The response order is **not sorted** and differs between calls. **`reset_time` is nil for any quota untouched in the current window** — 37 of the 39 — so `ResetAt` maps to the zero time.

26.  **Account Cash, Margin and First-Transaction Dates**
    *   **Service:** `AccountsServiceClient`
    *   **Method:** `GetAccount` (fields `cash`, the `portfolio` oneof, `first_trade_date`, `first_non_trade_date`)
    *   **File:** `api/client.go` (`mapCashBalances`, `applyPortfolio`, `timestampOrZero` inside `GetAccountDetails`)
    *   **Usage:** These arrive in the same response the terminal already makes every five seconds and used to be discarded, so the Analytics margin block costs **zero extra requests**. `HasMarginData` is set only for the MC and FORTS branches, the two that carry numbers: `MCT` is an empty message in the proto and an absent oneof carries nothing, so a zero there means "not reported" and must render as `Н/Д`. Every field is read nil-safe — the reconnaissance could not observe a live account (no available token carried one), so nothing assumes the broker populates anything.
    *   `applyAccountData` in `ui/data.go` copies all of these onto the stored account on every tick, not just equity, or the margin block would freeze at the values of the first load.
    *   **The Оценка panel and the ГО FORTS row cost nothing either.** They read `equity` and `unrealized_profit` from the account and `daily_pnl`, `average_price` and `maintenance_margin` from each position of the same response (`analytics.Valuation`). Per the proto, `daily_pnl` and `average_price` are *not* filled for FORTS positions and `maintenance_margin` is filled *only* for them — so on a unified account the collateral its FORTS positions tie up is visible only as the sum over positions, and the day's result of an account with futures is partial. Not yet checked against a live account: whether that sum matches the «Гарантийное обеспечение FORTS» figure of the broker's own terminal (the proto calls the per-position field *maintenance* collateral), whether its «Прибыль по позициям» percentage is taken over cost as ours is, and whether its day figure is Σ `daily_pnl`. What the one available screenshot does confirm is the relation itself: the broker's own terminal shows start-of-day = current − day to the kopeck (67 629.18 − 43.26 = 67 585.92).

27.  **Instrument Type (`Asset.Type`)**
    *   **Service:** `AssetsServiceClient`
    *   **Method:** `Assets` (the bulk list already loaded at startup)
    *   **File:** `api/client.go` (`loadAssetCache`, `assetTypeCache`, `GetInstrumentType`)
    *   **Usage:** The type is filed under both the ticker and the full symbol during the one bulk load, so grouping the Analytics overview by instrument type costs nothing. `GetInstrumentType` is a **pure cache read and never issues a request** — that is what lets the overview redraw on every tick.
    *   **Reconnaissance (2026-09-03, real API, full catalogue of 291 890 instruments over 98 `AllAssets` pages):** ten distinct values — `OTHER` (120 673), `FUTURES` (89 613), `EQUITIES` (45 906), `FUNDS` (16 597), `BONDS` (10 456), `CURRENCIES` (4 703), `SPREADS` (2 825), `INDICES` (1 098), `OPTIONS` (10), `SWAPS` (9). Four were unplanned; `analytics.typeGroups` maps the six expected ones and everything else falls through to Прочее, which at 41% of the catalogue is a normal row rather than a symptom. Note that `Assets` answers `NotFound` for a token with no trading account while `AllAssets` (paginated) works — that is how the census was taken.

29.  **Account Transactions (`Transactions`)**
    *   **Service:** `AccountsServiceClient`
    *   **Method:** `Transactions`
    *   **File:** `api/client.go` (`GetTransactions`, `mapTransaction`, `moneyAmount`)
    *   **Usage:** The money side of an account's history. The category is taken from the `transaction_category` enum name rather than the free-text `category` field. Every field is read nil-safe — the reconnaissance never observed a live transaction, because no available token carries a trading account. `moneyAmount` is shared with `mapCashBalances`: `google.type.Money` keeps units and nanos at the same sign, so a charge of −1.50 arrives as `{-1, -500000000}` and simply adds up.

30.  **Account History Loader (`LoadHistory`)**
    *   **Service:** `AccountsServiceClient`
    *   **Methods:** `Trades` + `Transactions`, in chunks
    *   **File:** `api/history.go`, `ui/analytics_history.go`
    *   **Usage:** One pass per account per session, triggered only by an explicit visit to the Trades or Money sub-screen. See the `api/history.go` entry above for the walk, the split, the guard and the quota pre-check. `GetTrades(accountID, from, to, limit)` is the parameterised call behind it; `GetTradeHistory` is now a 30-day wrapper over it and the History tab is unchanged.
    *   **Constants (set by the 2026-09-04 reconnaissance):** `historyChunk` 92d, `historyLimit` 1000, `historyPace` 150ms, `historyMaxRequests` 80, `historyQuotaReserve` 20, `historyQuotaProbe` 5, `benchmarkWindow` 14d. All package vars so tests can shorten a pass.
    *   **Reconnaissance (2026-09-04, real API):** `Bars` on the daily timeframe accepts **366 days** and rejects 367 with `InvalidArgument: Invalid date range`; a **seven-day** window over the Russian New Year holidays returns zero bars (`2010-01-01 + 7d` → 0, `+14d` → 5); quota names are lower camelCase (`AccountsService.trades`); and a request refused with `NotFound` **still spends its quota**. Not established, because the available token has no trading account and the server validates the account before the interval: `Trades`/`Transactions` limit semantics, accepted interval width, the category trade transactions arrive under, and whether `COMMISSION` carries a symbol.

31.  **Analytics Tab Request Budget**
    *   **Files:** `ui/analytics_overview.go`, `ui/analytics.go`
    *   The overview is drawn from memory only: positions and quotes from the existing five-second tick, the account own margin report, the startup asset cache and the index composition (a 24h cache shared with the Index tab). A test redraws it twenty times and asserts the client was not touched once. The index symbols do **not** join the quote subscription from this tab — the stream symbol set follows the Index tab.
    *   The account's history once per session, on an explicit visit to Trades or Money, plus two `GetBars` for the benchmark. A test runs twenty ticks and asserts the loader was not called again; another presses `P` ten times and asserts no request of any kind.
    *   The payout calendars once per session per account, two requests per long position, themselves cached per symbol for 24h — so `R` on that screen is usually free.
    *   Changing the period, re-entering a sub-screen and moving between Trades and Money all cost nothing: every preset is a filter over the loaded history. **No loader retries on its own after a failure** — only `R`, and the two history screens hold a 10s cooldown.
    *   The currency data (track `analytics_currency`): at most one `LastQuote` per foreign currency the account holds per `fxRateTTL` (10 min), only while the overview is on screen, none for a rouble account; at most one calendar request (two with the past fallback) per foreign-face bond per session, none for ordinary bonds, none when the day calendar is loaded. The redraw itself costs nothing (a test redraws twenty times with foreign currency, a stale rate and an unresolved bond on screen and asserts no rate, calendar or quote request). A rate limit latches both loaders for the session; `R` works through it.

32.  **Instrument Currency and Per-Piece Value** (track `analytics_currency`)
    *   **Services:** `AssetsServiceClient` — `GetAsset` (`quote_currency`, `bond_details.bond_face_value`) and `GetAssetParams` (`long_initial_margin`, `long_risk_rate`, `trade_lot_size`), both **already called once per instrument per session** for the lot.
    *   **File:** `api/currency.go` (`GetInstrumentCurrency`, `GetUnitValue`), `analytics/currency.go` (`ValuePosition`)
    *   **Reconnaissance (2026-09-10, real API):** `bond_details.currency` is **`"%"` on every bond** — rouble, yuan and a replacement bond with a 200 000 USD face alike — so it is not read. `quote_currency` is the settlement currency: CNY on the TQOY board, RUB for replacement bonds and RUB-settled yuan/dollar bonds, USD/HKD for foreign equities. Bond `current_price`/`average_price` are **percent of face**; P&L is money (ЯНДЕКС1Р1: −19 = 10 × (100.7 − 100.89) × 1000/100). Margin × 100 / risk rate / lot is one piece's value in the settlement currency — dirty, with face and conversion (РФ ЗО 27 Д: 16.6 M RUB ≈ 85 × price × face / 100; rouble bonds ≈ 1.01–1.04). Equity reconciles **to the kopeck** as Σ positions (bonds dirty) + cash + USD at the last `USD000UTSTOM` trade; without the face, as the terminal used to value bonds, it was 13% short.

33.  **Bond Face Currency** (track `analytics_currency`)
    *   **Service:** `CorporateActionsServiceClient` — `GetFutureBondsEvents`, `GetPastBondsEvents` (no interval)
    *   **File:** `api/currency.go` (`GetBondFaceCurrency`, `BondFaceCurrencyCached`, `currencyCode`), `ui/analytics_fx.go` (`fetchFaceCurrencies`)
    *   **Usage:** the only place the API names a face currency is `BondEvent.currency`, as a **symbol** (`₽`, `$`, `€`, `¥` — the last is the yuan: every bond carrying it was a CNY bond). Looked up only for bonds whose per-piece value does not match price × face / 100 (`analytics.BondsNeedingFace`); until then such a bond is valued at the per-piece figure in a «номинал в валюте» row. The calendar service timed out on **10 of 30** reconnaissance calls (30 s each), so nothing retries at once: the next attempt is a TTL away or `R`.

34.  **Exchange Rates (`LastQuote` of a pair)** (track `analytics_currency`)
    *   **Service:** `MarketDataServiceClient.LastQuote`
    *   **File:** `api/fx.go` (`fxSymbols`, `rateFromQuote`, `GetFXRates`), `analytics/currency.go` (`RateTo`, `RatesToFetch`, `RateIsStale`), `ui/analytics_fx.go` (schedule)
    *   **Reconnaissance (2026-09-10, real API):** `LastQuote` answers in and out of session (after the close: last trade, close, its timestamp, empty bid/ask). Live on MISX: `USD000UTSTOM` (last trade 17:30), `CNYRUB_TOM` (to 19:00), `KZTRUB_TOM`/`AMDRUB_TOM`/`KGSRUB_TOM` per 100, `BYNRUB_TOM`, `TRYRUB_TOM`; **every EUR pair on MISX is frozen at 2025-01-09 10:00:03** (plus GBP, CHF, HKD, USDCNY, USDKZT) and still answers — only the timestamp tells. `#WWCP` (forex, in `AllAssets` but not `Assets`) is live 24/5 for USDRUB, EURRUB, CNYRUB, INRRUB and USD crosses, `NotFound` for the CIS pairs. `_NPRO` has no `last` and timed out for EUR; `#RCBR` guesses are `NotFound`. The broker values foreign cash at the MOEX TOM last trade (reconciled to the kopeck twice), hence MOEX for USD/CNY and `#WWCP` only for EUR/INR. HKD/JPY/GBP/CHF/AED/UZS have no rate (HKD/JPY exist only as USD crosses, out of scope).

# Conductor Context

If a user mentions a "plan" or asks about the plan, and they have used the conductor extension in the current session, they are likely referring to the `conductor/tracks.md` file or one of the track plans (`conductor/tracks/<track_id>/plan.md`).

## Universal File Resolution Protocol

**PROTOCOL: How to locate files.**
To find a file (e.g., "**Product Definition**") within a specific context (Project Root or a specific Track):

1.  **Identify Index:** Determine the relevant index file:
    -   **Project Context:** `conductor/index.md`
    -   **Track Context:**
        a. Resolve and read the **Tracks Registry** (via Project Context).
        b. Find the entry for the specific `<track_id>`.
        c. Follow the link provided in the registry to locate the track's folder. The index file is `<track_folder>/index.md`.
        d. **Fallback:** If the track is not yet registered (e.g., during creation) or the link is broken:
            1. Resolve the **Tracks Directory** (via Project Context).
            2. The index file is `<Tracks Directory>/<track_id>/index.md`.

2.  **Check Index:** Read the index file and look for a link with a matching or semantically similar label.

3.  **Resolve Path:** If a link is found, resolve its path **relative to the directory containing the `index.md` file**.
    -   *Example:* If `conductor/index.md` links to `./workflow.md`, the full path is `conductor/workflow.md`.

4.  **Fallback:** If the index file is missing or the link is absent, use the **Default Path** keys below.

5.  **Verify:** You MUST verify the resolved file actually exists on the disk.

**Standard Default Paths (Project):**
- **Product Definition**: `conductor/product.md`
- **Tech Stack**: `conductor/tech-stack.md`
- **Workflow**: `conductor/workflow.md`
- **Product Guidelines**: `conductor/product-guidelines.md`
- **Tracks Registry**: `conductor/tracks.md`
- **Tracks Directory**: `conductor/tracks/`

**Standard Default Paths (Track):**
- **Specification**: `conductor/tracks/<track_id>/spec.md`
- **Implementation Plan**: `conductor/tracks/<track_id>/plan.md`
- **Metadata**: `conductor/tracks/<track_id>/metadata.json`
