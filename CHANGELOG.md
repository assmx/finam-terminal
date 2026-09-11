# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **Blocked assets are recognised** (`api/blocked.go`): a position the broker holds on a blocked venue (`_SPBZ`, `_MMBZ`) — or sends without a MIC while the bulk list knows only its twin there, like `FXRL` and `FXRL.MMBZ@_MMBZ` — is marked `Position.Blocked` from the list loaded at startup, at **no request**, and named after its twin. A ticker the list knows on a live venue is never taken for blocked. The Positions tab's Value column reads `BLOCKED`, and the overview's structure panel says under its total what it leaves out: `заблокировано: N · X RUB по цене брокера` (`blocked_assets`).
- **Mock server**: two blocked twins in `DefaultAssets`, `BlockedAccountPositions`, and a `SubscribeQuote` that stays silent when a blocked symbol is subscribed, as the live API does (`blocked_assets`).
- **Analytics: панель «Валюты»**: the overview's left column breaks the portfolio down by currency, under the breakdown by type and over the **same base**, so both panels' shares add up to 100%. The base currency comes first, then the others by value; under the rows a grey line per foreign currency gives the amount in its own money, the rate and the rate's time (`CNY 1 500.00 × 12.5300 · 14:05`). A rate older than 12 hours reads in yellow as `курс на 06.09 17:30` instead of passing for current, and a currency without a rate is a yellow line in its own money (`HKD 42 560.00 — нет курса`) that stays out of every share. A rouble-only account shows one row and costs no request (`analytics_currency`).
- **Foreign holdings are converted**: every position and cash line on the overview is valued in its own currency and converted into the account's base at the rate. The structure, sectors, concentration, leverage and the Оценка panel now measure one currency. Money bought in another currency joins the «Валюта» group, as the broker files it. The day's result converts each position's P&L at its currency's rate. A position that cannot be converted is counted apart (`без курса: N`) and makes the day partial, never silently mixed in (`analytics_currency`).
- **Exchange rates from pair quotes** (`api/fx.go`): the Trade API has no rate method, so `Client.GetFXRates` reads `LastQuote` of each currency's pair to the rouble — `USD000UTSTOM` and `CNYRUB_TOM` on MOEX (the broker values cash at their last trade), `EURRUB`/`INRRUB` from the `#WWCP` forex feed (MOEX EUR is frozen since January 2025), KZT/AMD/KGS/BYN/TRY on MOEX with the per-100 quoting handled. A quote older than 14 days is not a rate. Rates are asked for **once per currency per 10 minutes**, only while the overview is on screen, and are shared across accounts; a `ResourceExhausted` answer stops the schedule for the session with `лимит API: курсы не обновляются, R — вручную`, and `R` keeps working. LastQuote is called directly, without dragging a lot resolution along (`analytics_currency`).
- **Instrument currency and per-piece value at no cost** (`api/currency.go`): `Client.GetInstrumentCurrency` (quote currency and a bond's face, from the `GetAsset` answers the lot resolution already gets) and `Client.GetUnitValue` (one piece's value in its settlement currency, from the margin in the `GetAssetParams` answer the trade lot already gets). Both are pure cache reads; the integration suite pins `GetAsset`/`GetAssetParams` at the call counts they had before (`analytics_currency`).
- **Bond face currency from the calendar**: `Client.GetBondFaceCurrency` reads the face currency the API names only in the bond calendar, and only for bonds whose per-piece value shows a face in another currency than they settle in (replacement bonds, yuan bonds settled in roubles). It costs one request per such bond per session, none when «Выплаты» or a profile already loaded the calendar, and none for ordinary rouble and yuan bonds. Until it answers, such a bond is valued at the broker's own per-piece figure in a «номинал в валюте» row, never at a fraction of its value (`analytics_currency`).
- **`analytics/currency.go`**: `ValuePosition`, `RateTo` (cross through the rouble for a non-rouble base), `RatesToFetch`, `BondsNeedingFace`, `RateIsStale`, and `PnLCurrency` in `valuation.go`. Pure as before. A property test over 500 random portfolios pins that the type and currency breakdowns share one base, that holdings in unrated currencies never move it, and that nothing is NaN or Inf; package coverage is 96.1% (`analytics_currency`).
- **Mock server**: the currency reconnaissance instruments with their real `GetAsset`/`GetAssetParams` fields, per-symbol bond calendars (`BondCalendars`), live and frozen FX pair quotes, and per-symbol `LastQuote` counters (`analytics_currency`).
- **Analytics: панель «Оценка»**: the overview's right column now opens with the four figures the broker's own terminal leads its account summary with — the value at the start of the day, the current value, the day's result and the result on open positions, each result with its percentage (of the opening value and of the positions' cost respectively). All of it comes from the `GetAccount` response the terminal already reads every five seconds, so the panel costs **no request**. The start-of-day value is computed as current − day, which is exact on a day no money moved; where the broker leaves `daily_pnl` empty (FORTS positions) the day is shown for what is reported with a `без дневного P&L: N` note and the start-of-day value is `Н/Д` rather than wrong. The equity row moved here from «Маржа и риск», which no longer prints the same number a few rows below.
- **Analytics: ГО FORTS on a unified account**: «Маржа и риск» on an MC account shows the collateral its FORTS positions tie up, summed from the new `Position.MaintenanceMargin` (`positions[].maintenance_margin`, filled by the broker for FORTS positions only and `"N/A"` elsewhere). The row appears only when a position reports it — an account with no FORTS positions is not told it has zero.
- **Analytics: cash in other currencies**: a positive balance in a currency other than the account's base is listed in «Маржа и риск» as `остаток <валюта>` (`Allocation.ForeignCash`). It still cannot join the structure shares — the Trade API has no exchange rates — but it no longer disappears from the screen, which is what the manual had always promised.
- **Analytics: Сделки, Деньги, Выплаты**: three more sub-screens complete the tab (`[1] Обзор  [2] Сделки  [3] Деньги  [4] Выплаты`). Trades reports the realised result by FIFO with the usual statistics and a per-instrument table; Money splits the chosen period's cash flows by group and reports the account's result since it opened, with XIRR and a comparison against IMOEX; Payouts forecasts the dividends, coupons, amortizations and offers due on the positions currently held (`analytics_history`).
- **Period presets**: `P` cycles 1М → 3М → 1Г → YTD → Всё, shown in the sub-screen header and applied to Trades and Money. YTD turns at the viewer's own midnight, not UTC's. Changing the period issues no request — every preset is a filter over history already in memory (`analytics_history`).
- **`AccountsService.Transactions`**: `models.Transaction`/`TransactionTrade` and `Client.GetTransactions`. The category comes from the `transaction_category` enum rather than the free-text field, so cash-flow grouping switches on a closed set of twelve values (`analytics_history`).
- **History loader (`api/history.go`)**: `Client.LoadHistory` walks an account's trades and transactions backwards in 92-day chunks with a 150ms pace, splits a chunk whose answer came back at the limit, stops at a request guard, and refuses to start a long pass whose quota would not cover it. It never retries: a refusal, a failure or the guard ends the pass and the partial result is kept with the reason. One pass per account per session, off the event loop, cancelled when the app stops (`analytics_history`).
- **`Client.GetTrades`**: the parameterised window the loader needs; `GetTradeHistory` becomes a 30-day wrapper over it and the History tab is unchanged (`analytics_history`).
- **Corporate-action calendars cached for a day**: `GetDividends`, `GetSplits` and `GetBondEvents` serve a per-symbol cache with a 24-hour TTL, shared with the instrument profile. A failed load is not cached; an empty successful one is. `Dividend` and `BondEvent` gain `When`, the date as an instant, so the payout screen never reparses a display string (`analytics_history`).
- **`analytics/` grows the calculation layer**: `MatchFIFO`/`Reconcile` (realised results, accrued interest folded into the price, reversals distinguished from gaps), `Preset` (the five windows), `Stats`/`PerInstrument`, `Classify`/`Flows`, `XIRR`/`SinceOpenResult`, `BenchmarkFromBars`, `ExpectedPayouts` and `MergeTrades`/`MergeTransactions`. Still no I/O, no tview, nothing that can produce NaN. Package coverage 95.5%, with a property test over 200 random trade sequences asserting that realised result plus the cost of open lots equals the net cash the trades moved (`analytics_history`).
- **Mock server**: `MockAccountsServer` serves transactions, honours the request interval and limit on both `Trades` and `Transactions`, and offers configurable truncation caps and per-method error injection — without which the loader's chunk splitting could not be tested at all. The corporate-actions mock gains per-RPC counters, which is the only way to show that a cached calendar costs nothing (`analytics_history`).
- **User manual**: [«Вкладка Analytics»](docs/user_manual/analytics.md) documents the three new sub-screens, the period control, the FIFO and XIRR rules, the benchmark's caveat, every state line the loader can show and what each screen costs in requests (`analytics_history`).

