# Выбор S3-хранилища: ADR S3-01

Дата исследования: 11 ноября 2026 года; согласованность с UI baseline обновлена 14 ноября. Статус: **выбран для целевой архитектуры — SeaweedFS 4.53, `weed mini`, embedded LevelDB2, сохранённый глобальный fsync rule**. Это техническое решение исследования в рамках поручения выбрать стек; продуктовые правила D-01…D-16 сохраняются. Оно заменяет первоначальную рекомендацию прямого хранения файлов приложением. По D-16 весь текущий UI и поведение v27 зафиксированы в [baseline](prototype/accepted-baseline.json); этот ADR не добавляет storage-экраны или новые UI-варианты. Одновременная поддержка нескольких движков и автоматический fallback в v1 не нужны.

Рекомендация прикладного стека Go + React рассматривается отдельно в [ADR прикладного стека](09-application-stack-decision.md). Она меняет S3-клиент приложения на AWS SDK for Go v2, сохраняя выбранный storage-профиль и требования сохранности.

## Контекст и критерии

Один пользователь, браузер на том же современном Mac с Docker Desktop; несколько тысяч элементов и около 100 GB медиа, далее сотни GB. Библиотекой управляет только Tag Files. Дополнительные оригиналы не сохраняются: после автоматической обработки остаётся одна проверенная рабочая копия и preview. Кластер, Finder-доступ и публичный bucket не нужны.

Критерии в порядке влияния на выбор:

1. Сохранность единственной рабочей копии, понятная синхронизация данных/metadata до подтверждения PUT и восстановление upload после restart.
2. ARM64-образ, один storage-контейнер, работа без внешнего аккаунта и отдельной metadata DB.
3. Нужное подмножество S3: streaming, Range, multipart/resume, checksums, metadata и проверенная совместимость с AWS SDK for Go v2.
4. Внешние backup/restore и логический перенос из MinIO без чтения его внутреннего layout.
5. Проверяемые релизы, лицензия и управляемая эксплуатационная сложность.

Сотни GB сами по себе не требуют распределённой установки. Число объектов, размер крупнейшего видео, I/O и свободное место важнее абстрактного объёма. Замеры памяти/скорости на Mac не выполнялись. S3 выбирается независимо от прикладной БД: SQLite, jobs и worker остаются в модульном монолите, PostgreSQL/Redis не добавляются.

## Сравнение вариантов

Версии — проверенный срез исследования, не инструкция устанавливать будущий `latest`. ARM64/amd64 manifests прочитаны для SeaweedFS, Garage, RustFS и Versity; контейнеры не запускались.

| Кандидат | Локальная топология и лицензия | Существенные свойства и ограничения | Решение |
| --- | --- | --- | --- |
| **SeaweedFS 4.53**, 08.11.2026 | `weed mini`: master/volume/filer/S3 в одном процессе; embedded LevelDB2; Apache-2.0 | Сам размещает chunks и ведёт namespace. Глобальный fsync надо включить явно. Нужен согласованный backup всех metadata и volumes | **Основной выбор:** готовый объектный движок без кластера и внешней DB |
| **Garage v2.5.0**, 24.10.2026 | Один узел допустим; SQLite/LMDB metadata; AGPL-3.0 | Ориентирован на распределённое хранение; fsync-флаги по умолчанию выключены. Даже `metadata_fsync=true` использует SQLite NORMAL | Не выбран для единственной копии на одном узле |
| **RustFS 1.0.4**, 25.10.2026 | Single-node/single-disk без внешней DB; Apache-2.0 | Явный `strict` durability contract. Первый stable — 16.09.2026; короткая stable-история, SNSD не расширяется на месте | Перспективная альтернатива после накопления опыта эксплуатации и restore-проверок |
| **Versity Gateway v1.11.0**, 06.11.2026 | Один S3 процесс над POSIX/xattrs; Apache-2.0 | Самая простая топология, но не chunk store. При анализе кода выявлен риск недоступного MPU после прерывания Complete; дополнительно нужен durable bootstrap bucket | Не выбран в этой версии: восстановление upload важнее минимальной топологии |
| **MinIO AIStor Free** | Бесплатный single-compute уровень по лицензии MinIO | Нужен активный production license file, получаемый у поставщика | Не выбран для установки без внешнего оформления |

