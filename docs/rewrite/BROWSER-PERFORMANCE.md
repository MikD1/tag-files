# Local browser first-use rendering probe

The opt-in `scripts/check-browser-performance` measures the first useful gallery
render through the normal production app image, its embedded production React
SPA, SQLite and owned native storage image. It supplements the SQL/API/encode
probe in [PERFORMANCE.md](PERFORMANCE.md). This bounded Linux fixture does not
establish the reference Mac/100GB performance acceptance in
[07](07-delivery-and-acceptance.md).

## Reproduce

Use already-built local images; the script does not build, pull or publish:

```sh
TAG_FILES_BROWSER_APP_IMAGE=sha256:ef3cc12a4a58ecd6e36a6689210a6e33f3879d2571507ec1b82d4956f09cf3d9 \
TAG_FILES_BROWSER_STORAGE_IMAGE=sha256:70aa8659fd1622ffce9c39a65024c83b3619c248eb8a71c55ccc6af6f0e17ea5 \
scripts/check-browser-performance
```

Chromium and WebKit run sequentially, with one worker and no retries. The default
is 20 timed samples per scenario and phase; `TAG_FILES_BROWSER_PERFORMANCE_SAMPLES`
accepts 5–100. `TAG_FILES_BROWSER_ENGINES=chromium` selects one engine. An optional
`TAG_FILES_BROWSER_PERFORMANCE_START_FILE` delays browser timing until that file
exists, so unrelated CPU-heavy preparation can finish first.

Every invocation creates a unique Compose project, volumes, random private
credentials and loopback ports. A separate preparation network permits the
offline writer's authenticated loopback S3 access. It is detached after Compose
has finished startup; a runtime assertion requires storage to be running on the
internal library network alone before timing. The app serves every measured API
and media request at the same browser origin. There are no mock media responses,
Playwright route substitutions or Vite development server.

The script removes only its own containers, networks, volumes, credentials and
offline database. It retains fixture/image manifests, runtime-network evidence,
storage allocation, screenshots, per-sample JSON and any failed-test traces under
an ignored project `.performance.browser-*` directory. This output is outside
`web/test-results`, so ordinary browser checks cannot delete it.

## Fixture and measurement

The separate test-only Go module uses the production SQLite driver and
`store.Open`/`Write`: WAL, FULL, foreign keys and an immediate writer transaction.
Before the app starts, it seeds 10,000 ready image items, 2,000 distinct tags,
100,000 item-tag links, 350 collections and 20,000 verified asset rows. Every item
has its own primary and poster UUID/key. Each key contains a real copy of the
same valid 640×400 PNG primary: 5,140 bytes with SHA256
`078e3547f8f919802742dbebe9d2d0e7c618f4eff36bc63791e503eada09b391`.
Each poster is an actual 640×400 JPEG encoded at quality 80, 7,111 bytes with SHA256
`532f4209bc74484c7104494048b0e2bc95c9834abf15d496c972d7bafc305d93`.
Canonical primary/previews keys contain the actual initialized library UUID
and asset UUID; the fixture checks them with `domain.OwnedObjectKey`.
The total actual S3 object payload is 122,510,000 bytes (about 116.83 MiB), not 100GB.
Preparation performs a full GET/size/SHA256 check for every object and an
independent paginated inventory of all 20,000 keys; S3 work finishes before the
catalog SQL transaction. After app startup, every expected first-page ID also
receives complete same-origin primary/poster GET/hash verification before timing.
Native filesystem allocation includes storage metadata and is recorded separately
from payload size.

The fixture builds independent first-page membership/order oracles from its
deterministic tag assignment, then checks actual API responses against those
oracles before timing:

| Scenario | Query | Matches |
| --- | --- | ---: |
| Library | empty | 10,000 |
| Simple search | `tag0001 \|\| tag0002` | 100 |
| Compound search | `(tag0001 \|\| tag0002) && !tag0138` | 55 |

Every scenario has 24 expected cards and a different ordered first-page ID set.
The SPA search phase also asserts that the previous DOM does not already match
the next expected ID set, preventing a stale prior render from satisfying the
observer.

Each browser initially records one fresh-context navigation separately. This is
not a cold OS, Docker, S3 or API measurement: fixture PUT/verification and API
preflight already warmed server caches. The browser then warms all three pages
and omits those warmup observations from distributions. It measures:

- Full navigation from browser `performance.timeOrigin` through the actual HTML,
  JavaScript, API and media path, with the same browser context/cache retained.
- Search submission in the warmed SPA, with its normal Query cache. A browser
  `performance.now()` timestamp precedes Playwright's Enter dispatch, so this is
  a conservative action-to-observation interval including dispatch overhead.