- **Analytics tab**: a fifth tab with sub-screens switched by digit keys. The overview breaks the portfolio down by instrument type and by sector with shares and bars, shows the cash line, a margin and risk block (margin utilisation, cushion to a margin call, leverage) and the top-3 concentration. It is computed entirely from data the terminal already holds and **issues no request of its own**, so it redraws on every five-second tick for free (`analytics_overview`).
- **`UsageMetricsService.GetUsageMetrics`**: `Client.GetUsageMetrics` maps the Trade API quota table into `models.QuotaUsage`. It backs the history loader's quota pre-check, which refuses to start a long pass whose quota would not cover it (`analytics_overview`).
- **New `analytics/` package**: pure calculations over `models` with no I/O and no tview — `Structure` (allocation, sectors, concentration), `RiskMetrics` (margin figures and colour levels), and the number helpers behind them. Nothing panics on empty or malformed broker data and no result is ever NaN or Inf: an uncomputable value is reported through a `Valid` flag so the screen shows `Н/Д`. Coverage 95.5% (`analytics_overview`).
- **New `GetAccount` fields**: `AccountInfo` now carries the cash list by currency (`models.CashBalance`), the portfolio kind and its margin numbers (`initial_margin`, `maintenance_margin`, `available_cash`, `money_reserved`) and the first-transaction dates. All of it arrived in the same response the terminal already made every five seconds and was being discarded, so the whole margin block costs nothing (`analytics_overview`).
- **`Asset.Type` in the instrument cache**: `Client.GetInstrumentType` resolves an instrument's type by full symbol or bare ticker from the bulk list loaded once at startup. A pure memory read — it never issues a request (`analytics_overview`).
- **Mock server**: `MockUsageMetricsServer` is the seventh registered service, with a quota fixture and error injection for the history pre-check; `MockAccountsServer` serves per-account cash and portfolio oneof fixtures (MC and FORTS) (`analytics_overview`).
- **User manual**: new page [«Вкладка Analytics»](docs/user_manual/analytics.md) (`analytics_overview`).

