# Browser maximum-file memory and responsiveness gate

The exact 20GiB +256MiB gate passed in Chromium and WebKit against the frozen
production SPA/app and owned S3 pair below on 2026-11-15. Both engines performed
their own full worker fingerprints and real native XHR transfers, published the
padded valid video, retained the unsupported companion and explicitly cancelled
it. This is local Linux browser evidence; shipping Safari acceptance remains
separate.

## Run and isolation

Set `TAG_FILES_BROWSER_APP_IMAGE` and `TAG_FILES_BROWSER_STORAGE_IMAGE` to the
accepted images, then run `scripts/check-browser-large`. Each image is resolved
to its immutable ID before use; the script never builds, pulls or retags images.
Default sizes are exactly 21,474,836,480 and 268,435,456 bytes. Engines run in
order, each in its own Playwright process, against one disposable app/S3 pair.
`TAG_FILES_BROWSER_ENGINES=chromium` or `webkit` selects one engine.

For a harness smoke only, override both `TAG_FILES_BROWSER_LARGE_BYTES` and
`TAG_FILES_BROWSER_SECOND_BYTES` (the documented smoke used 67,108,864 each).
The generated image IDs, byte lengths and transfer counts remain in evidence;
overrides must never be presented as maximum-file acceptance.

Generated inputs, secrets and Compose metadata live in an ignored
`.large-upload.browser.*` temporary project directory.
S3, app metadata and scratch use new private named volumes. The maximum run
requires at least 100GiB observed free space in the disposable S3 filesystem
before upload, accounting for pending inputs/candidates/reservations. On normal
completion or a handled failure the script removes its containers, volumes and
temporary files. No large fixture,
media archive, trace, HAR or video is created in `/tmp`. Existing libraries,
image tags and deployment volumes are not modified. Metadata-only results live
under ignored `.large-upload.browser-results/{chromium,webkit}` outside the
ordinary Playwright output directory, so its cleanup cannot remove them. The
ordinary config excludes `e2e/large`; fresh discovery lists 16 ordinary tests and two
tests in the independent opt-in large config.

## What the test proves

- The large source is a real host-backed sparse OS file. A tiny valid faststart
  H264 MP4 is followed by an ISO-BMFF `free` atom with a 64-bit extended size and
  sparse zero padding to the exact requested length. This is valid-container
  maximum-byte coverage, **not 20GiB useful video or a long encode**. A second
  sparse file contains an unsupported header and zero padding.
- An independent Python SHA256 pass streams each OS file in 1MiB chunks. The
  actual browser `input[type=file]` receives local paths, never a Playwright
  Buffer or `readFile` payload. The browser's full incremental worker fingerprint
  must match the independent SHA before the real API creates the sessions.
- Server-advertised 32MiB parts and two transfers are asserted. A bounded same-
  origin loopback streaming proxy copies 64KiB at a time and paces only the first
  actual PUT at 8MiB/s. It never reconstructs Blob payloads, uses `route.fetch`,
  logs payloads, or serializes files. Every part's actual Content-Length, byte
  count, generation, digest and successful receipt must agree. Two successful
  real forwarding intervals must overlap; maximum concurrent PUTs must be two.
  Native XHR progress must show an intermediate percentage before completion.
- The valid source must pass real upload/Complete/S3 verification/scratch/media
  processing and become a ready Video with a readable content HEAD. The other
  source must retain a failed session with all confirmed parts, then disappear
  after an explicit UI Cancel. The final visible queue must be empty.
- While hashing and while upload/processing is pending, repeated real controls
  collapse/expand the sidebar, navigate to Library, type into the native search
  input, assert focus, Tab away, clear and navigate back to Uploads. Each entire
  roundtrip must finish within 2s, measured with monotonic clocks. Initial SPA
  hydration and one prewarming control/navigation roundtrip are labelled startup
  separately before baseline memory is sampled.
- Every 200ms, Linux `/proc` RSS is sampled for all descendants of the Playwright
  worker (its browser process tree), excluding the Node worker itself. App RSS
  uses its separate Docker host PID. Node worker RSS is recorded separately.
  Tree RSS sums resident pages and can double-count shared pages; it is not PSS,
  JS heap usage, total VM memory, page-cache accounting or a shipping-browser
  memory measurement. The bounded regression ceiling is 512MiB growth over the
  warmed browser-tree baseline; Node growth must stay within 256MiB.
- A 50ms browser interval records delay, monotonic timestamp and phase for up to
  36,000 ticks. Raw samples, per-phase p95/p99/max and the largest spikes are
  saved. There is **no invented event-loop product pass/fail threshold**; the
  actual controls/focus/navigation probes and memory bounds supply acceptance.

The 512MiB browser-growth and 2s control ceilings are explicit bounds for this
probe, not unmeasured claims about every browser/device. The reference 07 Mac,
Safari/VoiceOver, first useful rendered results and sleep/power-loss gates remain
separate.

## Small-file smoke evidence — 2026-11-15