The observer requires the entire matching 24-card ID sequence in the DOM and
one intersecting visible thumbnail with nonzero natural dimensions, successful
`img.decode()` and a following animation frame. It does not wait for every image
or for network idle. Raw JSON retains UTC sample start/end, browser time origin,
start/cards/decode/useful observation timestamps, expected/actual IDs, decoded
item/media URL, p50/p95/max, browser version and all observed page/network errors.
Cards/decode timestamps are observation points, not an assertion of the first
possible paint. The 2s initial budget is asserted for every warm sample in this
bounded local probe; the fresh-context record is reported separately.

## Executed evidence

The focused run passed on 2026-11-15, 11:34:42–11:35:03 UTC, after unrelated
image/QEMU builds had finished. It used the exact Linux arm64 image IDs in the
reproduction command above (built from `60f35ca2e3c9ac002e75be3d5f0e27ddfd4db70e`).
The test checkout was `8ce4ce34773eca996893ae0e645b47ddabb6f3f6` with this
test-only harness still uncommitted. It used Playwright 1.65.1 on a Linux arm64 Docker daemon. Production app, Go runtime,
SPA sources, dependencies, images and deployment files were not changed by this
probe. Browser versions were Chromium 159.0.8154.6 and Playwright WebKit 27.2;
the recorded viewport was 1280×720 CSS pixels (the project device preset takes
precedence over the global configuration's viewport).

Each browser completed 120 warm samples (20 for each scenario/phase) plus its
separate fresh-context observation. All 240 warm samples met the 2s bound, with
no observed page errors, failed media/API responses or unexpected network errors.
The independent membership/order checks, every stale-DOM transition assertion,
all 20,000 full object hashes and 116 same-origin primary/poster preflight hashes
passed. Chromium's test took 8.9s (9.2s runner); WebKit's took 11.5s (11.9s runner).

Timings below are milliseconds, nearest-rank p50/p95/max, rounded to 0.1ms:

| Phase | Scenario | Chromium p50 / p95 / max | WebKit p50 / p95 / max |
| --- | --- | --- | --- |
| Warm navigation | Library | 62.8 / 79.2 / 79.8 | 83 / 98 / 109 |
| Warm navigation | Simple search | 78.6 / 79.8 / 80.3 | 85 / 91 / 99 |
| Warm navigation | Compound search | 78.4 / 79.4 / 80 | 85 / 101 / 102 |
| Warm SPA search | Library | 53 / 54.7 / 62.2 | 41 / 51 / 71 |
| Warm SPA search | Simple search | 52.5 / 54.1 / 54.3 | 29 / 58 / 83 |
| Warm SPA search | Compound search | 53.1 / 54.2 / 55.9 | 32 / 54 / 55 |

The separate fresh-context library observation was 133.4ms in Chromium and
176ms in WebKit. Server and media caches were already warm, as described above.
Fixture preparation took 29.676s. Native storage `/data` allocation immediately
after preparation was 163,800KiB (`du -sk`), including native metadata; this is an
observation of this disposable volume, separate from 122,510,000 object bytes.

Complete local evidence remains in
`/home/mik.guest/projects/tag-files/.performance.browser-ose4zd_0`:

- `fixture.json`, `images.json`, `storage-runtime.json`, `storage-allocation.txt`,
  `media-preflight.json` and `cleanup.json`.
- `browser/chromium/chromium-samples.json` SHA256
  `1c6b5253449854a42e1809fdd2c51373a36ea501a9cab9b00f3e8c077c67c1ed`.
- `browser/webkit/webkit-samples.json` SHA256
  `dbdd608841e0f0aa03a92502c794f3b04114eaeb8eccc430640c82370b14e7d0`.
- Both actual gallery screenshots, with the matching compound query and visible
  decoded synthetic media. The owned project, containers, networks, volumes,
  credentials and offline database were removed; no global prune was used.

Focused static checks also passed: Python byte compilation, Go fixture compile
and vet, web TypeScript and targeted ESLint. Ordinary Playwright discovery still
lists exactly 16 cases and excludes this opt-in directory.

Earlier preparation failures were harness failures, not accepted measurements:
internal-only Docker networking suppressed the offline loopback S3 port; Compose
reconciliation recreated a manually detached dependency; an async Playwright
predicate returned its false Promise resolution without polling; and noncanonical
test object keys were correctly refused by the production media ownership gate.
Those attempts retained their diagnostics and removed their owned resources.
Only the corrected complete run above supplies timing evidence.

This exercises identical small synthetic images on an otherwise idle local
Linux service. Real Mac Safari/Chrome, shipping-browser codecs, accessibility,
the specified VM limits, 100GB primary objects, realistic image entropy/size
distributions and true cold-start measurements remain separate acceptance gates.
