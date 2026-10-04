# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **Configurable Commodities tab**: NG, Brent, palladium, platinum, gold, silver, cocoa, coffee and ES international continuous futures. `~/.finam-cli/commodities.json` controls visibility, order, descriptions, quote precision, MOEX family masks, rollover lead days and scale/FX conversion. The source and native MOEX tables share six columns and physical widths. MOEX prices are converted into source units below the detail, followed by a separate `converted MOEX − source` delta. ES is a SPY ETF proxy, not an identical underlying. All quote legs share the existing sharded stream and paced fallback.
- **Update enable setting**: `enabled` in `~/.finam-cli/update.json` disables background checks, startup offers, indicators and in-app updates without losing cached release information. Existing files without the key retain enabled updates.

### Changed
- Tab order: Positions → Orders → Commodities → Index → Analytics → History. Disabled Commodities is omitted from the header and navigation.
- Commodities resolves MOEX series by Finam's actual expiration timestamps and retains one active contract per family mask in the separate `~/.finam-cli/commodities_cache.json` (`updated_at`, `roll_days`, `contract`), shared across accounts. Daily tracking refreshes only the active contract; family discovery runs initially or at the rollover boundary. Atomic cache writes never modify user settings. Missing or corrupt cache data does not disable the tab and is rebuilt after a successful lookup. The old settings `futures` section is no longer used. Failed broker refreshes retain the previous contract and timestamp; expired contracts are excluded from display, subscriptions and orders.
- Removed the Commodities `Unit` column and the `unit` configuration field; profiles display the configured name without a unit suffix. Legacy `moex_symbol` must be replaced by `moex_mask` and `roll_days`.
- MOEX detail tickers use the same catalogue display names as History; both `Name` columns use the configured commodity description. Selection updates native quotes, conversion, delta and the order shortcut immediately.
- Commodity MOEX details are key-value groups below a separator: white contract plus expiry, MOEX price converted into Commodity units and converted-MOEX-minus-Commodity delta, and independent source/native quote timestamps. The `Updated` column is removed from both quote tables. Missing source prices preserve native conversion but leave delta unavailable; missing native/required FX leaves both unavailable. Expiry retains the independent `expiry_warning_days` window (default 5; zero disables warnings).
- Commodity source and MOEX tables share a fixed Ticker column and fixed-width numeric columns; Name absorbs spare terminal width.
- Contract MOEX and conversion groups use compact 26-column widths; updates retain 32 columns for full timestamps. Two-column gaps remain fixed. Conversion starts below Name; groups stack on narrow terminals.
- Pure commodity conversion and expiration selection now live in the `commodity` package rather than `config`, with no I/O or UI dependencies.

### Fixed
- Embedded commodity defaults now use the shipped `config/example/commodities.json`.
- Same-day empty futures selections are invalidated when `roll_days` changes, including after restart.
- Switching an international commodity profile's chart timeframe no longer triggers account-scoped asset and lot metadata requests.

## [v0.17.0] - 2026-09-14

### Added
- **Analytics tab**: a fifth tab with four screens, switched by the digit keys — `[1] Обзор  [2] Сделки  [3] Деньги  [4] Выплаты` (`analytics_overview`, `analytics_history`).
  - **Обзор** — what the portfolio is made of and what it is worth right now: the breakdown by instrument type, by currency and by sector with shares and bars; the value at the start of the day, the value now, the day's result and the result on open positions, each with its percentage; margin utilisation, the cushion to a margin call, leverage and the collateral FORTS positions tie up; the top-3 concentration; and the account's result since it opened. It is drawn from data the terminal already holds, so it refreshes with the rest of the screen every five seconds and costs no request of its own.
  - **Сделки** — the realised result of the chosen period, matched FIFO: the total, the win rate and the profit factor as headline figures, then the number of trades, the average win and loss, the best and the worst trade, and a table by instrument.
  - **Деньги** — the money that moved in the chosen period, split into deposits, withdrawals, commissions, taxes, payouts, trades and securities transfers and drawn as bars against the largest of them; plus the account's result since it opened, its annualised rate (XIRR) and the MOEX Index over the same period beside it.
  - **Выплаты** — the dividends, coupons, amortizations and offers due on the positions currently held, with what falls into the next 30 and 90 days.