### Changed
- **Analytics: a progress bar instead of a progress line**: while the account's history loads — the first visit to «Сделки» or «Деньги», or `R` — the two screens show one yellow bar across the middle of the screen, on the status line's colour, with the percentage centred, and nothing else. The line `История: запрос 14 из ~40, загружено с …` over empty `История ещё не загружена` frames is gone: it had to be read, and the eye went to the frames, which looked like a broken screen. The bar counts steps that take real time — the history windows (a window that had to be split is one step) and the two IMOEX bar requests — so it only moves forward and reads 100% only when every step is done; the old count ran past its own estimate (`запрос 20 из ~18`), its date jumped back when the walk moved from trades to transactions, and the bar requests were not shown at all. `HistoryProgress` reports `Done`/`Total` in windows instead of `Requests`/`Estimated`/`Boundary`. Focus sits on the bar, so Enter and A cannot act on the hidden table during `R`; it returns to the screen when the pass ends unless a search window or a profile took it meanwhile. No new request (`analytics_loading_bar`).
- **User manual**: [«Вкладка Analytics»](docs/user_manual/analytics.md) documents the progress bar — what counts as a step, the keys while it is up, which account it follows — and the new states of «Сделки» and «Деньги»; the two loading lines leave the status-line table (`analytics_loading_bar`).
- **«Маржа и риск» no longer lists foreign balances**: the `остаток <валюта>` rows moved to «Валюты», where the balance is shown once with its rate. Loans stay in «Маржа и риск», because they are risk rather than holdings (`analytics_currency`).
- **Leverage** compares equity with the positions' converted exposure, not with "base minus rouble cash", which would have counted bought currency as exposure (`analytics_currency`).
- **User manual**: [«Вкладка Analytics»](docs/user_manual/analytics.md) documents the Валюты panel, where the rates come from, how a bond's currency is decided, the new states and the request budget; [«Позиции»](docs/user_manual/positions.md) documents the currency codes in the money columns and the bond formula (`analytics_currency`).
- **Analytics tab redesign**: same figures, a screen built to be read. Every panel is now drawn as tall as its content instead of stretched to the column, with the slack left unframed — a border around empty space read as a broken panel, and on the Money and Payouts screens three quarters of the height was exactly that. A table with nothing in it is collapsed entirely, so an account with no expected payouts shows one sentence rather than a sentence over an empty full-height table. The overview splits into five panels (Структура, Секторы, Итог с открытия · Маржа и риск, Концентрация); Trades leads with three headline figures instead of seven ragged label-value pairs; Money draws its cash-flow groups as bars against the largest of them. Labels are joined to their figures with leader dots, share bars lose the `░` background that made every row grey, risk levels carry a `●` as well as a colour so the reading survives a monochrome terminal, table headings are aligned with the columns they head, and the sub-screen bar no longer runs its highlighted labels into each other. Panels no longer wrap: on a terminal too narrow for the numbers a row is clipped at the edge rather than folded onto a second line. And every row is laid out to the panel's real width rather than to a fixed 38 columns — breakdown rows and leader rows now reach the right edge instead of stopping two thirds of the way across, share bars grow with the panel, and on a narrow terminal it is the label that gives way rather than the figure. Headline blocks are the exception and stay capped: three KPI figures spread across 120 columns stop reading as a group.

  The tab is now **framed like every other section** — same border, same title, and the same double rule when it holds focus — with the sub-screen tabs and the period as a strip inside that frame over a dividing rule. Previously the bar floated at the top-left of the screen with nothing to belong to and the panels started hard against the top edge. Framing it exposed a real defect it had been hiding: when the panels of a column wanted more rows than the column had, the last of them carried on drawing past the bottom, which now painted over the frame. Panel heights are therefore decided at draw time, when the space is finally known, and handed out in priority order; a panel that cannot be given a border, a row and a border is dropped rather than drawn as a stump (`analytics_polish`).
- **User manual**: [«Вкладка Analytics»](docs/user_manual/analytics.md) rewritten for the redesigned tab — a "how to read the screen" section (panels sized to their content, leader dots, bars, headline figures, the `●` risk bullet, `Н/Д` versus `—` versus `*`), every panel named as it appears on screen with all of its fields, all four tables column by column, every state line each sub-screen can print, and the request budget. The pages that list the tabs no longer mention the removed API sub-screen, and `interface-overview.md` gains the tab's own keys (`analytics_polish`).
- **Position value is computed in one place**: `analytics.PositionValue` now backs both the Positions tab's Value column and the Analytics overview, so the two screens cannot disagree about what a holding is worth. As a result that column falls back to the broker's own `current_price` when the live quote has not arrived, instead of showing `N/A` (`analytics_overview`).
- **`applyAccountData`** carries the whole refreshed account — margin, cash and dates — onto the stored account, not just equity and unrealized P&L. Without it the Analytics margin block would have shown the values from the first load forever (`analytics_overview`).
- **Tab navigation**: `activeTabTable` returns a `tview.Primitive` rather than a `*tview.Table`, because the Analytics tab is not a table and decides for itself which primitive takes focus on the current sub-screen (`analytics_overview`).

