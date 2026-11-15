# Large legacy migration CLI rehearsal (2026-11-15)

The focused Linux arm64 gate passed with the real, normally built offline CLI:
`scan`, an abruptly killed `copy`, a fresh `copy` process, and `verify`.
It copied and verified **12 objects / 2,147,483,734 actual bytes**, including one
2 GiB object in **128 × 16 MiB parts**. No production code changed for this gate.
This is synthetic SeaweedFS evidence; actual legacy MinIO and full-library
acceptance remain open.

Run from the repository root with Docker and the exact already-built image:

```sh
scripts/check-migration-large
```

The script runs only `TestLargeLegacyMigrationCLIResumeAndRSS`, under the Go
race detector. The child executable uses `go build -trimpath`, without race
instrumentation. The test skips in ordinary checks unless
`TAG_FILES_MIGRATE_LARGE_INTEGRATION=1`. Fresh focused compilation under race,
`go vet ./...` in `tools/migrate`, and shell syntax validation also passed.
The accepted integration test took **102.33 s**; the race package took
**103.356 s**. Fixture/build/independent checks are included in that test time.

## Exact artifacts and source

- Source HEAD: `5b5d8a546830adbd9cd207ba2c6412fd435ca154`, with test/harness work
  recorded in `source-status.txt`. No product code was modified in this slice.
- CLI: Go **1.27.4**, Linux arm64, CGO enabled, production SQLite driver;
  SHA256 `f04f567dbb13e019c7120fbca56d01343a1e83797f5044819a4a533ca368157b`.
  Complete module/build settings are retained in `build-version.txt`.
- Separate native source:
  `chrislusf/seaweedfs:4.53@sha256:ccad23e18639c8b98cee17e7100b95830a06077b56d8e5b006d6dcf8ec5e94a0`.
- Owned production target image:
  `sha256:70aa8659fd1622ffce9c39a65024c83b3619c248eb8a71c55ccc6af6f0e17ea5`.
- Accepted raw artifacts:
  `.performance.migration-3833386250` under the repository root, outside
  Playwright output. UTC window: **11:51:55.486–11:53:34.122**, before cleanup.
  Artifacts retain every CLI stdout/stderr, per-stage PID/exit/timing JSON,
  20 ms `/proc` samples, HTTP observations, independent expected hashes,
  before/after multipart proofs, disk observations, catalog comparisons,
  final verify JSON, manifest/raw rows and small SQLite evidence copies.

The 2,147,483,648-byte seed uses zero-filled gaps with different markers at both
ends of every 16 MiB part. Its independent SHA256 is
`1e766f219fc9efbf53450e13df9f1eace735a55e9c165052189d394ba324dff9`.
Setup and independent hashing use 128 KiB buffers, without a 2 GiB memory
allocation. The source PUT, CLI archival/full frozen-source checks, target
multipart, final verification, and independent archive/target hashes process
all actual bytes. The `.mp4` name and preserved `video/mp4` MIME do not make
these synthetic bytes a valid video; no probe/encode/browser claim is made.

## Interruption, preservation and read-only source

The observing target proxy preserves original SigV4 Host/Authorization,
pair/epoch headers, bodies and authenticated responses. Before forwarding part
4, it holds dispatch while the test independently reads the SQLite journal and
the owned gateway's authoritative `ListParts`. Exactly three parts have durable
local ACKs whose size, SHA256 and opaque ETag match their immutable binding,
independently hashed seed section and returned native proof. The test then
sends **SIGKILL** to that actual first CLI process.

At the fresh CLI's part-4 dispatch gate, all three returned/native and local
tuples, MPU identity, part geometry and epoch remain identical. The raw
observation log and final counter map show each of parts **1–128 forwarded
exactly once**, including the three acknowledged before interruption.

The source is another native backend behind an enforced GET/HEAD-only gateway.
PUT, POST, DELETE and PATCH each returned 403. The frozen SQLite file has mode
0444, an independent `O_WRONLY` attempt was refused, and its before/after SHA256
stayed `1ff178b63f99a7e94ce0d730e5c690ab940b8e0c7af5a6e408e12a7deb136447`.
Source, archive, target metadata and journal roots are disjoint and host-backed;
none of the large data uses `/tmp`. Source setup/full-read HTTP clients use an
explicit ten-minute bound instead of the small fixture's five-second client.