- **Period presets**: `P` cycles `1М → 3М → 1Г → YTD → Всё` on the Trades and Money screens. YTD turns at your own midnight, not UTC's, and changing the period is instant — it filters history already loaded rather than asking the broker again (`analytics_history`).
- **The portfolio is measured in one currency**: every position and every cash balance is valued in its own currency and converted into the account's base at the exchange rate, so the structure, the sectors, the concentration, the leverage and the day's result all add up to the same total. Money bought in another currency joins the breakdown as a holding, the way the broker files it. A position that cannot be converted is counted apart (`без курса: N`) rather than quietly mixed in (`analytics_currency`).
- **The «Валюты» panel** on the overview: the portfolio broken down by currency over the same base as the breakdown by type, so both add up to 100%. Under the rows, one line per foreign currency gives the amount in its own money, the rate and when the rate was taken (`CNY 1 500.00 × 12.5300 · 14:05`). A rate older than 12 hours reads in yellow as `курс на 06.09 17:30` instead of passing for current, and a currency with no rate is shown in its own money and left out of every share. A rouble-only account shows a single row (`analytics_currency`).
- **Exchange rates**: read from the currency pairs the broker itself quotes — dollar and yuan from the MOEX session the broker values cash at, euro and rupee from the forex feed, plus tenge, dram, som, Belarusian rouble and lira. A rate is asked for at most once per currency every ten minutes and only while the overview is on screen; if the broker answers with a rate limit the terminal says `лимит API: курсы не обновляются, R — вручную` and `R` keeps working (`analytics_currency`).
- **Bonds with a foreign face value** — replacement bonds, yuan bonds settled in roubles — are recognised and valued in the currency of their face instead of at a fraction of their worth (`analytics_currency`).
- **Blocked assets are recognised**: a position the broker holds on a blocked venue — or sends under a ticker it lists only there, like `FXRL` — is identified from the instrument list loaded at startup, at no extra request. The Positions tab's Value column reads `BLOCKED` instead of an amount the broker does not count, and the overview says under its total what it is leaving out: `заблокировано: N · X RUB по цене брокера` (`blocked_assets`).
- **User manual**: new page [«Вкладка Analytics»](docs/user_manual/analytics.md) — how to read each screen, what every panel and column means, every state line the tab can print, and what each screen costs in requests (`analytics_overview`, `analytics_history`, `analytics_currency`, `analytics_polish`).

### Changed
- **A progress bar while the history loads**: the first visit to «Сделки» or «Деньги», and `R` afterwards, now shows one bar across the middle of the screen with the percentage in it. The old line of counters over empty frames had to be read, and the eye went to the frames, which looked like a broken screen. The bar counts steps that take real time, so it only moves forward and reaches 100% when the pass is genuinely done (`analytics_loading_bar`).
- **Analytics tab redesign**: the same figures, on a screen built to be read. Every panel is drawn as tall as its content instead of stretched to fill the column, and the slack is left unframed — a border around empty space read as a broken panel, and on the Money and Payouts screens three quarters of the height was exactly that. A table with nothing in it is collapsed entirely, so an account with no expected payouts shows one sentence instead of a sentence above an empty full-height table. Labels are joined to their figures with leader dots, share bars lost the grey background that made every row look alike, risk levels carry a `●` as well as a colour so the reading survives a monochrome terminal, and table headings line up with the columns they head. Rows are laid out to the panel's real width rather than a fixed 38 columns, so they reach the right edge on a wide terminal and give up the label rather than the figure on a narrow one. The tab is now framed like every other section of the terminal, with the screen tabs and the period as a strip inside that frame (`analytics_polish`).
- **«Маржа и риск» no longer lists foreign balances**: they moved to «Валюты», where a balance is shown once, with its rate. Loans stay where they were, because they are risk rather than holdings (`analytics_currency`).
- **The Positions tab's Value column** falls back to the broker's own price when the live quote has not arrived yet, instead of showing `N/A`. That column and the Analytics overview are now computed the same way, so the two screens cannot disagree about what a holding is worth (`analytics_overview`).

### Fixed
- **Bonds were understated roughly a hundredfold**: a bond's price is a percentage of its face value, and the terminal was treating it as the price itself. Ten ЯНДЕКС1Р1 at 100.72 showed `1 007.20` instead of `10 072`, and a replacement bond with a 200 000 USD face showed `97.25`. With bonds valued through the face, the overview reconciles with the broker's own equity to the accrued interest, where it had been 13% short (`analytics_currency`).
- **Money columns mixed currencies silently**: the Value, Daily P&L and Unreal P&L cells of a position held in another currency now carry its code (`2516.80 USD`) (`analytics_currency`).
- **The profile printed `%` as a bond's face currency**: `Face Value` read `1000.0 %` on every bond, a 200 000 USD replacement bond included. It now shows the currency the bond's own payout calendar names (`200000.0 USD`), and where that is unknown the face value is shown alone rather than with the wrong code (`analytics_currency`).
- **«Выплаты» split one currency into two totals**: a rouble coupon and a rouble dividend arrived under different spellings and were counted separately (`analytics_currency`).
- **The overview counted blocked assets the broker values at zero**: a blocked fund or share was summed in at the broker's price — on one real account the structure's total read `2 266` against an equity of `83`. Blocked positions are now out of every portfolio figure (`blocked_assets`).
- **A blocked instrument silenced realtime quotes**: one blocked symbol in a quote subscription stops it delivering anything at all, for any instrument, and without an error — and opening a blocked instrument's profile from the search was enough to trigger it. The terminal now keeps blocked symbols out of every subscription and never asks the broker about them (`blocked_assets`).
- **`R` on «Сделки» or «Деньги» broke the IMOEX comparison**: the refresh re-reads the last day and was handing that one-day window to the index comparison as well, so afterwards the comparison beside the account's whole-life result read ≈0% with `Годовых Н/Д — период короче 30 дней`. A refresh now leaves the comparison the full pass measured alone (`analytics_history`).
- **The very first moment of an account's history could be missed** by the history walk, and the progress bar could stop one step short of its own total (`analytics_loading_bar`).
- **«нет данных о начале истории счёта» vanished** as soon as the screen redrew itself (`analytics_loading_bar`).
- **A ticker the broker had already refused was asked about again and again**: a position sent without a venue that the instrument list does not know cost a failing request on every five-second tick, and another whenever the Positions tab refreshed its quotes. Such a ticker is now remembered for a day (`blocked_assets`).