### Fixed
- **A history walk could miss its first instant**: the walk stepped each window from the previous start, an instant further back each time, and `estimateRequests` divided in float64, which past two chunks rounds a nanosecond away. On a span a few nanoseconds past a whole number of chunks the walk took one window fewer than estimated and never asked for `From` itself — the moment the account's history starts. Windows now start a whole number of chunks before `To`, the estimate divides in integers, and a property test pins the two to agree (`analytics_loading_bar`).
- **«нет данных о начале истории счёта» vanished on entry**: the loader wrote the line once and the screen's own redraw, straight after, wiped it. It is now derived on every redraw (`analytics_loading_bar`).
- **The overview counted blocked assets the broker values at zero**: a blocked fund or share was summed in at the broker's price, so on the account the reconnaissance used the structure's total read 2 266 against an equity of 83. Blocked positions are now out of the base and every breakdown, the concentration, the leverage and the Оценка panel's day and cost; an account holding nothing else has a real zero day (`blocked_assets`).
- **A blocked symbol silenced the quote stream**: one blocked symbol in a `SubscribeQuote` subscription stops it delivering anything for any of its symbols, without an error — and opening a blocked instrument's profile from the search put it into the positions' shard. The client now keeps blocked symbols out of every subscription, never asks `LastQuote` or `GetAssetParams` about one (both hang), and the stream check no longer waits for them (`blocked_assets`).
- **Bonds were understated by face/100 on the Positions tab and in Analytics**: a bond's price is a percentage of face, and its value was taken as price × quantity. Ten ЯНДЕКС1Р1 at 100.72 showed 1 007.20 instead of 10 072, and a replacement bond with a 200 000 USD face showed 97.25. Positions are now valued through the face; with that, the overview reconciles with the broker's equity to the accrued interest (0.18% on the account the reconnaissance used, where it had been 13% short) (`analytics_currency`).
- **Money columns mixed currencies silently**: the Value, Daily P&L and Unreal P&L cells of a position in another currency than the account's now carry its code (`2516.80 USD`) (`analytics_currency`).
- **Payout currencies**: the bond calendar names currencies with symbols, so «Выплаты» showed `₽`/`$` and split a rouble coupon (`₽`) and a rouble dividend (`RUB`) into two totals. Calendar currencies are now ISO codes (`analytics_currency`).

### Reconnaissance (real API, 2026-09-11)
- **Blocked instruments live on two venues**: `_SPBZ` (436 in the bulk list, tickers suffixed `.SPBZ`) and `_MMBZ` (384, `.MMBZ`). `is_archived` is false on all 17 012 instruments, and the names on these venues are cut at 30 characters — 110 of the 820 lost the word `BLOCKED` — while `Block` appears in 13 live names, so the venue is the only reliable mark (`blocked_assets`).
- **`FXRL` arrives without a MIC** and the list knows only its twin `FXRL.MMBZ@_MMBZ`; its `unrealized_pnl` is 0.0 although the price halved, and the account's equity equals its cash to the kopeck, the second day running. 621 of the 820 blocked instruments share their base ticker with a live listing (`blocked_assets`).
- **Three calls fail on a blocked symbol**: `GetAssetParams` and `LastQuote` hang, and a subscription carrying one delivers nothing for any symbol (`SBER` alone: 56 quotes in 12 s; with `FXRL.MMBZ@_MMBZ`: none). `GetAsset`, `Bars`, `Schedule` and the calendars answer promptly and empty (`blocked_assets`).

### Reconnaissance (real API, 2026-09-10)
- **`bond_details.currency` is `%` on every bond**, a replacement bond with a USD face included. It is the unit of the price, not the face currency, which the API names only in the bond calendar, as a symbol (`₽ $ € ¥`). `quote_currency` is the settlement currency (`analytics_currency`).
- **Bond prices are percent of face and P&L is money**: the unrealised result of a live bond position adds up only through the face (`analytics_currency`).
- **The broker values foreign cash at the last MOEX TOM trade**: the sum of positions, bonds at the dirty price and cash reconciled with the reported equity to the kopeck at two separate moments with the dollar at the last `USD000UTSTOM` trade, and was 6 kopecks off with the forex rate (`analytics_currency`).
- **Every EUR pair on MOEX is frozen at 2025-01-09** and still answers `LastQuote` with its old price; only the timestamp tells. `#WWCP` carries live EUR/RUB, USD/RUB, CNY/RUB and INR/RUB (`analytics_currency`).
- **`GetAssetParams` prices one piece**: margin × 100 / risk rate / lot equals the instrument's dirty value in its settlement currency, face and conversion included. That is how a foreign-face bond is recognised without a calendar request (`analytics_currency`).
- **The corporate-actions service is unreliable**: 10 of 30 calendar calls ended `DeadlineExceeded` after 30 s (`analytics_currency`).
- **Not observed**: P&L units of a foreign-currency position (no account held one), which a per-position consistency check covers; weekend quote behaviour (`analytics_currency`).

