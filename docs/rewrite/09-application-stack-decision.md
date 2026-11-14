# Выбор стека приложения

Дата исследования: 11 ноября 2026 года; согласованность с UI baseline обновлена 14 ноября. ADR APP-01. **Рекомендация для переписывания и выбранный профиль планирования — Go + React/Mantine SPA** с SQLite, durable jobs, SeaweedFS и FFmpeg. Это технический выбор исследования, а не отдельное подтверждение языка или библиотек владельцем. Он заменяет прежнюю рекомендацию .NET 10 + Angular 22; .NET 10 + React остаётся сильной альтернативой в сохранённом сравнении.

По D-16 весь текущий интерфейс и поведение одобренного макета v27 зафиксированы как цель реализации; [baseline](prototype/accepted-baseline.json) связывает решение с HTML и исходниками. Внешний контракт больше не является набором UX-предложений: выбранные библиотеки должны воспроизвести его. Историческое сравнение стеков и технические feature gates не добавляют вариантов UI и не пересматривают D-01…D-16.

Владелец уточнил, что новую версию будут разрабатывать AI-агенты. Поэтому знакомство человеческой команды с C#/Angular не используется как главный аргумент. Предпочтение Go основано на небольшом прикладном framework, явном SQL и фиксируемых межмодульных контрактах. Предположения, что агенты всегда лучше пишут Go/React, а приложение будет существенно быстрее или экономнее, в решение не входят.

## Что определяет выбор

Продукт — один пользователь, браузер на том же современном Mac, Docker Desktop, несколько тысяч записей и около 100 GB фото/видео с дальнейшим ростом до сотен GB. Основные работы сервера — HTTP/S3 streaming, короткие SQLite-транзакции, выполнение FFmpeg и согласование durable состояний. Нет потребности в SSR, поисковой индексации страниц, distributed transactions, enterprise integration framework или Node runtime для UI.

Переписывание уже предполагает новую реализацию. Отказ от C# не требует постепенного изменения работающего сервиса: прежняя библиотека переносится отдельным offline-скриптом. Сохранение возможностей F-01…F-38, точных метаданных и семантики поиска остаётся обязательным при любом языке, с явно принятыми изменениями D-01…D-16 и одобренным v27 UI. [Функциональный реестр](01-current-behavior.md), [спецификация](02-product-specification.md).

Критерии: понятные границы ответственности; проверяемые API/SQL/состояния; единообразие кода агентов; воспроизводимая ARM64-сборка; полный media/UX-паритет; обновляемость зависимостей. Размер бинарника и популярность framework сами по себе не определяют пригодность.

## Основные варианты

| Вариант | Сильные стороны для Tag Files | Цена и ограничения | Вывод |
| --- | --- | --- | --- |
| **Go + React** | Стандартный HTTP, явные SQL и process/context API; генерация типов из SQL/OpenAPI; небольшое приложение без ORM/DI framework | Больше явного SQL, своя сборка lifecycle; CGO у выбранного SQLite driver; короткое окно поддержки Go | **Рекомендуется** для новой реализации с закреплёнными соглашениями |
| **.NET 10 + React** | Зрелые HTTP/hosting/cancellation, точный `long`, EF migrations и длинная поддержка; смена frontend независима от backend | EF/query translation и критические SQL требуют контроля; тот же S3/SQLite-протокол и supervision FFmpeg | Почти равноценная альтернатива; выбрать, если важнее длительная серверная поддержка и интегрированная платформа |
| **Node 26 LTS + Fastify 5 + React** | TypeScript на сервере и клиенте, JSON Schema, streaming и единое tooling | Точный int64 требует bigint-политики; синхронный SQLite требует контроля event loop; native addon и DB worker усложняют модель | Реальный третий вариант, если единый язык важнее минимального серверного framework |

