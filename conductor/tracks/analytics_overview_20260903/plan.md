# Plan: Вкладка Analytics — обзор портфеля, маржа и риск, квоты API

## Overview
Добавляем пятую вкладку Analytics с под-экранами внутри (в этом треке `[1] Обзор  [2] API`): структура портфеля по типам и секторам, кэш и концентрация, маржа и риск — всё из данных, которые терминал уже получает каждые 5 секунд (`GetAccount`, стрим котировок, bulk-список активов, состав IMOEX), плюс таблица квот `UsageMetricsService.GetUsageMetrics` за один запрос при входе и по `R`. Расчёты живут в новом чистом пакете `analytics/`, маппинг новых полей — в `api/`, экраны — в `ui/analytics*.go`. Каркас вкладки (перечисление под-экранов, кеши по счёту) сразу проектируется под трек 2 (Сделки, Деньги, Выплаты). Разработка по TDD.

## Phase 1: Разведка на реальном API
- [x] Task: Одноразовая программа с реальным токеном (авторизация → bulk `Assets` → `GetAccount` по каждому счёту → `GetUsageMetrics`): зафиксировать в spec.md фактические значения `Asset.Type`, вид портфеля и заполненность `cash`/`available_cash`/`initial_margin`/`maintenance_margin`/`first_trade_date` по счетам, формат `current_price` облигационной позиции (если облигация есть), имена и окна квот; временный код удалить (7fd2ae7)
  - Acceptance: spec.md дополнен разделом с фактами; таблица соответствия типов и решение по формуле облигаций зафиксированы

## Phase 2: Модели и API-слой
- [x] Task: (Red→Green) `models.CashBalance` и новые поля `AccountInfo` (`PortfolioKind`, `Cash`, `AvailableCash`, `InitialMargin`, `MaintenanceMargin`, `MoneyReserved`, `HasMarginData`, `FirstTradeDate`, `FirstNonTradeDate`) + маппинг oneof `portfolio`, списка `cash` и дат в `GetAccountDetails` (api/client.go); фикстуры testserver: ответ счёта с `cash` и `portfolio_mc`, второй счёт с `portfolio_forts`; юнит-тесты маппинга (MC, FORTS, пустой oneof → `HasMarginData=false`, nil-поля) и интеграционный тест полей через bufconn (15d14b1)
  - Acceptance: тесты зелёные; существующие тесты `GetAccountDetails` не сломаны
- [ ] Task: (Red→Green) `SecurityInfo.Type` + сохранение `Asset.Type` в `loadAssetCache` (карта тип по полному символу и по тикеру) + `Client.GetInstrumentType(symbol string) string`; фикстуры `DefaultAssets()` получают типы (акция, облигация, фьючерс); тесты: поиск по символу и по тикеру, пустая строка для неизвестного
  - Acceptance: тесты зелёные; кеш активов по-прежнему грузится одним запросом
- [ ] Task: (Red→Green) `models.QuotaUsage` + `Client.GetUsageMetrics()` (`usageMetricsClient` в `newClientFromConn`, `logGRPCError`, nil-safe `reset_time`) + `MockUsageMetricsServer` в api/testserver (седьмой сервис: фикстура `DefaultQuotas()` с тремя квотами разной заполненности, `GetUsageMetricsError` для инъекции, счётчик вызовов); юнит-тест маппинга и интеграционные тесты (успех, ошибка, пустой список)
  - Acceptance: тесты зелёные; `TestServer` регистрирует семь сервисов
- [ ] Task: `ui.APIClient` + мок-клиент ui-тестов: `GetUsageMetrics`, `GetInstrumentType` (счётчики вызовов в моке для критериев «0 запросов на тике»)
  - Acceptance: `go build ./... && go vet ./...` чистые; существующие ui-тесты зелёные

## Phase 3: Пакет analytics — структура, риск, квоты
- [ ] Task: (Red→Green) `analytics/number.go` (разбор строковых чисел с запятой и «N/A», счётчик `Skipped`) и `analytics/structure.go`: `Structure(StructureInput) Allocation` — база долей (позиции + кэш базовой валюты), выбор базовой валюты (RUB → первая запись → RUB), группы по типам через переменную-таблицу соответствия, строка Кэш, секторы по карте символ→сектор, топ-3 и число позиций, множитель номинала для облигаций (параметр, по умолчанию 1), исключённые иновалютные позиции; табличные тесты: сумма долей 100%, неизвестный тип → Прочее, сектор только для бумаг из карты, нулевая база → `Valid=false`, позиция без цены → счётчик, отрицательный кэш не входит в базу
  - Acceptance: тесты зелёные; функции не паникуют на пустом входе и не возвращают NaN/Inf
- [ ] Task: (Red→Green) `analytics/risk.go`: `RiskMetrics(RiskInput) Risk` — MC (использование, запас, плечо), FORTS (использование ГО, плечо «Н/Д»), MCT/пустой oneof («Н/Д»), нулевое эквити → «Н/Д»; уровни цвета по порогам 50/80 и 50/20; табличные тесты границ порогов и всех видов портфеля
  - Acceptance: тесты зелёные