Both files were 64MiB; each engine transferred 134,217,728 actual bytes in four
successfully acknowledged 32MiB parts, with two overlapping PUTs. The padded
source held 17,201 bytes of useful MP4 container/video before its sparse `free`
atom. Its independent fingerprint was
`d6fd6b45c7343640506eefd747331c496459c91a4af57b92aaad5980ec4e78a6`.
Both engines reached ready for that video and retained/cancelled the unsupported
companion using production code and native XHR bodies.

- App/SPA: `sha256:71b2fd0d264dcdbfb74bff58619598e0c36e184c9986a3d4b92b8dccf571905a`.
- S3: `sha256:22fb8cc103c992f86d84449312e92cecf5340f83f59e00d529e7c726458e4e1a`.
- Linux arm64 VM, 8 CPUs, Docker 29.9.3; Playwright 1.65.1.
- Final smoke log: `/tmp/tag-files-browser-large-smoke-final.log`.

| Measurement | Chromium 159.0.8154.6 | WebKit 27.2/WPE |
| --- | ---: | ---: |
| Warm browser-tree RSS | 467,951,616B | 657,956,864B |
| Observed browser-tree peak RSS | 551,788,544B | 868,335,616B |
| Browser-tree growth | 83,836,928B | 210,378,752B |
| Separate observed app peak RSS | 35,827,712B | 35,069,952B |
| Node worker RSS growth | 39,350,272B | 40,108,032B |
| Delay overall p95 / p99 / max | 14.3 /16.0 /17.1ms | 26 /399 /400ms |
| Hash delay p95 / p99 / max | 15.3 /17.1 /17.1ms | 26 /73 /73ms |
| Upload/processing delay p95 / p99 / max | 14.2 /16.0 /16.8ms | 25 /400 /400ms |
| Worst complete control roundtrip | 415.2ms | 498.3ms |
| Timed control roundtrips | 7 | 5 |
| Browser memory samples | 45 | 39 |

The WebKit spike is real measured upload-phase jitter: the worst final smoke
sample was 400ms at monotonic document time 6787ms, separate from startup. An
earlier smoke measured 415ms and failed a provisional 250ms threshold that has no
07 specification basis. That result remains recorded in
`/tmp/tag-files-browser-large-smoke.log`; no 250ms or replacement 500ms product
budget is claimed. The earlier harness also used wall-clock control durations;
the final version uses monotonic durations after observing a wall-clock step.

The harness writes per-engine artifacts under ignored
`.large-upload.browser-results/{chromium,webkit}/`: `memory-evidence.json`,
`process-evidence.json`, and `event-loop-samples.json`. They contain metadata,
process measurements and timer samples only. Long-running API outcome polling
explicitly disposes each JSON response so the test client does not retain
hundreds of part inventories in its request context. Sequential engines have separate
output directories. Raw samples preserve phase and timestamp for spike review;
the completed maximum run's actual retained files and Chromium telemetry gap
are specified below.

The final strengthened smoke (`/tmp/tag-files-browser-large-smoke-probes.log`)
also asserted native Tab focus on Photos and intermediate XHR progress in both
engines: Chromium percentages8/21/36/50 and WebKit12/31/47/67 were observed before
completion. Its browser RSS growth was86,663,168B /208,510,976B; worst control
roundtrip405.4ms /481.0ms; delay p95/p99/max12.5/13.7/13.7ms /
20/376/406ms. The maximum spike remained in upload, at document time6795ms for
WebKit. Both4-part real transfers, ready publication and retained-error Cancel
passed again. This later smoke preserves the same scope and does not replace
the earlier400/415ms observations.

## Completed maximum-file evidence — 2026-11-15

Both sequential engines passed, and `scripts/check-browser-large` exited 0. The
exact frozen pair was:

- App/SPA: `sha256:ed12964c6f4f10a27e1bf289571511221a33619d2ef4675287d596f9a22d8df7`.
- Storage: `sha256:c7061aaa1d9fb08d7143346c6ba0997dd1f7ee7defa8bd821a258410ac8fe1e5`.
- Linux arm64 VM, 8 CPUs, Docker 29.9.3, Playwright 1.65.1. Actual application
  container FFmpeg: `8.0.1-3ubuntu2`. Other local native/QEMU gates ran in parallel;
  these timings do not represent an isolated throughput benchmark.

Each engine received the same exact 21,474,836,480-byte sparse OS file, containing
17,201 bytes of useful tiny MP4 before the extended-size `free` atom, and a
268,435,456-byte unsupported sparse companion. The host source files allocated
only 20,480 and 4,096 bytes respectively; this does not reduce the actual bytes
read, hashed and forwarded. Independent Python SHA256 values were:

- Video: `f4bc588b0cef8f450c044b18d4392fff2ef04de62ef477dd7c9aa979dcef8a9b`.
- Unsupported: `39480f19bae1c1c07e06a3306fde88c024b167e906db71636f733d58219c267c`.

Both browser fingerprints matched. Each engine sent **648 successful native
32MiB PUTs /21,743,271,936 actual bytes**, with maximum active transfers two and
two successful forwarding intervals overlapping. Intermediate native XHR
progress appeared throughout. Both valid sources passed real Complete/full
source verification/scratch/software FFmpeg processing and became ready Video
items with readable canonical content HEADs. Each unsupported source retained
all eight confirmed parts in a failed session until explicit UI Cancel, after
which the visible queue and server upload-batch list were empty.