### Reconnaissance (real API, 2026-09-04)
- **Daily bars cap at 366 days**: 366 answers, 367 is rejected with `InvalidArgument: Invalid date range`. The since-open horizon is routinely longer, so the benchmark samples two narrow windows at the ends rather than requesting the range whole (`analytics_history`).
- **A seven-day bar window can be empty**: `2010-01-01 + 7d` returned zero bars — the Russian New Year holidays run to the 10th. The benchmark window is 14 days, because an account opened in early January is an ordinary case (`analytics_history`).
- **Quota names are lower camelCase** (`AccountsService.trades`). Matching the bare method name would also catch `OrdersService.subscribeTrades` and `MarketDataService.latestTrades`, so the pre-check matches the dot-qualified suffix case-insensitively (`analytics_history`).
- **A refused request still spends its quota**: fifteen calls rejected with `NotFound` decremented the remaining count all the same. That is the argument for the pre-check's reserve, and against any automatic retry (`analytics_history`).
- **Not established**: `Trades`/`Transactions` limit semantics, accepted interval width, the category trade transactions arrive under, and whether `COMMISSION` carries a symbol. The available token has no trading account, and the server validates the account before the interval, so there is no way round it. Those move to the manual smoke (`analytics_history`).

### Reconnaissance (real API, 2026-09-03)
- **`Asset.Type`**: the full catalogue (291 890 instruments over 98 pages) uses ten values, four of them unplanned — `INDICES`, `SPREADS`, `SWAPS` and `OTHER`. `OTHER` is the largest bucket at 41%, so the Прочее row is a normal sight rather than a symptom. The six expected values keep their groups and everything else falls through to Прочее.
- **`GetUsageMetrics`**: 39 quotas, `limit` 200 for all but `ReportsService.createAccountReport` (3), a 60-second window, and — the fact that shaped the renderer — `reset_time` is absent for any quota untouched in the current window, which was 37 of the 39.
- **Not confirmed**: no available token carries a trading account (`TokenDetails.account_ids` is empty), so the `GetAccount` field census and the bond `current_price` format could not be observed. The mapping is written nil-safe per field, the bond face-value multiplier stays disabled, and the gap is covered by the track's manual smoke test.

## [v0.16.0] - 2026-08-27

### Added
- **Index Tab**: a fourth tab showing the composition of the MOEX Index (IMOEX) — ticker, Russian name, price, session change (absolute and percent), weight and volume, sorted by weight. `Enter` opens the instrument profile and `A` the standard order modal, both through the existing paths with the correct trade lot. The tab is account-independent (`index_tab`).
- **`AssetsService.GetConstituents`**: `Client.GetIndexConstituents` collects the index composition across the cursor pagination (guarded at 10 pages), caches it per index symbol for 24h, and keeps serving the previous composition when a refetch fails or comes back empty (stale-on-error). An empty response is treated as a failed load and never cached (`index_tab`).
- **`models.Quote.Change`**: the broker's own session change (`quote.change` = last − close) is now mapped into the quote model. It arrives with every `LastQuote` response and every `SubscribeQuote` message, so the Index tab renders Chg and Chg% without a single extra API call (`index_tab`).
- **Paced quote fallback for the Index tab**: rows the realtime streams do not cover are filled by a sweep that issues one `LastQuote` per request, 150ms apart, at most once a minute and only while the tab is open. With all shards up the sweep issues no request at all — it exists for shards that are down or symbols beyond the shard limit. A `ResourceExhausted` answer turns automatic refresh off for the session, reports it in the status bar and leaves `R` working (`index_tab`).
- **Positions-stream guard**: if the subscription does not come up within 60s of the index composition joining it (or drops three times), the index symbols are excluded for the rest of the session so portfolio quotes recover, and the tab falls back to the sweep (`index_tab`).
- **User manual**: new page [«Вкладка Индекс»](docs/user_manual/index-tab.md) (`index_tab`).
- **Mock server**: `MockAssetsServer.GetConstituents` with paginated fixtures and per-call error, empty and endless-cursor injection; `MockMarketDataServer.SubscribeQuote` driven by a queue with error injection (`index_tab`).

### Changed
- **Sharded quote subscription**: measured against the real API, one `SubscribeQuote` subscription accepts exactly 15 symbols and the limit applies per subscription rather than per connection. The client now splits the symbol set across parallel streams — one worker per shard, each with its own reconnect and backoff, capped at 8 shards — so the whole index is realtime at zero unary calls and a failing shard cannot take the others down. `SubscribedSymbols()` reports the live symbols, and quote polling resumes only for positions their shard is not delivering (`index_tab`).
- **Adaptive symbol cap**: a subscription refused with `InvalidArgument: Maximum number of symbols exceeded` is no longer an outage. The client halves the refused count, truncates from the end of a priority-ordered list so portfolio positions are never dropped, and resubscribes immediately. It stays as a safety net behind sharding, for the day the broker moves the limit (`index_tab`).
- **Quote subscription order**: `SetQuoteSymbols` now preserves the caller's order rather than sorting, because that order is priority order for the broker's symbol cap. Resubscribe decisions compare the capped sets ignoring order, so a reshuffle that subscribes to the same instruments still costs nothing (`index_tab`).
- **Load errors in the interface**: failures to load data from the broker now read `Ошибка при загрузке данных от брокера. Попробуйте обновить позже.` instead of the raw gRPC status text, which arrived with internal codes and English and Russian run together. Applied to the Index tab, positions, the account summary, the status bar and search; the technical cause is still written to the log. Order-placement errors keep the broker's own reason, which is actionable (`index_tab`).
- **Tab navigation**: the tab set is now derived from a single `tabLabels` list instead of two hardcoded modulo-3 expressions, so ←/→ cycle all four tabs (`index_tab`).
- **`GetQuotes`**: a rate-limited (`ResourceExhausted`) response now ends the batch and is returned to the caller instead of being logged per symbol and skipped — one refused call must not become dozens (`index_tab`).
- **Closing an instrument profile** returns focus to the tab it was opened from, with the selection intact, instead of always to Positions (`index_tab`).