Выбор Go не означает, что C# не подходит. ASP.NET Core уже умеет streaming без полного buffering, cancellation и фоновые службы. Очередь в памяти и преждевременное удаление input в прототипе — ошибки архитектурного протокола, а не свойства C#. Ограничение .NET SQLite также нужно учитывать: async ADO.NET операции провайдера выполняются синхронно. [ASP.NET streaming](https://learn.microsoft.com/en-us/aspnet/core/mvc/models/file-uploads?view=aspnetcore-10.0), [SQLite async](https://learn.microsoft.com/en-us/dotnet/standard/data/sqlite/async).

Для Node можно использовать зрелый better-sqlite3, BigInt mode и ограниченный DB owner/worker внутри процесса. Встроенный `node:sqlite` пока имеет статус release candidate; его синхронный API нельзя считать автоматическим устранением зависимости и проблем event loop. Это решаемые ограничения, но для данного приложения единый TypeScript не перевешивает их сам по себе. [Fastify schemas](https://fastify.dev/docs/latest/Reference/Validation-and-Serialization/), [Node SQLite](https://nodejs.org/api/sqlite.html), [better-sqlite3 integers](https://github.com/WiseLibs/better-sqlite3/blob/master/docs/integer.md).

### Другие backend

Rust + Axum/Tokio/SQLx технически покрывает требования, но ownership/traits/async и дополнительная сложность сборки не дают установленной продуктовой выгоды. SQLx выполняет blocking SQLite через worker thread; динамический AST-поиск не получает compile-time проверку SQL только от наличия `query!`. Compiler также не доказывает согласованность S3/SQLite или остановку дочерних процессов. Rust стоит пересмотреть при измеренном жёстком ограничении ресурсов. [SQLx SQLite](https://docs.rs/sqlx/latest/sqlx/sqlite/index.html), [query macros](https://docs.rs/sqlx/latest/sqlx/macro.query.html), [Tokio process](https://docs.rs/tokio/latest/tokio/process/struct.Command.html).

Python/FastAPI не получает преимущества от Python-specific нагрузки: обработка уже выполняется FFmpeg, ML в baseline отсутствует. Python либо C# могут оставаться языками внешних migration/backup tools независимо от runtime основного сервиса. Nest, Hono и Bun также способны обслуживать приложение, но не дают необходимой возможности, которой нет у трёх основных вариантов; дополнительные framework/runtime conventions здесь не являются целью.

## Frontend выбирается отдельно

| Вариант | Что даёт | Что потребуется контролировать |
| --- | --- | --- |
| **React + Vite** | Обычная static SPA, готовые UI kits, подходящий набор media plugins, typed OpenAPI client | Заранее закрепить router, server cache, формы, стили и обработку ошибок; React не поставляет их как одну платформу |
| **Angular 22 + Material/CDK** | Интегрированные router/forms/HTTP/dialogs/a11y, единообразие платформы | Связанные версии Angular/TypeScript/Node и соглашения signals/RxJS; upload/viewer всё равно отдельная интеграция |
| **Svelte 5 + SvelteKit 3 static SPA** | Компактные компоненты, scoped styles, layouts и маршруты без Node runtime | Закрепить runes/static conventions, подобрать другой viewer или написать adapters; меньше строк не доказывает меньше ошибок |

React выбран ради конкретной согласованной SPA-композиции, а не из-за предполагаемой неспособности агентов работать с Angular. Vue 3 также применим, но его промежуточная позиция между React и Svelte не даёт установленного преимущества именно этой медиатеке; это не отдельный обязательный pilot. [React SPA from scratch](https://react.dev/learn/build-a-react-app-from-scratch), [Angular releases](https://angular.dev/reference/releases), [SvelteKit SPA](https://svelte.dev/docs/kit/single-page-apps), [Vue overview](https://vuejs.org/guide/introduction.html).

Next.js, React Server Components, SSR и server actions не нужны: каталог локальный, SEO отсутствует, API и media уже обслуживает Go. Node требуется только на стадии сборки. CSS/иконки/fonts входят в образ, внешний CDN не используется.

## Рекомендуемый профиль Go

| Ответственность | Выбор |
| --- | --- |
| HTTP | `net/http`, method/path patterns `http.ServeMux`, явные middleware |
| JSON API | OpenAPI 3.0.3 как нормативный исходник; `oapi-codegen` для Go interfaces/models; `openapi-typescript` + `openapi-fetch` для TS |
| Большие тела и media | Отдельные raw `net/http` handlers в том же OpenAPI; JSON binder/validator не читает их body |
| SQLite | `database/sql` + `mattn/go-sqlite3`; закреплённая CGO-сборка с bundled SQLite, без случайной замены системной libsqlite3 |
| Запросы | `sqlc` для статических SQL; небольшой параметризованный AST → SQL compiler для boolean search |
| Schema updates | Reviewed SQL migrations и встроенная библиотека `goose`; только собственная новая схема |
| S3 | AWS SDK for Go V2, прямой multipart API; отдельные streaming и seekable-file пути |
| Jobs | SQLite jobs + один исполнитель; goroutines/channels только для выполнения и пробуждения |
| Медиа/логи | `os/exec` с supervision process group, FFmpeg/ffprobe; `log/slog` |
| Сборка/проверки | `go.mod`/`go.sum`, закреплённые генераторы; gofmt, vet, tests, race и воспроизводимая генерация |

Стандартного ServeMux хватает для method/path routing. `oapi-codegen` strict interfaces дают compile-time соответствие JSON handlers, но **не заменяют всю runtime-validation**. Для JSON закрепляется один validator; streaming endpoints обходят его buffering, сохраняя общие Host/Origin/CSRF и size policies. Генерация JSON server interfaces исключает streaming operations; общий OpenAPI продолжает описывать все маршруты. [ServeMux](https://pkg.go.dev/net/http#ServeMux), [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen), [openapi-typescript](https://openapi-ts.dev/introduction), [openapi-fetch](https://openapi-ts.dev/openapi-fetch/).

Huma — допустимая code-first альтернатива, но одновременно две системы контрактов не вводятся. Отдельный OpenAPI проще независимо рецензировать при параллельной работе агентов. ORM, Gin/Fiber, generic repository, mediator, второй SQL builder и DI framework не входят в baseline.

Выбранный mattn требует C compiler в builder, но не GCC в runtime; bundled SQLite фиксируется версией driver. Builder/runtime должны иметь совместимую libc и проверенную arm64/amd64-сборку. `modernc.org/sqlite` — полноценная альтернатива без обычного CGO build, но с собственной цепочкой C-port/libc зависимостей. Сам по себе pure Go не доказывает выигрыш RAM/скорости. В поставке всё равно остаётся FFmpeg, поэтому scratch/distroless и один полностью статический бинарник не являются целью. [mattn driver](https://github.com/mattn/go-sqlite3), [modernc driver](https://pkg.go.dev/modernc.org/sqlite).

`sqlc` генерирует типизированный доступ по SQL/schema и не выполняет миграции. Goose выполняет историю миграций; отдельный Atlas/declarative schema workflow не нужен. Политика SQLite: writer handle с одним соединением, небольшой ограниченный reader pool, FK/busy timeout/FULL на каждом физическом connection. `BEGIN IMMEDIATE` реализуется проверенным driver transaction mode либо закреплённым `*sql.Conn`, а не последовательностью `db.Exec` через произвольные pool connections. S3/FFmpeg не выполняются внутри SQL-транзакции. [sqlc SQLite](https://docs.sqlc.dev/en/latest/tutorials/getting-started-sqlite.html), [goose](https://github.com/pressly/goose), [database/sql](https://pkg.go.dev/database/sql).

### Особенности S3 и FFmpeg

У AWS SDK for Go V2 `io.Reader` в Body не означает, что SDK может подписать и повторить любой nonseekable поток. Для browser part задаются ContentLength, явный ChecksumSHA256 и проверенный signing profile; официальный unsigned-payload middleware может убрать потребность перечитывать тело для SigV4. **Unsigned payload не означает отсутствие checksum:** SeaweedFS обязан фактически отвергать тело с неверным SHA-256. Автоматический retry не должен перечитывать уже израсходованный reader; браузер повторяет те же bytes/digest. [AWS S3 utilities](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/sdk-utilities-s3.html), [checksums](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/s3-checksums.html).

`manager.Uploader` не используется как скрытый второй multipart coordinator: он добавляет собственные parts buffers/concurrency. У приложения уже есть sessions, immutable part bindings и общий admission limit. Для canonical PUT локальный output file seekable; это отдельный, более простой retry-путь. Complete → HEAD → полный GET/hash и fenced SQLite commit остаются обязательными независимо от SDK.

`exec.CommandContext` не является supervisor дерева процессов. В Linux нужен один wrapper: отдельная process group, TERM → ограниченное ожидание → KILL группе → reap/Wait; bounded stderr/progress, threads и scratch. Запуск — аргументами без shell. Проверка должна включать потомка, переживающего остановку родителя. [Go os/exec](https://pkg.go.dev/os/exec#CommandContext).

## Рекомендуемый профиль React

| Ответственность | Выбор и правило |
| --- | --- |
| Сборка | React 19.3, Vite 8.3, TypeScript strict; Node 26 LTS только builder |
| Маршруты | React Router Data mode; URL хранит q/savedSearchId/type/sort/page/seed/context, Back и scroll восстанавливаются; страницы по 24 материала |
| Server state | TanStack Query v5 — единственный API cache; loaders делегируют ему prefetch, mutations обновляют/инвалидируют те же keys |
| Формы/UI | Mantine core/hooks/form одной версии, CSS modules и штатный PostCSS setup; теги идентифицируются по ID, не label |
| Local state | useState/useReducer; versioned localStorage только для preferences и необходимых client receipts |
| Viewer | Кандидат реализации YARL с Zoom, Rotate, Video, Inline; один встроенный MediaViewer и canonical `/content/:id` из grid/list/related по D-14/D-16 |
| Upload | Отдельный coordinator, hash worker, bounded parts, XMLHttpRequest для PUT/progress; JSON client не буферизует file |
| Проверки | TypeScript compiler, ESLint/React Hooks rules, component/logic tests, Playwright и реальные Safari/Chrome/VoiceOver |

Такое владение state исключает три конкурирующих копии каталога в router, global store и `useEffect(fetch)`. У Query закрепляются retry/refetch defaults: changing-state requests не повторяются вслепую после тайм-аута; используются прежний Idempotency-Key и operation/session result. RHF, Zod, Redux/Zustand, Tailwind и второй UI kit не нужны по текущим требованиям. Это не запрет на будущие функции, а ограничение исходной композиции. [Router modes](https://reactrouter.com/start/modes), [Query defaults](https://tanstack.com/query/latest/docs/framework/react/guides/important-defaults), [Mantine Vite](https://mantine.dev/guides/vite/), [Mantine forms](https://mantine.dev/form/use-form/).

YARL документирует Zoom/Rotate/Video/Inline; окончательное закрепление пакета зависит от воспроизведения уже одобренного viewer по D-14/D-16: один маршрут из grid/list/related, media `80dvh`, текущие группы controls и скрытая панель `Image tools`, теги и коллекция ниже, точные video hotkeys, prev/next и Back из server navigation context, focus, portrait/landscape, browser zoom shortcuts и доступность. Проверяется технический способ реализации, а не новый дизайн. Отдельный overlay quick-preview не требуется; пакет не диктует два UI-режима. Новый JS viewer не добавляет отсутствующий браузерный codec. [YARL plugins](https://yet-another-react-lightbox.com/plugins), [Playwright browsers/codecs](https://playwright.dev/docs/browsers).

Остальная React-композиция так же сохраняет весь контракт v27 из [архитектуры](03-target-architecture.md) и [описания макета](prototype/README.md): English UI; четыре верхние sidebar-ссылки и Uploads внизу, ручной rail 72 px; grid без selection с popup тегов и `+`/`−`; list с hover/focus Edit и checkbox справа, выделением страницы и массовой заменой tags/collection; компактные Tags/Categories/Collections и поиск коллекций. Общий редактор использует tag chips + поиск и одно nullable searchable Collection поле; Save атомарно применяет оба draft, Cancel не меняет метаданные. Эти взаимодействия утверждены D-16; новые Settings/diagnostics screens, tag autocreate или bulk add/remove не следуют из выбора Mantine.

Счётчик единого viewer сохраняет правило трёх секунд, без новых условий видимости/playback. Thumbnails/grid/list не создают просмотр; прежнее различие quick-preview/detail больше не применяется. React StrictMode и повтор effects/retry одного открытия не должны создавать второй view event или второй upload: event ID устойчив, timers/listeners очищаются, GET не имеет побочных эффектов. Polling читает pending/error очередь D-15 и прекращается при отсутствии исполняющихся работ; ready/cancel сразу исчезают, failed без TTL остаётся до retry/cancel. Готовый результат повторного Complete/GET находится по items.upload_id без истории. Черновик формы отделён от cache, Cancel ничего не меняет на сервере.

Для SHA частей подходят ограниченные Blob slices в worker. Полный file fingerprint требует incremental hashing: `SubtleCrypto.digest` не поддерживает потоковую подачу, а `file.arrayBuffer()` для 20 GiB недопустим. Если после повторного выбора нет доказанной контентной идентичности, старая session не продолжается. Конкретный incremental hasher выбирается по лицензии, bounded-memory и browser fixture gate. [Web Crypto digest](https://developer.mozilla.org/en-US/docs/Web/API/SubtleCrypto/digest).

## Версии и сопровождение

| Компонент | Срез на 11.11.2026 и политика |
| --- | --- |
| Go | 1.27.4, patch от 10.11.2026; поддерживаются две последние major-линии, отдельной LTS нет. Обновлять toolchain до выхода ветки из поддержки |
| .NET как альтернатива | .NET 10 LTS до 14.11.2028. У уже вышедшего .NET 11 тот же EOL; преимущество 10 — зрелость линии, а не более дальняя дата относительно 11 |
| React/Vite | React 19.3.1; Vite 8.3 получает текущие исправления. React не приписывается календарь LTS Angular |
| Node builder | Node 26 Active LTS; Node 24 уже Maintenance LTS. В runtime Go Node отсутствует |
| UI/tooling | Mantine 9.8.0 и React Router 8.5.1 — проверенный срез, Query v5; точные версии TypeScript/plugins/generators/SDK фиксируются совместным integration gate |

Источники версий: [Go release policy](https://go.dev/doc/devel/release), [.NET support](https://dotnet.microsoft.com/en-us/platform/support/policy/dotnet-core), [React release](https://github.com/react/react/releases/tag/v19.3.1), [Vite policy](https://vite.dev/releases), [Vite engine](https://github.com/vitejs/vite/blob/v8.3.0/packages/vite/package.json), [Node releases](https://nodejs.org/en/about/previous-releases), [Mantine releases](https://github.com/mantinedev/mantine/releases), [Router releases](https://github.com/remix-run/react-router/releases).

Новая версия не означает плавающий latest: go.mod/go.sum, npm lockfile, tool versions и OCI digests фиксируются в release. Patch/security updates проходят те же проверки. У Go меньше framework machinery, но чаще требуется переход на следующую поддерживаемую ветку; это реальная цена выбора относительно .NET LTS.

## Перенос семантики между языками

Внутри Go duration хранится nullable `int64` ticks по 100 ns, не float seconds и не `time.Duration`: последний измеряет int64 наносекунд и имеет другой диапазон. API сохраняет согласованный `durationSeconds`; новое публичное ticks-поле не требуется. Numeric item/tag IDs проверяются на JavaScript safe integer, opaque UUID остаются строками. Timestamp/NULL/enum mapping и исторические некорректные durations проверяет внешний importer, без округления raw integer.

В Node-альтернативе это потребовало бы bigint внутри сервера и явного JSON boundary; обычные короткие видео сами по себе не превышают safe integer, но контракт сохранения SQLite int64 шире обычных файлов. В Go тоже нельзя пропускать exact значения через `map[string]any`/float64 или неограниченное умножение ticks на 100.

Parser/binder и AST не переводятся механической заменой Dynamic LINQ. Сохраняются BINARY/case-sensitive имена, дубликаты, разные правила rename/create и unresolved terms. Тестируются Unicode-категории, длины строк и сравнения: Go bytes/runes и .NET UTF-16 не эквивалентны. Миграция не нормализует имена, не сливает теги и не перекодирует старые медиа ради смены языка.

Внешний legacy tool может остаться C#, если это снижает риск чтения прежнего TimeSpan/схемы. Он исполняется отдельно и не добавляет .NET в app image. Goose управляет только новой схемой приложения; backup/restore и перенос старых данных не превращаются в его CLI/API. [Миграция](04-migration-and-operations.md).

## Организация разработки агентами

Будущий репозиторий закрепляет один contract source, один SQL history и один набор правил в AGENTS.md. Архитектурные ограничения одинаковы для всех агентов:

- Модули Catalog/Organization/Search/Ingestion/Media/Operations владеют своим поведением; транспорт, SQL, S3 и FFmpeg adapters не содержат независимой копии бизнес-правил.
- OpenAPI и migration/query changes отдельно видны в review. Go/TS/SQL generated files не правятся вручную; CI повторяет генерацию и требует чистый diff. Один change-set согласует изменения общего контракта до параллельной реализации.
- Типизированы job/session/asset states и versioned payload/AST. Недопустимые переходы и affected-row checks проверяются тестами; один enum не обеспечивает lifecycle.
- Один S3 streaming adapter, один FFmpeg process wrapper, несколько выделенных claim/fenced-commit операций. Агент не дублирует эти протоколы в новых endpoints.
- Общая команда проверки запускает форматирование/static checks, Go/TS compilation, tests и generation-diff; генераторы работают офлайн из закреплённой среды после подготовки зависимостей. Быстрый локальный набор отделён от более долгих S3/FFmpeg/browser/crash gates.
- Сохранность F-01…F-38 с принятыми изменениями D-01…D-16 и соответствие UI baseline доказываются независимыми fixtures, а не количеством новых строк или endpoint. В tests используется настоящая SQLite с production driver/FK/WAL/FULL; сложные S3-границы проверяются реальным backend и fault injection.
- Новая библиотека либо слой получает конкретное обоснование и проверку совместимости; несколько агентов не выбирают независимо разные forms/state/SQL frameworks. Механизмы backup/import остаются внешними; UX сверх одобренного v27 не попадает в core как удобное «улучшение».

Это способ ограничить вариативность реализации, а не обещание качества конкретной модели AI. Документация и тесты остаются основой контроля независимо от выбранного языка.

## Проверка решения перед основной реализацией

Первый вертикальный этап проверяет выбранный стек: JSON command → durable session → raw UploadPart с SHA → restart/resume → Complete и полный GET/hash → FFmpeg → fenced ready commit → Range playback/cancel. Добавляются exact ticks/null fixture, tag rename/search fixture и один React editor/viewer flow. Это план будущей реализации, не уже выполненный spike.

Фиксируются время build/check, диагностируемость ошибок и число корректирующих итераций на одной задаче, bounded RAM, HTTP latency во время DB commit и FFmpeg, остановка процесса и browser media behavior. Измерения одного pilot не обобщаются на все модели/задачи. Полное второе приложение на .NET не требуется; сравнимый небольшой альтернативный pilot имеет смысл только при существенном провале выбранного пути.

Смена языка не меняет [S3 ADR](08-s3-storage-decision.md), публикацию PUT/GET/hash/SQLite, двухконтейнерную поставку, отсутствие архива оригиналов и внешние инструменты. Go не ускоряет FFmpeg автоматически и не открывает macOS GPU из Linux VM. Производственный допуск определяется [приёмкой](07-delivery-and-acceptance.md), а не результатом выбора стека.