| Measurement | Chromium 159.0.8154.6 | WebKit 27.2/WPE |
| --- | ---: | ---: |
| Both full fingerprints accepted | 349.44s | 121.02s |
| Entire browser scenario | 818.30s | 592.63s |
| Warm browser-tree RSS | 467,906,560B | 685,600,768B |
| Observed browser-tree peak RSS | 568,168,448B | 1,094,242,304B |
| Browser-tree growth | 100,261,888B (95.6MiB) | 408,641,536B (389.7MiB) |
| Separate observed app peak RSS | 50,753,536B | 49,946,624B |
| Node worker RSS growth | 123,752,448B | 196,292,608B |
| Raw final delay p95 / p99 / max | 4.7 /12.6 /55.7ms | 409 /448 /846ms |
| Hash delay p95 / p99 / max | 4.5 /10.7 /18.9ms | 3 /4 /6ms |
| Upload/processing delay p95 / p99 / max | 4.8 /13.5 /55.7ms | 420 /464 /846ms |
| Startup delay max | 10.4ms | 45ms |
| Worst complete control roundtrip | 467.77ms | 1098.26ms |
| Timed control roundtrips | 682 | 458 |
| Browser memory samples | 4060 | 2942 |

The final raw Chromium maximum was 55.7ms at monotonic document time 818,176.4ms
in upload/processing. Its earlier `memory-evidence` snapshot showed 18.9ms before
the final sample. WebKit's maximum was **846ms** at document time 312,360ms in
upload, with additional 729/723ms spikes. These were after startup, which is
labelled separately. No 250ms or 500ms event-loop budget is claimed. Every actual
sidebar/native-search/Tab-focus/Clear/navigation roundtrip stayed below the
probe's 2s ceiling, and browser/Node RSS growth stayed below their respective
512/256MiB bounds. WebKit's observed timer jitter remains part of the result.

The full controller log is preserved in
`.large-upload.browser-results/controller.log` (original
`/tmp/tag-files-browser-large-final.log`). Exact memory evidence and final
timestamped process/timer summaries parsed from that stdout are preserved as
`memory-evidence-from-controller.json` and `process-summary-from-controller.json`
for each engine. WebKit also retains its original complete
`event-loop-samples.json`, `memory-evidence.json` and `process-evidence.json` in
its test subdirectory. The ordinary browser suite cleaned the former shared
`web/test-results` root during this run and removed Chromium's complete per-tick
JSON file; its exact aggregate/phase/worst-timestamp summaries and all control
durations remain in the controller log. Missing ticks were not reconstructed.
Future large output is isolated outside that root; the successful maximum run
was not repeated solely to replace telemetry.

After success, the owned Compose project
`tag-files-browser-large-36657e9ad056`, all three named volumes and both networks
were removed; post-run label queries returned no resources. The reusable sparse
source cache was also removed after preserving its metadata and hashes in
`source-manifest.json`. No large source/archive remains from this gate.

This evidence identifies the image pair, not a claimed clean commit. The later
catalog MetadataEditor pending-field correction is outside this image pair's
scope and has a separate actual-container browser check; its patch does not
change the upload coordinator, hashing, XHR or Go runtime. This gate does not
claim 20GiB of useful video, a long video encode, shipping macOS Safari/Chrome,
VoiceOver, browser first-useful-results timing, full-library performance or
power-loss durability.

## Earlier interrupted maximum attempt

Intentionally stopped before acceptance. The first maximum attempt used app
`sha256:d0a09d308c21b726653b4a88f20e691f8b978f07ba1c7a98089211a46d790bb2`
and storage
`sha256:acc5e3fd27e3f20e237d7d039bdb89a3c1d20856ac986b05b239c28cc5b3d376`.
Independent review found a gateway retry-proof defect after launch, so the parent
requested cancellation before the browser finished its full fingerprint. The
last Chromium log showed 10% of the 20GiB fingerprint at 31.306s. No accepted Create
or PUT stage was reached. Owned Playwright processes received SIGINT; the harness
exited 1 for the interrupted child, then removed its exact disposable project,
all three volumes and networks. No unrelated Docker resources were stopped.
Log: `/tmp/tag-files-browser-large-maximum.log`.

The sparse inputs and completed independent Python hashes were safely retained
in the ignored `.large-upload.browser-cache.1887358` project directory and reused
for the completed corrected-pair run above, then removed after success.
`TAG_FILES_BROWSER_REUSE_MANIFEST` accepts an owned cache manifest when one is
available. Cache reuse validates exact lengths and file inode/device/mtime
identity; the browser still computes the entire fingerprint itself.
`TAG_FILES_BROWSER_VALIDATE_ONLY=1` validates cached metadata and starts no
services/browser/upload. This metadata-only path has passed. The controller now
also interrupts its owned Playwright process group before Docker cleanup when
cancelled, without signalling other projects.

Neither the small smoke, this cancelled attempt, nor the earlier native Go
20GiB upload was counted as completed browser maximum-file evidence.