### Fixed
- **Bond coupons were missing from the payout forecast**: `GetPastBondsEvents` refuses a `date_to` of today with `InvalidArgument: Invalid arguments:date_to` — which is exactly what the shared past-year range produced — so every bond in a portfolio failed its calendar, contributed nothing to the expected payouts, and left one unexplained line on the screen naming the instrument. The request now carries no interval at all, which the API documents as defaulting to a year and which returns the same events. Measured against the live API on 2026-09-09: a `date_to` of today is refused at 30 days, six months and a year alike, while the same window ending yesterday is accepted; the sibling dividend and split calendars accept a `date_to` of today and are unchanged. The mock server now enforces the same validation, so the shape cannot regress (`analytics_polish`).
- **Payout status wording**: a failed calendar now reads `календарь выплат недоступен: <тикер>` rather than `нет данных: <тикер>`, which sat above the whole screen and read as if the screen itself had no data (`analytics_polish`).
- **A payout the broker has not priced no longer shows as `0.00`**: a value reported as zero — how a floating-rate bond's next coupon arrives, dated but not yet priced — is a sum that is not set yet, not a payment of nothing, and rendering it as `0.00` asserted the opposite. Such a payout keeps its date, shows `—` where the money would be, stays out of the 30/90 totals (which no longer gain a `0.00 RUB` line for a currency that has nothing coming), and the panel says `по N выплатам сумма не определена`. It is not counted as a record the screen failed to read: it is shown, not dropped (`analytics_polish`).
- **Session expiry after sleep**: the client now watches the session expiry itself and re-authenticates two minutes before it, checking every 30 seconds, alongside the `SubscribeJwtRenewal` stream. The stream alone was not enough — the broker counts its ~14 minute delivery schedule from the subscribe and sends nothing when a stream is reopened, so every reconnect pushed the next token back while the current one kept expiring, and a machine that slept through the session woke with a dead token and `Unauthenticated` on every RPC until the broker's own schedule came round (`index_tab`).
- **Quote shard reconnect delay**: a shard that had been delivering data now restarts its backoff at 1s instead of inheriting a grown delay, so a blip on a healthy stream no longer costs up to 30 seconds without quotes. The delay still doubles to 30s for a subscription that never delivered anything (`index_tab`).
- **Index-stream guard blaming unrelated outages**: once the subscription has come up while carrying the composition, the guard's clock stops for the session. Before, every symbol recompute restarted it, so a later outage with a different cause — a sleeping machine, an expired session — could still cost the tab its quotes (`index_tab`).
- **Index tab header and width**: the header row is pinned (`SetFixed`) and every cell carries its column's expansion. tview derives both the visible rows and the column widths from the rows on screen, so scrolling the 46-row list used to take the column labels off screen and collapse the table to its content width (`index_tab`).
- **`Tab` key on the Index tab**: `Tab`/`Shift+Tab` from the account table now focuses the active tab's table through `activeTabTable()` instead of a hardcoded switch over the first three tabs, which left the focus stuck on the account table while Index was active (`index_tab`).
- **Index tab loading state**: entering the tab starts the composition load before drawing, so it reports `Loading` instead of `No constituents`, and fetches quotes as soon as the composition lands rather than waiting for the next background tick (`index_tab`).
- **Doomed lot-resolution calls**: a quote request made without an account context no longer tries to warm the lot caches. `GetAsset` and `GetAssetParams` both require an account, so those calls could only fail — and because they failed nothing was cached and they repeated on every pass, producing 494 guaranteed errors in a single session. Each swept symbol now costs one request instead of three (`index_tab`).
- **`GetQuotes` and `GetSnapshots` deadlines**: every `LastQuote` now carries its own deadline instead of the whole batch sharing one 30s context, which made the later symbols of a long batch fail with `DeadlineExceeded` while the broker was answering normally (`index_tab`).

## [v0.15.0] - 2026-08-25

### Added
- **Auto Update**: the terminal now checks GitHub Releases once a day in the background, shows the available version next to the running one in the header as `⚡ vX.Y.Z`, and offers to download, verify and install it at the next launch before restarting itself. The indicator is informational only — no popups over the trading interface — and the same dialog is available on demand via the `U` hotkey (`auto_update`).
- **`updater` package**: standard-library only — semver comparison, the `~/.finam-cli/update.json` state cache, the GitHub Releases client, the daily scheduler, verified asset download and the atomic binary replacement with rollback (`auto_update`).
- **Release checksums**: `checksums.txt` is now generated and published with every release, and the self-update verifies the downloaded binary against it (falling back to the asset size for older releases) (`auto_update`).
- **`config.UserConfigDir()`**: the `~/.finam-cli` path is now resolved in one place, shared by the token `.env` and the update cache (`auto_update`).