## [v0.16.0] - 2026-08-27

### Added
- **Index tab**: a fourth tab showing the composition of the MOEX Index (IMOEX) — ticker, Russian name, price, session change in roubles and percent, weight and volume, sorted by weight. `Enter` opens the instrument profile and `A` the order modal, exactly as on Positions. The tab does not depend on the selected account (`index_tab`).
- **Fallback quote refresh on the Index tab**: rows the realtime streams do not cover are filled in one at a time, at most once a minute and only while the tab is on screen. If the broker answers with a rate limit, automatic refresh turns off for the session, the status bar says so, and `R` keeps working (`index_tab`).
- **User manual**: new page [«Вкладка Индекс»](docs/user_manual/index-tab.md) (`index_tab`).

### Changed
- **The whole index is realtime**: the broker accepts only fifteen instruments per quote subscription, so the terminal now spreads its instruments across several parallel subscriptions. All 46 index rows and your positions update live at the same time, without a single extra price request, and a subscription that fails cannot take the others down with it. Portfolio quotes always take priority over the index (`index_tab`).
- **Readable errors**: a failure to load data from the broker now reads `Ошибка при загрузке данных от брокера. Попробуйте обновить позже.` instead of raw status text with internal codes and English and Russian run together. The technical cause still goes to the log, and order-placement errors keep the broker's own reason, which is actionable (`index_tab`).
- **←/→ now cycle all four tabs** instead of the first three (`index_tab`).
- **Closing an instrument profile** returns to the tab it was opened from, with the selection intact, instead of always to Positions (`index_tab`).

### Fixed
- **Bond coupons were missing from the payout forecast**: every bond in a portfolio failed to load its calendar and contributed nothing, leaving one unexplained line on the screen naming the instrument. The terminal was making the one request shape the broker refuses for this calendar alone; it now asks the way the broker accepts (`analytics_polish`).
- **A payout the broker has not priced showed as `0.00`**: a floating-rate bond's next coupon arrives dated but not yet priced, and rendering that as zero asserted it pays nothing. Such a payout now keeps its date, shows `—` where the amount would be, stays out of the 30- and 90-day totals, and the panel says `по N выплатам сумма не определена` (`analytics_polish`).
- **Payout status wording**: a calendar that failed to load now reads `календарь выплат недоступен: <тикер>` rather than `нет данных: <тикер>`, which sat above the whole screen and read as if the screen itself had no data (`analytics_polish`).
- **The session died after the machine slept**: a laptop that slept through the session woke to an authentication error on every request, until the broker's own renewal schedule came round. The terminal now watches its session expiry itself and re-authenticates two minutes before it (`index_tab`).
- **Quotes froze for up to 30 seconds after a brief interruption**: a subscription that had been delivering data now reconnects after a second, instead of inheriting a delay grown for a stream that never worked (`index_tab`).
- **The Index tab could lose its realtime quotes for the rest of the session** because of an outage that had nothing to do with it — a sleeping machine, an expired session (`index_tab`).
- **The Index tab's header scrolled away and the table collapsed**: scrolling the 46-row list took the column labels off screen and squeezed the columns down to their content width. The header is now pinned and the widths hold (`index_tab`).
- **`Tab` did nothing on the Index tab**: focus stayed stuck on the account table (`index_tab`).
- **The Index tab said `No constituents` on entry** before it had even tried to load, and then waited for the next background tick to fetch prices. It now says `Loading` and fetches as soon as the composition arrives (`index_tab`).
- **Filling the Index tab cost three requests per instrument instead of one**, and filled the log with hundreds of guaranteed failures, because it also tried to look up trading parameters the broker cannot return in that context (`index_tab`).
- **The later rows of a long price batch showed nothing**: the whole batch shared one 30-second deadline, so the instruments at the end failed with a timeout while the broker was answering normally. Each request now carries its own deadline (`index_tab`).

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