Открытый `minio/minio` архивирован 25.04.2026 и не является поддерживаемой базой нового сервиса. Существующий экземпляр остаётся источником для migration script. [Репозиторий MinIO](https://github.com/minio/minio).

### Почему SeaweedFS

Официальная документация допускает `weed mini` для single-node production. Он объединяет master, volume server, filer и S3 в одном процессе; отдельный PostgreSQL не нужен, достаточно embedded LevelDB2. Это несколько внутренних подсистем, но **один storage-контейнер**. В отличие от POSIX gateway физическое размещение chunks принадлежит хранилищу, а приложение задаёт только object keys. [Mini](https://github.com/seaweedfs/seaweedfs/wiki/Quick-Start-with-weed-mini), [Filer Stores](https://github.com/seaweedfs/seaweedfs/wiki/Filer-Stores), [код mini 4.53](https://github.com/seaweedfs/seaweedfs/blob/4.53/weed/command/mini.go#L348).

Версия 4.53 выпущена 08.11.2026. Официальный образ содержит linux/arm64 и linux/amd64; лицензия Apache-2.0. Базовый production S3 в open-source редакции не имеет лимита «25 TB»: это число относится к отдельным Enterprise dev/test условиям. Enterprise-функции не включаются в проект. [Релиз](https://github.com/seaweedfs/seaweedfs/releases/tag/4.53), [release workflow](https://github.com/seaweedfs/seaweedfs/blob/4.53/.github/workflows/container_release_unified.yml#L144), [лицензия](https://github.com/seaweedfs/seaweedfs/blob/4.53/LICENSE), [редакции](https://www.seaweedfs.com/docs/comparison/), [условия Enterprise](https://www.seaweedfs.com/docs/deploy/).

Нужные PUT/GET/HEAD/Range/Delete/ListV2 и multipart API поддерживаются. Для AWSSDK.S3 v4 есть реальный разбор и исправление checksum-сценария; это историческое свидетельство для .NET-клиента, а не сертификат совместимости AWS SDK for Go v2 или любой будущей версии SDK. Multipart SHA256 COMPOSITE не равен SHA256 всего файла; HEAD checksum требует соответствующего режима запроса. [S3 API](https://github.com/seaweedfs/seaweedfs/wiki/Amazon-S3-API), [.NET/checksum issue и исправление](https://github.com/seaweedfs/seaweedfs/issues/11401).

Обязательна сохранённая глобальная настройка до первой загрузки:

```text
fs.configure -locationPrefix=/ -fsync=true -apply
```

Глобальный `/` охватывает и multipart/служебные paths. В 4.53 проверена цепочка: storage rule → upload fsync → синхронизация volume data и index → sync-write filer metadata в LevelDB2. Параметры `volume.fsyncMinBatchBytes`, `volume.fsyncMinBatchRequests`, `volume.fsyncBatchWait` лишь настраивают batching и сами **не включают fsync**. Важные исправления передачи S3 fsync и наследования rule вошли до 4.53; поэтому фиксируется образ и проверяется фактический путь, включая MPU. [Правило](https://github.com/seaweedfs/seaweedfs/wiki/Path-Specific-Configuration), [upload propagation](https://github.com/seaweedfs/seaweedfs/blob/4.53/weed/operation/upload_chunked.go#L330), [data/index](https://github.com/seaweedfs/seaweedfs/blob/4.53/weed/storage/volume_write.go#L628), [filer sync](https://github.com/seaweedfs/seaweedfs/blob/4.53/weed/filer/filer.go#L474), [LevelDB2 sync](https://github.com/seaweedfs/seaweedfs/blob/4.53/weed/filer/leveldb2/leveldb2_store.go#L136), [S3 fix](https://github.com/seaweedfs/seaweedfs/pull/10906), [inheritance fix](https://github.com/seaweedfs/seaweedfs/pull/11953).

Нельзя переносить вывод о PUT на все операции: delete-path содержит TODO об отсутствии fsync. Поэтому app сохраняет deletion receipts и сверяет удаление после restart. Удалённое место в append-oriented volumes освобождается обслуживанием хранилища; окончательное удаление в продукте не означает secure erase SSD или немедленное уменьшение physical volume. Cold backup должен сохранять и filer metadata, и volumes; один metadata snapshot недостаточен. [Delete implementation](https://github.com/seaweedfs/seaweedfs/blob/4.53/weed/storage/volume_write.go#L462), [vacuum](https://github.com/seaweedfs/seaweedfs/blob/4.53/REST_API.md#vacuum-deleted-space), [backup](https://github.com/seaweedfs/seaweedfs/wiki/Data-Backup).

Цена выбора: больше внутренних компонентов и необходимость проверять compaction/free-space, конфигурацию metadata и согласованный restore. Это предпочтительнее разработки собственного storage engine или обходов найденного MPU-состояния другой реализации. End-to-end power-loss устойчивость Mac здесь ещё не доказана; это обязательная приёмка.

### Почему не Garage, RustFS, Versity и AIStor

**Garage.** Базовое нужное S3 поддерживается, но даже с `metadata_fsync=true` SQLite-адаптер v2.5.0 задаёт `synchronous=NORMAL`. SQLite допускает потерю недавно подтверждённой транзакции при power loss в этом режиме. Для единственного узла с автоматическим удалением input этот компромисс нежелателен. Это вывод для данного сценария, а не общая оценка распределённой надёжности Garage. [S3 compatibility](https://garagehq.deuxfleurs.fr/documentation/reference-manual/s3-compatibility/), [configuration](https://garagehq.deuxfleurs.fr/documentation/reference-manual/configuration/), [SQLite adapter](https://github.com/deuxfleurs-org/garage/blob/v2.5.0/src/db/sqlite_adapter.rs#L72), [SQLite synchronous](https://sqlite.org/pragma.html#pragma_synchronous), [релизы](https://garagehq.deuxfleurs.fr/_releases.html).

**RustFS.** Рассмотрен stable 1.0.4, называть его beta неверно. `strict` обещает синхронизацию payload/metadata/directories до успешного ответа. Однако stable появился менее двух месяцев назад; SNSD документация ориентирует режим на небольшие некритичные workloads, а полноценного runbook восстановления всего состояния в исследовании не найдено. В стандартной открытой release-сборке нужные операции доступны; наличие отдельной build-feature `license` не подменяется утверждением, что такого кода вообще нет. [1.0.4](https://github.com/rustfs/rustfs/releases/tag/1.0.4), [1.0.0](https://github.com/rustfs/rustfs/releases/tag/1.0.0), [durability](https://github.com/rustfs/rustfs/blob/1.0.4/docs/operations/durability-modes.md), [SNSD](https://docs.rustfs.com/en/installation/linux/single-node-single-disk), [matrix](https://github.com/rustfs/rustfs/blob/1.0.4/docs/architecture/s3-compatibility-matrix.md), [features](https://github.com/rustfs/rustfs/blob/1.0.4/rustfs/Cargo.toml).

**Versity.** Привлекателен одним процессом, Apache-2.0 и `posix --fsync`; ARM64-образ проверен. Но в v1.11.0 Complete переименовывает MPU в `.inprogress`; повтор при наличии этого marker возвращает success ещё до готового объекта. ListMultipartUploads скрывает такие каталоги, а startup recovery этого состояния в просмотренном коде не найден. Отсюда риск: SIGKILL во время сборки оставляет части недоступными обычному resume по исходному upload ID. Это **вывод статического анализа, не воспроизведённый runtime-тест и не утверждение о всех версиях**. Для выбора достаточно не брать известную неопределённость как baseline; писать private-filesystem recovery не предлагается. [Claim и early success](https://github.com/versity/versitygw/blob/v1.11.0/backend/posix/posix.go#L2621), [скрытие inprogress](https://github.com/versity/versitygw/blob/v1.11.0/backend/posix/posix.go#L3683), [релиз](https://github.com/versity/versitygw/releases/tag/v1.11.0).

Дополнительно `CreateBucket` не синхронизирует запись bucket в gateway root, тогда как PUT fsync останавливается на bucket. Потребовался бы внешний durable bootstrap; backup default metadata требует file/bucket xattrs. Ключи непосредственно отображаются в POSIX пути, внутреннего hash-sharding нет. После исправления и runtime-проверки MPU проект можно пересмотреть. [CreateBucket](https://github.com/versity/versitygw/blob/v1.11.0/backend/posix/posix.go#L934), [syncNamespace](https://github.com/versity/versitygw/blob/v1.11.0/backend/posix/fsync.go#L53), [metadata](https://github.com/versity/versitygw/wiki/POSIX-metadata), [POSIX backend](https://github.com/versity/versitygw/wiki/POSIX-Backend).

**AIStor Free.** Бесплатный single-compute tier существует, но production установка требует активного license file от поставщика. Поэтому бесплатность не равна установке без внешнего оформления. ARM64 runtime manifest AIStor отдельно не проверен; отсутствие поддержки не утверждается. [Pricing](https://www.min.io/pricing), [installation/license](https://docs.min.io/aistor/installation/container/install/).

## Зафиксированный deployment-профиль

| Параметр | Контракт первого релиза |
| --- | --- |
| Сервисы | `app` и `storage`; SQLite/jobs/FFmpeg внутри app, без Redis/PostgreSQL/отдельного worker |
| Storage image | `chrislusf/seaweedfs:4.53`, index digest `sha256:ccad23e18639c8b98cee17e7100b95830a06077b56d8e5b006d6dcf8ec5e94a0` |
| Топология | `weed mini`, один узел без replication, embedded LevelDB2; не полагаться на defaults для production-конфига |
| Durability | Сохранённое global `fs.configure -locationPrefix=/ -fsync=true -apply` до разрешения uploads; проверка после restart/update/restore |
| Volumes | `storage-data` для всего Seaweed `/data`, `app-data` для SQLite, `scratch` для FFmpeg |
| Metadata paths | Сохранять фактические master/filer/volume paths; официальный filer.toml использует `/data/filerldb2` независимо от `-dir` |
| Доступ | Private bucket `tag-files`, случайные credentials, anonymous requests отвергаются; mini без credentials может работать Allow All |
| Сеть | Только app опубликован на `127.0.0.1`; S3 `http://storage:8333` и остальные порты Seaweed только внутри Compose |
| SDK | AWS SDK for Go v2 (`service/s3`), SigV4, path-style, фиксированная region подписи; точные версии модулей после integration gate в `go.mod`/`go.sum` |
| Backup | Внешний cold script: остановить app и storage, сохранить SQLite, весь metadata/chunks/config state, secrets и version manifest |

Это контракт будущего deployment, не уже испытанный Compose. Digest проверен чтением registry manifest. Развёртывание фиксирует все пути, private access, ненужные endpoints и fsync rule; отдельный storage preflight проверяет настройки, app не получает Docker socket или storage filesystem mount. Стандартный S3 не сообщает физическую ёмкость или правило fsync. Без достоверного показателя операционный отчёт помечает его как неизвестный; отдельной страницы storage/diagnostics в одобренном UI нет. [Docker image/config](https://github.com/seaweedfs/seaweedfs/blob/4.53/docker/README.md), [default filer path](https://github.com/seaweedfs/seaweedfs/blob/4.53/docker/filer.toml), [mini access defaults](https://github.com/seaweedfs/seaweedfs/wiki/Quick-Start-with-weed-mini#default-s3-access).

Go SDK по умолчанию использует seek для вычисления длины и SigV4 payload hash в PUT/UploadPart. Для nonseekable browser body длина задаётся явно через `ContentLength`; при необходимости вариант с `v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware` проверяется на закреплённой паре SDK/SeaweedFS. `UNSIGNED-PAYLOAD` относится к подписи и не заменяет явный `ChecksumSHA256`: gateway обязан вычислить checksum реального тела, отвергнуть неверный digest и подтвердить part только после проверки. Алгоритм согласуется между Create/UploadPart/Complete. Неперематываемый body нельзя слепо повторно читать для SDK retry; новая попытка получает те же байты/digest от browser и сверяется через ListParts. Менеджер MPU может буферизовать nonseekable части; bounded RAM проверяется для фактического proxy-пути. [.NET-сценарий выше](https://github.com/seaweedfs/seaweedfs/issues/11401) этих свойств Go-клиента не подтверждает. [Go SDK unseekable input](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/sdk-utilities-s3.html#unseekable-streaming-input), [Go SDK checksums](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/s3-checksums.html).

## Ключи и границы ответственности

```text
tag-files/
  incoming/<library-id>/<upload-id>/source
  primary/<library-id>/<asset-id>/media.<extension>
  previews/<library-id>/<asset-id>/<profile-version>/poster.jpg
```

Это логические S3 keys. Приложение не реализует распределение файлов по UUID-префиксам и не знает Seaweed chunk layout. UUID обеспечивают идентичность; теги/коллекции не влияют на размещение. Primary/preview неизменяемы, новая попытка публикации получает отдельный candidate asset ID. Смена движка впоследствии выполняется логическим S3-копированием и обновлением configuration, без сохранения физического layout.

Браузер обращается только к app: streaming proxy обслуживает multipart и Range GET без полного файла в RAM и без S3 credentials браузеру. Presigned URLs в локальной схеме не нужны. Resume определяется ListParts и сохранёнными part bindings. Сервер выбирает часть от 5 MiB до 5 GiB, кроме последней, которая может быть меньше; не более 10 000 частей. [S3 limits](https://docs.aws.amazon.com/AmazonS3/latest/userguide/qfacts.html).

До UploadPart сохраняется `(session,partNumber,size,SHA256)`, gateway проверяет checksum тела. Повтор номера допускает только те же байты; новый digest требует новой session. Lease сам по себе не останавливает запрос в S3. Complete закрывает новых writers, ожидает известные операции и сохраняет manifest; результат каждого Complete подтверждается HEAD и полным GET/hash, затем создаётся job. ETag непрозрачен, составной checksum и user metadata SHA не равны доказательству целостности всего файла.

После probe/conversion: durable candidate key+size+SHA → PUT с global fsync → полный GET/hash → fenced SQLite `ready` commit → cleanup incoming/scratch. Исходник не архивируется. По D-15 ready commit одновременно публикует item с UNIQUE upload_id, удаляет session/parts/ingestion job и сохраняет самостоятельный cleanup intent; строка Uploads исчезает немедленно. Ошибка cleanup не отменяет успех primary и не возвращает upload в очередь. Cancel/delete сохраняют receipts для повторного Abort/Delete после поздних ответов; неизвестные объекты не удаляются автоматически. У idle/receiving, failed и активных uploads нет TTL: необходимые inputs/MPU/parts сохраняются до ready либо ручного cancel. Нельзя настраивать age-based lifecycle/AbortIncompleteMultipartUpload для принадлежащих живым sessions входов. Receipt содержит owned keys/MPU/in-flight context без FK на удаляемую session и живёт до завершения Abort/Delete/reconcile; готовый item по upload_id обеспечивает повтор Complete/GET без истории. Это обязанности приложения: у S3 и SQLite нет общей транзакции. Подробности — в [архитектуре](03-target-architecture.md) и [HTTP спецификации](02-product-specification.md).

## Перенос, backup и диск

Одноразовый внешний скрипт читает старую SQLite/MinIO, создаёт новую схему и переносит объекты через S3 PUT/MPU с независимым полным GET/hash каждого файла. Старые готовые MP4/фото копируются без массового перекодирования; внутренние MinIO directories не переносятся в Seaweed. App остановлен, target storage работает. Import/export и backup/restore не входят в код, API, UI или scheduler основного сервиса. [Runbook](04-migration-and-operations.md).

Cold backup останавливает оба сервиса и сохраняет всю пару SQLite + Seaweed metadata/chunks/config/secrets. Нельзя ограничиться filer snapshot или `s3 sync`: первый не содержит payload, второй не сохраняет весь служебный state и БД Tag Files. Restore в чистые volumes выполняется той же проверенной версией и проверяется полным каталогом/hashes/relations. Предположений о восстановимости по наличию архива нет.

Запас диска учитывает старую и новую библиотеку, MPU/incoming, FFmpeg input cache/output, preview, журналы и volume compaction. Named volumes могут находиться на одном VM disk: свободные значения нельзя складывать как независимые диски. Точный пиковый расход и момент освобождения места измеряются pilot; logical size не равен physical usage. Проверяются и Docker disk image limit, и свободное место macOS.

## Обязательные проверки перед production migration

| Проверка | Требуемый результат |
| --- | --- |
| S3 conformance | Закреплённые SeaweedFS 4.53 и модули AWS SDK for Go v2: PUT/GET/HEAD/Delete/ListV2, Range 206/416, metadata, SigV4, ошибочные checksum, MPU Create/Upload/ListParts/Complete/Abort/ListMultipartUploads |
| Go SDK streaming | Настоящий nonseekable body с явным ContentLength; выбранный SigV4 middleware и ChecksumSHA256, проверка checksum по телу и отказ на неверный digest, ограниченная RAM, обрыв/retry без повторного чтения исчерпанного потока |
| Multipart recovery | Повтор части с тем же digest, запрет другого; lost responses, restart во время Complete, несколько частей и one-part small file; HEAD/GET после любого Complete, bounded RAM и checksum для nonseekable body |
| Durability | Global fsync сохранён и охватывает MPU, app SQLite FULL; crash на каждой границе и принудительное выключение VM после подтверждённого PUT/Complete, включая новый bucket |
| Гонки | Cancel/complete и stale worker не создают дубль, не заменяют canonical bytes; cleanup receipts удаляют поздние owned objects, input не удалён до primary+commit |
| Диск/обслуживание | ENOSPC storage/scratch/SQLite, MPU peak, delete/vacuum и compaction headroom, отсутствие ложного успеха |
| Restore | Внешний cold backup → чистые volumes → весь каталог, metadata, hashes, связи и partial upload/job recovery |
| Mac sizing | Реальный Apple Silicon: idle/peak RAM/CPU, upload+playback+FFmpeg, тысячи объектов и крупное видео; фиксируются модель Mac и ресурсы Docker |

Исследование проверило документацию, код и manifests, но не заменяет эти испытания. `kill -9` процесса не доказывает power-loss durability; single-node не защищает от потери диска без внешней копии. Требования включены в [план приёмки](07-delivery-and-acceptance.md).

При провале обязательного gate пересматриваются версия/конфигурация или сам ADR, а не ослабляется проверка и не удаляется input раньше. RustFS можно повторно оценить после проверенного restore и накопления stable-опыта; Versity — после устранения MPU recovery риска. Изменение позиционирования на несколько машин, replication или миллионы объектов требует нового сравнения.