### Changed
- **Header**: renders through the new `headerLabel` helper with dynamic colours; the text is unchanged when no update is available, and gains a `⚡ <version>` segment when one is (`auto_update`).

## [v0.14.0] - 2026-08-25

### Added
- **Realtime Quotes**: quotes for the active account (and an open instrument profile) now arrive over the `SubscribeQuote` gRPC stream instead of a 5-second poll per position; the stream manager reconnects with backoff, resubscribes when the symbol set changes, and merges incremental updates using the 2.19.0 `is_data_snapshot` flag (`trade_lot_and_realtime_quotes`).
- **Quote Polling Fallback**: if the stream drops, the next 5-second tick resumes polling automatically; inactive accounts and chart bars keep polling as before (`trade_lot_and_realtime_quotes`).
- **Trade Lot Size**: `GetAssetParams.trade_lot_size` (2.18.1) is now the primary lot size for order sizing, shown as `Trade Lot` in the profile Trading section and in the order modal label `Lots (size - N)` (`trade_lot_and_realtime_quotes`).

### Changed
- **Finam Trade API SDK** updated to commit `ac0abdd` (2026-08-13), covering releases 2.18.0–2.19.0 (`trade_lot_and_realtime_quotes`).
- **Lot Resolution**: two-tier cache — the trade lot from `GetAssetParams` wins over the asset lot from `GetAsset`, with a negative cache entry when the API reports no trade lot; positions, the order modal, the close modal, `PlaceOrder` and `PlaceSLTPOrder` all read the same resolved value (`trade_lot_and_realtime_quotes`).

## [v0.13.0] - 2026-08-24

### Added
- **Corporate Action Calendars**: Instrument profile now shows dividend and split calendars for equities and coupon/amortization/offer calendars for bonds, via the new `CorporateActionsService` (`GetDividends`/`GetSplits`/`GetBondEvents`); each section is capped at 3 past + 3 future with a `…` overflow hint (`corporate_actions_and_trade_enrichment`).
- **Trade НКД**: History tab shows a combined `НКД` column (accrued interest + currency, e.g. `12.34 RUB`) for bond trades, blank for others (`corporate_actions_and_trade_enrichment`).
- **Order Link Marker**: Orders tab marks a stop order and the exchange order it triggered with a `↳` cross-reference (`corporate_actions_and_trade_enrichment`).

### Changed
- **Finam Trade API SDK** updated to commit `ee013ef` (2026-07-07), past releases 2.15.0–2.17.0 (`sdk_update_new_api_keys`).
- **API Key Format**: onboarding, `.env.example`, and `README.md` now point to `https://api.finam.ru/tokens/` and describe the new short `tapi_sk_...` key format; old long tokens remain supported as Legacy (`sdk_update_new_api_keys`).
- **Token Refresh**: replaced timer-based re-authentication with the `SubscribeJwtRenewal` gRPC stream for automatic JWT renewal, including reconnect with backoff (`sdk_update_new_api_keys`).

### Fixed
- **Startup Authentication**: `AuthService.TokenDetails` is now called without the `Authorization` metadata header — the API rejects calls that carry both the header and the body token, which broke account loading at startup with `InvalidArgument: Token is invalid or malformed`.
- **Slow First Connect**: `grpc.WithDisableServiceConfig()` skips the `_grpc_config.api.finam.ru` TXT lookup that Finam does not publish; its ~11s NXDOMAIN alone consumed the 10s deadline of the first `Auth` call (measured: 12.2s connect vs 0.3s RPC).
- **Token Expiry Source**: session token expiry now comes from `TokenDetails.expires_at` (readable via the new `TokenExpiry()` getter) instead of a JWT parser that always failed on the opaque `tapi_ak_...` token and fell back to an invented 50m lifetime (real: 15m).

## [v0.12.0] - 2026-04-07

### Added
- **Integration Test Suite**: In-process mock gRPC server (`api/testserver/`) covering all 5 Finam services, plus integration tests for client lifecycle, asset cache, token refresh, and gRPC error paths (`integration_testing`).
- **CI Coverage & Race**: CI split into unit-test, integration-test, coverage, and lint jobs with `-race` and merged coverage reporting (`integration_testing`).
- **Extended Instrument Info**: Profile screen renders futures (expiration, contract size), options (+ strike), and bonds (face value, currency); open interest shown in Quote section for derivatives (`extend_instrument_info`).
- **Real Version Display**: New `version/` package; TUI header shows the actual build version via `-ldflags -X`, with `runtime/debug.ReadBuildInfo()` fallback rendering `dev (<sha>[, dirty])` for local builds (`app_version_display`).
- **Makefile**: `make build` injects version metadata; `make test`, `test-integration`, `test-all`, `test-race`, `coverage`, `lint` shortcuts (`integration_testing`, `app_version_display`).