The existing independently authored legacy fixture remains present: all five
kinds and IDs 11–15, duplicate Unicode/case-sensitive tags, collections, every
scalar/nullable value, joins, and signed integer 100 ns ticks including both
int64 extrema and 9,007,199,254,740,993. Hardcoded independent target SQL
expectations and the final CLI report check all four category ID sets. The
archive includes temporary/orphan bytes, both actual versions of a Unicode
history key and one delete marker. All 12 source/archive/target payload hashes,
Content-Type and user metadata match independently seeded expectations; source
tags remain in the archive. `verify` returned `complete: true`, no exceptions.
That value is data verification, not cutover approval.

## Process memory and elapsed time

| Normal CLI stage | Seconds | Exit | `/proc` RSS/HWM peak bytes | Wait rusage peak bytes | Samples |
| --- | ---: | --- | ---: | ---: | ---: |
| scan | 7.232 | 0 | 23,109,632 | 68,767,744 | 362 |
| first copy | 9.980 | SIGKILL / -1 | 39,145,472 | 77,475,840 | 499 |
| resumed copy | 34.106 | 0 | 44,703,744 | 80,945,152 | 1,706 |
| verify | 15.106 | 0 | 42,618,880 | 86,757,376 | 756 |

Both measurements stayed below the chosen **256 MiB test regression bound**.
The maximum sampled child RSS/HWM was 42.63 MiB; the largest recorded wait
rusage was 82.74 MiB. This bounds the normal CLI processes in this fixture,
not the parent race harness, Docker services, app runtime or browser. The bound
is a test choice and does not establish a product memory budget.

## Disk observations and cleanup

These are **point-in-time `du -sk` observations**, not a continuously observed
native storage working peak or a universal disk budget:

| Checkpoint | Host work KiB | Source `/data` KiB | Target `/data` KiB |
| --- | ---: | ---: | ---: |
| empty backends, seed exists | 2,097,164 | 452 | 536 |
| source seeded | 2,097,164 | 2,097,728 | 536 |
| archive complete | 4,198,772 | 2,097,728 | 536 |
| three durable parts | 4,199,488 | 2,097,728 | 49,736 |
| resumed copy complete | 4,199,156 | 2,097,804 | 2,098,208 |
| full verify complete | 4,199,164 | 2,097,824 | 2,098,224 |

The initial host seed alone reported about 2 GiB allocated; sparse construction
does not establish disk-space savings on this host. Native allocations and
shared project filesystem free bytes are retained in `disk.json`. Concurrent
filesystem activity, allocation/GC and a Docker VM's limits need separate
operator sizing. Fixture admission inputs (32 MiB write allowance, 16 MiB
metadata allowance, one-byte reserves) are test inputs, not measured W/D/R
budgets for a real migration.

`cleanup.json` records the actual separate source container ID **and source
volume name**, plus target container/volume. All four were absent after their
cleanup; the host work directory, source/archive payloads and credential files
were removed. Only this fixture's resources were removed. Random credentials
are not retained in HTTP observations, build metadata or reports.

One earlier attempt in `.performance.migration-293665269` completed scan but
was rejected by the independent MIME oracle because native Seaweed inferred
`audio/aac` from a generic octet-stream setup. The fixture now seeds explicit
types; preservation assertions were kept. Its source read-only directory also
required manual permission restoration before deleting the remaining small
fixture DB. Its raw failed output remains distinct from accepted evidence.
These were harness preparation/cleanup failures, not production fixes.

Still open: the owner's actual MinIO/version/encryption/lifecycle and native
rollback snapshot, complete real source audit and exceptions, representative
continuous physical working-peak/full-library disk sizing, full pilot/offline
window, actual 100 GB library, target app/media acceptance on a disposable
copy, Mac/browser/accessibility and physical power-loss validation. No real
data migration, publication, push or release-readiness claim occurred.
