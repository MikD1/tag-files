# Bounded production performance probe

The opt-in `scripts/check-performance` passed on 2026-11-15 against a normal
production executable and the real owned S3 image. This supplies a Linux
synthetic SQL growth-profile measurement for [07 acceptance budgets](07-delivery-and-acceptance.md#предлагаемые-измеримые-цели),
not a universal benchmark or a release-readiness claim.

## Reproduce

Run `scripts/check-performance` from the repository root. It resolves
`TAG_FILES_PERFORMANCE_STORAGE_IMAGE` (default `tag-files-storage:local-check`)
to an immutable image ID before starting. An existing image is required; the
script neither builds nor retags images. Only
`TestProductionCatalogPerformanceDuringSoftwareEncode` runs, with `-race` in
the Go test/client. The observed app is separately built by normal
`go build -trimpath ./cmd/tag-files`, without the race detector.

The script bounds the test to eight minutes; the test's own context is six
minutes. It creates a private random-credential storage fixture on a disposable
named Linux volume and app metadata/scratch in a disposable project directory.
Containers, volumes, credentials and scratch are removed on completion. Existing
libraries and deployment volumes are never selected. The full storage suite and
20 GiB suite are not run by this command.

## Measured fixture and method

- Linux arm64 VM, kernel `7.0.0-28-generic`, 8 visible CPUs, Docker 29.9.3,
  8,296,857,600 bytes VM memory. App metadata/scratch used project `virtiofs`;
  S3 used a named Linux volume. This is not the reference Mac/4 CPU/4 GiB profile.
- Go 1.27.4 (automatically selected inside `app/`); host FFmpeg 8.0.1-3ubuntu2 with libx264. Production processing kept
  its default software libx264 `veryfast`, CRF 23, two-thread configuration.
- Owned storage image:
  `sha256:22fb8cc103c992f86d84449312e92cecf5340f83f59e00d529e7c726458e4e1a`.
- Observed normal executable SHA256:
  `e50c43a3d6ed0f60d5381ec9aefbba814369bcaf8347d9678dadb7eb550a7c59`.
- SQL-only catalog: 10,000 ready items, 2,000 tags, exactly 100,000 item-tag
  links and 350 collections. Each ready item has an explicitly synthetic,
  unwritten zero-byte primary descriptor so the real ready predicate and joins
  run. **These are not 10,000 actual objects or 100 GB of media.** No synthetic
  media GETs are performed. Fixture counts and 24 nonempty catalog/search results
  are asserted before timing; all 2,000 tags/350 collections must be returned.
- Five warm-up reads per route, then 100 timed reads per route. Measurements
  cover loopback HTTP through complete JSON-body consumption, including client
  overhead; they are not isolated server CPU time or browser rendering time.
- One real 24-second, 1920×1080, 30fps software-generated video, 23,755,597
  source bytes, uploaded through the actual HTTP part/Complete API and owned S3.
  Source SHA256:
  `a9db1a552504bd56dd538e241731090829638156201f64c5ab5032eee442b6d0`.
  Source generation and initial S3 allocation are outside API timing windows.
- During the app's observed `libx264` child process: six concurrent read loops
  of 100 requests each, 100 atomic two-item versioned metadata writes, and 100
  independent WAL snapshot reads that reject mixed tag/collection/version state.
  Each timed request must start and finish while that encode child is alive to
  count as overlap. All 100 per route/write loop overlapped in this run.
- SIGKILL while that encode remains active, then restart the same executable
  against the same disposable library/storage. The pending job must recover,
  publish within 30s after readiness, resolve its original Complete receipt and
  retain all 100 successful two-item metadata writes. Only one encode runs at
  a time; recovery necessarily starts a second attempt of the interrupted job.

## Actual results

Latencies in milliseconds; each row contains 100 samples. The search expressions
are `tag0001 || tag0002` and `(tag0001 || tag0002) && !tag0003`.

| Request | Warm median / p95 / max | Encode median / p95 / max |
| --- | ---: | ---: |
| Catalog 24 | 3.001 / 3.288 / 3.638 | 7.738 / 15.553 / 17.375 |
| Boolean search 24 | 9.137 / 9.570 / 9.900 | 12.521 / 21.078 / 39.452 |
| Boolean search with negation 24 | 9.184 / 9.546 / 9.904 | 13.081 / 24.114 / 40.268 |
| Complete tag directory | 1.754 / 2.125 / 2.743 | 6.551 / 12.786 / 20.433 |
| Tag statistics | 23.541 / 24.359 / 25.549 | 24.544 / 34.589 / 79.097 |
| Complete collection directory | 1.149 / 1.536 / 1.856 | 5.863 / 11.635 / 25.569 |
| Atomic two-item metadata write | — | 5.309 / 10.384 / 13.168 |

Startup to readiness: 54.61 ms. Warm idle API RSS: 51,740,672 bytes (49.3 MiB),
below the 300 MiB target. One observation after the 2.713 s overlap window showed
API RSS 67,317,760 bytes and FFmpeg RSS 327,565,312 bytes in separate processes;
these two observations are **not peak-RSS measurements**.

All 100 metadata writes completed during encoding, with no timeout, stale
version or SQL-busy response; worst observed write 13.168 ms. All snapshot reads
were atomic, and the reader observed multiple committed versions during the load. This is evidence against writer starvation for this bounded load,
not a proof for arbitrary workloads.

Restart to readiness: 53.71 ms. The interrupted source published and passed the
follow-up receipt/catalog/content checks within 4.272 s of readiness. The canonical
video GET streamed 14,166,208 actual S3 bytes. Both written item versions remained
101, and catalog total was 10,001. Final SIGTERM joined successfully. No physical
power loss was simulated.

`scripts/check-performance` completed successfully with race detection:
test 30.16 s, package 31.201 s. Local raw log:
`/tmp/tag-files-performance-final.log`. Earlier harness-only attempts were
discarded: a source exceeded the one-part fixture bound, then a 5 s client timeout
interrupted first physical S3 allocation. The final fixture uses the same 24 s
resolution/frame workload at a smaller source CRF and a 30 s transport timeout;
the measured 2 s API limit and 300 ms search-p95 assertions remain enforced.

Toolchain correction: `go version` at the repository root reports 1.27.3, but
`app/go.mod` requires 1.27.4 and the actual child build runs inside `app/` with
`GOTOOLCHAIN=auto`. A subsequent normal-build inspection confirmed
`go version -m tag-files: go1.27.4`, `CGO_ENABLED=1`, `GOARCH=arm64`, and
`-trimpath=true`. That later source build had SHA256
`198ed5a2d3259c736cf5b1c622582b64602254356cfa6db858dd6eb70c1364a7`;
it is distinct from the measured executable hash above and is toolchain
inspection evidence, not a rerun of the performance probe.

## Remaining acceptance boundaries

This run passes the selected queries’ 300 ms search-p95 budget, the probe’s 2 s
complete-JSON API response bound, idle API RSS ≤300 MiB, and the small interrupted
queue’s 30 s recovery bound in this environment. API response latency does **not**
establish browser first-useful-results ≤2 s; that browser budget remains separate.
The 100 GB physical library, broad/complex query distributions, reference Mac
hardware/VM limits, browser/FFmpeg peak memory, Safari/Chrome rendering, and
physical durability remain separate gates. The SQL growth counts have been
exercised; physical growth-profile bytes have not.