### Changed
- **Go 1.26**: Toolchain bumped to Go 1.26; codebase modernized via `go fix` (`rangeint`, `minmax`, `stringscut`, `any`); dependencies refreshed (`go126_upgrade`).
- **Finam Trade API SDK** updated to 2.14.0 with new derivative/bond fields and open interest (`extend_instrument_info`).
- **Release Workflow**: `.github/workflows/release.yml` injects `Version`/`Commit`/`BuildDate` per matrix artifact via ldflags (`app_version_display`).

## [v0.11.0] - 2026-03-13

### Added
- **Advanced Order Types**: Order modal now supports Limit, Stop-Loss, Take-Profit, and linked SL/TP pair orders with dynamic price fields and auto-selected stop conditions (`stop_loss_take_profit`).
- **Order Management**: Cancel active orders (X/Del) and modify orders (E) directly from the Orders tab with confirmation dialogs (`order_management`).
- **Enhanced Orders Table**: Richer columns showing stop conditions, limit/stop prices, validity, and executed/remaining quantities per order type (`order_management`).
- **Account List Redesign**: Two-row format per account — ID on first row, Equity + daily P&L (color-coded) on second row (`account_list_redesign`).
- **Number Formatting**: Thousand-separator formatting with spaces (Russian locale) for all monetary values (`account_list_redesign`).
- **PlaceSLTPOrder API**: New method for placing linked stop-loss + take-profit order pairs where one cancels the other (`stop_loss_take_profit`).
- **CancelOrder API**: New method for cancelling active orders via gRPC (`order_management`).

### Changed
- **Finam Trade API SDK** updated with `PlaceSLTPOrder` support (`stop_loss_take_profit`).
- **Account List** removed Type column in favor of the two-row Equity/PnL layout (`account_list_redesign`).

### Fixed
- **Order Status Mapping**: All order statuses including SL/TP-specific ones are now correctly mapped (`order_management`).
- **Price Auto-Fill**: Order modal pre-fills price fields with current market price from search results (`stop_loss_take_profit`).

## [v0.10.1] - 2026-03-05

### Added
- **Detailed gRPC Error Logging**: All 16 gRPC API calls now log errors in a unified format including service, method, request parameters, gRPC status code, error message, and endpoint — makes broker support diagnosis significantly easier (`detailed_grpc_logging`).

### Fixed
- **Broker Error Indication**: Accounts that fail to load from the broker now display an error indicator in the UI instead of silently showing stale data (`detailed_grpc_logging`).
- **Portfolio Preservation**: Portfolio data is preserved on transient API errors and Equity/PnL values update in real-time.

## [v0.10.0] - 2026-02-24

### Added
- **Instrument Profile**: Full-screen instrument profile overlay opened via Enter on positions or P on search results, displaying asset details, trading parameters, quotes, trading schedule, and a Unicode candlestick chart with switchable timeframes (M5/H1/D/W) (`instrument_profile`).
- **Candlestick Chart**: Unicode-based price chart with smart time labels on X-axis and support for multiple timeframes (`instrument_profile`).

### Fixed
- **Local Timezone**: All dates in History and Orders tables now display in the user's local timezone instead of UTC (`local_timezone_dates`).
- **Code Formatting**: Fixed `gofmt` formatting across all Go source files.

## [v0.9.0] - 2026-02-13

### Added
- **Portfolio Tabs**: Tabbed interface within the Positions window with History (trade history) and Orders (pending orders) views, switchable via arrow keys and Tab (`portfolio_tabs`).
- **Lot-Based Trading**: Quantities displayed in lots across Positions, History, and Orders tables; lot-based input in Buy/Close modals with real-time cost calculation and lot size display (`lot_based_trading`).
- **Human-Readable Names**: Descriptive instrument names (e.g., "Sberbank" instead of "SBER") displayed across all tables and modal titles, with automatic caching and fallback to ticker symbols (`human_readable_names`).

## [v0.8.1] - 2026-02-04

### Added
- **Security Search**: Dedicated full-width search window for finding assets and initiating orders (`security_search`).

## [v0.8.0] - 2026-01-26

### Added
- **Community Health**: Added `CONTRIBUTING.md` with detailed development guidelines.
- **Community Health**: Added `LICENSE` file (Apache 2.0).
- **Documentation**: Added `CHANGELOG.md` to track project history.
- **Documentation**: Added status badges (CI, Go Report, License, Version) to `README.md`.
- **Documentation**: Added "Development with Gemini and Conductor" section to `README.md`.

## [v0.7.0] - 2026-01-26

### Added
- **Portfolio View**: Comprehensive view of current portfolio holdings (`portfolio_view`).
- **Order Placement**: Ability to place market and limit orders (`order_placement`).
- **Position Closing**: Dedicated modal and logic for closing existing positions (`close_position`).
- **Startup Wizard**: Interactive initial setup and UI for API token configuration (`startup_setup`, `startup_ui`).
- **Token Management**: Proactive token refresh to maintain session validity (`proactive_token_refresh`).
- **UI Layout**: Full-width positions table for better visibility (`full_width_positions_table`).
- **CI/CD**: GitHub Actions pipeline for automated testing and builds (`github_actions_pipeline`).

### Changed
- **UI Responsiveness**: Improved interface adaptation to terminal resizing (`ui_responsiveness`).
- **UX**: Enhanced quantity input handling in order forms (`improve_qty_input`).
- **Filtering**: Automatically filter out positions with zero quantity (`filter_zero_positions`).