- [ ] Task: (Red→Green) `analytics/quotas.go`: `SortQuotas` (по доле остатка, `Limit=0` в конец) и `QuotaLevel` (пороги 20/50); тесты сортировки и уровней
  - Acceptance: тесты зелёные; покрытие пакета ≥ 80%

## Phase 4: UI — вкладка, Обзор, API
- [ ] Task: (Red→Green) Регистрация вкладки: `TabAnalytics` в `tabLabels`, `AnalyticsView{Layout, Header, Pages, Overview, Quotas}` в ui/analytics_render.go (шапка под-экранов, две колонки `TextView` для Обзора, таблица квот с `SetFixed(1, 0)` и расширением на всех ячейках, строка состояния под шапкой), страница "analytics" в `TabbedView.Content`, `SetTab`, `activeTabTable()` через фокусируемый примитив под-экрана; тесты: цикл ←/→ по пяти вкладкам, заголовок содержит " Analytics ", фокус после `SetTab`, шапка `[1] Обзор  [2] API`
  - Acceptance: тесты зелёные; существующие тесты табов не сломаны
- [ ] Task: (Red→Green) ui/analytics.go: состояние вкладки (`analyticsState`: перечисление под-экранов, активный под-экран, кеш квот, карта кешей по счёту под трек 2), клавиши `1`/`2` и `R`/`К` в input.go для вкладки, подсказки статус-бара «1-2 Экран  R Обновить»; тесты обработчиков и статус-бара
  - Acceptance: тесты зелёные; переключение под-экранов не делает запросов
- [ ] Task: (Red→Green) Обзор из памяти: общий помощник `positionValue(pos, quote)` (та же формула, что колонка Value в `updatePositionsTable`, плюс множитель номинала по решению разведки) и его использование в Positions; `updateAnalyticsOverview(app)` собирает `StructureInput`/`RiskInput` из позиций, котировок, `AccountInfo`, `GetInstrumentType` и состава индекса и рендерит две колонки (полосы, цвета, «Н/Д», строка секторов, счётчик «без цены»); вызов на тике рядом с `updateIndexTable` и в `applyAccountData` при активной вкладке; `ensureIndexLoaded()` на первом показе вкладки; счёт с `LoadError` → сообщение недоступности; тесты рендера: счётчик новых методов мока на тике = 0, `GetIndexConstituents` ≤ 1, «Н/Д» для FORTS/MCT, строка «состав индекса недоступен» при ошибке состава, смена счёта меняет данные
  - Acceptance: тесты зелёные; рендер использует только состояние в памяти
- [ ] Task: (Red→Green) Под-экран API: `loadQuotasAsync` (goroutine → `GetUsageMetrics` → QueueUpdateDraw) при первом показе и по `R`, состояние «Загрузка…», ошибка с подсказкой `R` (для `ResourceExhausted` пометка «лимит API»), рендер таблицы (сортировка `SortQuotas`, цвета `QuotaLevel`, полоса, «сброс через мм:сс», строка времени запроса), пустой список → «квоты не получены»; тесты: ровно один вызов при входе, по одному на `R`, ноль на тике, повторный вход без запроса, ошибка отображается
  - Acceptance: тесты зелёные

## Phase 5: Документация и верификация
- [ ] Task: Полная авто-проверка — `go build ./...`, `go vet ./...`, `go test ./...`, `go test -tags=integration ./api/...`, `CGO_ENABLED=1 go test -race` обеих сюит (при отсутствии C-компилятора локально — гейт CI), `make lint`, `make coverage` для новых пакетов
  - Acceptance: нет ошибок и предупреждений, линтер чистый, покрытие нового кода ≥ 80%
- [ ] Task: Документация — `docs/user_manual/analytics.md` (под-экраны, клавиши, формулы маржи, откуда берутся типы и секторы, поведение при ошибках) и ссылки из `index.md` и `interface-overview.md`; CLAUDE.md (пакет `analytics/`, файлы `ui/analytics*.go`, пункт «Usage Metrics (GetUsageMetrics)» и новые поля `GetAccount` в API Implementation Details, седьмой мок-сервис, `Testing`); `conductor/product.md` (Key Features); CHANGELOG.md; README.md
  - Acceptance: документация соответствует реализации
- [ ] Task: (Ручной смоук) Реальный ключ: вкладка пятая в цикле; Обзор показывает доли по типам с суммой 100%, секторы по бумагам IMOEX, кэш, топ-3, блок маржи с цветами; смена счёта меняет данные; под-экран API показывает квоты и в логе ровно один `GetUsageMetrics` на вход и по одному на `R`; за 5 минут на вкладке в логе нет `ResourceExhausted` и нет новых unary-вызовов сверх существующих. **Плюс отложенные пункты разведки** (фаза 1 не смогла их снять — у доступных токенов пустой `account_ids`): вид портфеля счёта (MC/MCT/FORTS), фактическая заполненность `cash`/`available_cash`/`initial_margin`/`maintenance_margin`/`first_trade_date`, формат `current_price` облигационной позиции (при её наличии) и, по итогу, нужен ли множитель номинала в `positionValue`
  - Acceptance: пользователь подтверждает поведение; отложенные факты разведки зафиксированы в spec.md
