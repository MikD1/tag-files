# Verified local Linux arm64 bundle

Recorded 2026-11-15. The final local archive bundle was built from
`60f35ca2e3c9ac002e75be3d5f0e27ddfd4db70e` with version `v0.1.0` for `linux/arm64`.
The archive-loaded images passed both expanded browser upload/editor cases in
9.9 seconds, simultaneous app/storage restart, retained primary reads and SPA
deep links. The storage archive passed the actual binary/notice/all-layer audit.
The release helper then sealed the bundle by re-exporting the tested immutable
images and comparing their original config and ordered uncompressed layer hashes.

No registry publication or hosted workflow ran. These are locally tested images;
this report does not establish production acceptance on the owner's Mac.

| Artifact | Immutable identity |
| --- | --- |
| App tested image | `sha256:ef3cc12a4a58ecd6e36a6689210a6e33f3879d2571507ec1b82d4956f09cf3d9` |
| App original config | `sha256:739b04ceac657a8357470beb25b6c09a8dc5b37617225368b97aeef170e76973` |
| Storage tested image | `sha256:70aa8659fd1622ffce9c39a65024c83b3619c248eb8a71c55ccc6af6f0e17ea5` |
| Storage original config | `sha256:3c62b937cdff80655a1a3fa4ccda59f413f54bd654bd0ad5b26dcd77af3fa718` |
| Independently audited backup tool | `sha256:ea33de2a15a0bb9fe0603710df34e372822ad8ceea06ce7d8406615ca16e4881` |

The sealed receipt and both archives remain at
`/home/mik.guest/projects/tag-files-release-ready-20261115-arm64/`.
`bundle.json` contains complete archive SHA256 values, config digests, diffIDs,
source commit, local run identity and `sealed: true`. The local image aliases
`tag-files-app:local` and `tag-files-storage:local-check` point at this pair at
the time of this report. Later documentation-only commits do not change them.

## Start a separate empty local installation

On this arm64 Docker daemon, the exact images are already loaded. Choose a new
external directory, unused project name and port; the installer refuses an
existing installation rather than replacing it.

```sh
scripts/install \
  --directory "$HOME/tag-files-local" \
  --version 0.1.0 \
  --app-image sha256:ef3cc12a4a58ecd6e36a6689210a6e33f3879d2571507ec1b82d4956f09cf3d9 \
  --storage-image sha256:70aa8659fd1622ffce9c39a65024c83b3619c248eb8a71c55ccc6af6f0e17ea5 \
  --no-pull --project tag-files-local --port 8080 --start
```

Open `http://127.0.0.1:8080`. The private deployment directory contains the pinned
image receipt, Compose configuration and independently generated credentials.
This creates a new library. Legacy migration is a separate explicit offline
operation documented in [migration](../tools/migrate/README.md).
See [INSTALL](INSTALL.md) for update, required cold backup and rollback rules.

## Supporting checks and exact scope

- The common local suite passed: generated contracts/SQL, Go vet/ordinary/race,
  both offline tools, Python deployment/release checks, TypeScript/ESLint,
  88 web tests and production build. The final notice collector separately
  passed five tests, including its later exact-version supplement regression.
- Sixteen Chromium/WebKit HTTP/SQLite browser cases passed in 29.1 seconds.
  The final image check additionally exercises real worker/XHR/S3 upload,
  lost accepted part response, full metadata persistence and pending controls.
- Full corrected native storage/media/runtime suite: 15 cases, 435.188 seconds.
  This preceded the packaging change. The repackaged storage received its own
  actual lost-ACK/restart/body/binding/1,001-part pagination/full-SHA/cleanup
  proof check: 49.10 seconds, race package 50.140 seconds, on frozen `20e806…`.
  Its unchanged `weed` and supervisor contents are exercised again by the final
  archive-loaded browser/restart pair; new artifact notices/layers are audited.
- Full repackaged backup tool cold restore: 72.802 seconds, including five
  independently checked multipart proof files and ten full object hashes.
  [Backup evidence](../tools/backup/EVIDENCE.md) records the exact tool/storage
  pair and limits. Native fixture migration/full verification passed 33.402
  seconds on the earlier release-image storage; its source fixture is SeaweedFS.
- Both maximum browser cases passed with 20 GiB padded valid MP4 plus a 256 MiB
  unsupported companion. [Browser measurements](../docs/rewrite/BROWSER-MEMORY.md)
  identify their earlier frozen pair, memory/control timings and telemetry gap.
  This is real-byte transfer/verification coverage, not 20 GiB of useful video.
- Independent whole-change and artifact reviews closed the concrete editor,
  multipart-proof and distribution-notice findings. The final storage audit
  covers 244 upstream dependencies, both supervisors and 343 notice/source
  files, and confirms no unused Rust executable exists in any distributed layer.
- The final editor/packaging source also passed a fresh amd64 image rehearsal
  under QEMU: two browser cases 7.0 s, real video conversion and restart/full
  hashes, runtime 63.953 s; 343-file/all-layer storage audit passed.
  [AMD64 evidence](AMD64-EVIDENCE.md) identifies those separate check images.
- The final arm64 pair passed the [browser growth probe](../docs/rewrite/BROWSER-PERFORMANCE.md):
  10,000 items with 20,000 real PNG/JPEG objects, 122,510,000 payload bytes;
  each engine 120 warm samples plus one fresh-context record, zero page/network
  errors. Warm navigation max 80.3/109 ms, SPA search max 62.2/83 ms. This uses
  small repeated media and normal caches on Linux.

Full traceability and remaining gates are in [ACCEPTANCE](../docs/rewrite/ACCEPTANCE.md).
Actual macOS Safari/Chrome/VoiceOver, Desktop sleep/power loss, native amd64 CI,
hosted release/pull, full real legacy MinIO/SQLite migration around 100 GB and
reference-profile performance measurements remain open. Existing legacy
sources stay available until that acceptance is complete.

Local final logs: `/tmp/tag-files-release-ready-build.log`,
`/tmp/tag-files-release-ready-runtime.log`,
`/tmp/tag-files-release-ready-notices.log` and
`/tmp/tag-files-release-ready-seal.log`. Disposable fixture containers, networks
and volumes were removed. Real user data was not attached or migrated.
