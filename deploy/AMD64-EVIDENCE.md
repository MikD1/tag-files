# Local amd64 image rehearsal

This record covers local Linux **amd64 containers under QEMU on an arm64 host**.
It does not constitute native amd64 execution, macOS Safari/VoiceOver acceptance,
power-loss evidence, a release, or publication. Browser processes run natively
on the arm64 test host; their HTTP requests exercise the amd64 services.

## Reproduction and isolation

Run from the repository root. `scripts/check-amd64` builds the current pinned
application/storage Dockerfiles for `linux/amd64`, records build metadata and
image identities, then creates a random Compose project with three fresh named
volumes, independent random credential files and a temporary loopback port.
It uses a private copy of `deploy/compose.yml` plus an amd64 platform override.
Both service references are the exact inspected immutable image IDs. No existing
library, shared local image tag, application source, or Compose file is changed.
Only `tag-files-app:amd64-check` and `tag-files-storage:amd64-check` are built.
The script removes its own containers, networks, three volumes and credential
files, retaining reports and logs under the selected artifact directory.

For an arm64 Linux Docker host without amd64 support, inspect existing handlers
first. The [Docker multi-platform instructions](https://docs.docker.com/build/building/multi-platform/)
describe statically linked QEMU registration with the `F` flag. The
[upstream binfmt instructions](https://github.com/tonistiigi/binfmt/blob/master/README.md)
support selecting architectures and uninstalling one exact handler. This run
used that procedure restricted to amd64, with an immutable image reference:

```sh
ls /proc/sys/fs/binfmt_misc
# Continue only when qemu-x86_64 is absent; preserve every existing handler.
docker run --privileged --rm \
  tonistiigi/binfmt@sha256:400a4873b838d1b89194d982c45e5fb3cda4593fbfd7e08a02e76b03b21166f0 \
  --install amd64
cat /proc/sys/fs/binfmt_misc/qemu-x86_64
docker buildx inspect default
scripts/check-amd64 --artifacts /tmp/tag-files-amd64-evidence
# After this fixture and all of its builds have exited, remove only the handler
# installed above. Never remove a handler that predated the rehearsal.
docker run --privileged --rm \
  tonistiigi/binfmt@sha256:400a4873b838d1b89194d982c45e5fb3cda4593fbfd7e08a02e76b03b21166f0 \
  --uninstall qemu-x86_64
```

The script requires an existing usable handler on arm64 and does not itself
perform privileged registration/removal. `--skip-build` can rehearse already
built dedicated tags, still inspecting and pinning their actual IDs. Default
builds retain `app-build.json`/`storage-build.json`, build logs, `browser.log`,
`runtime.log` and `report.json`; failed runtime checks also retain evidence.

## Checks

- Healthy app/storage startup, empty actual SQLite library, HTTP readiness and
  SPA deep link from the image's built static assets.
- Existing release Playwright cases in Chromium and WebKit: actual background
  worker SHA calculation, native Blob XHR, route navigation during upload,
  publication through S3 and the media worker, image decoding, HEAD and Range,
  removal of ready sessions/batches. Chromium also loses an accepted part
  response and reconciles it without uploading that part a second time.
- Actual **amd64 FFmpeg** creates a 0.6-second, 80×48 MPEG4/PCM MOV fixture. The
  public HTTP create/part/Complete protocol streams it through the app and S3;
  the real durable worker converts it. Independent image ffprobe confirms H264,
  AAC and unchanged dimensions/duration; MP4 `moov` precedes `mdat`, and actual
  image FFmpeg fully decodes the downloaded primary. The generated video JPEG
  poster is retrieved through the same-origin media endpoint.
- All three complete primaries are downloaded and SHA256 recorded; each passes
  HEAD and exact 32-byte Range. Both services restart on the same disposable
  volumes and exact images. The complete catalog and all three full-byte hashes
  must match; readiness and SPA deep links must work again.

## Latest run with the final editor and distribution packaging

Passed on **2026-11-15**, from committed source
`9856f01810c3331009b0cf41f4c2969faec13812`. This rebuild includes the pending
metadata/Enter fixes, Collection chevron and final storage notice/runtime
packaging. It uses `VERSION=amd64-check`; the separately sealed `v0.1.0` arm64
archives are unchanged. Execution remains QEMU on Linux arm64, with native host
Chromium/WebKit processes talking to the actual amd64 services.

```sh
scripts/check-amd64 --artifacts /home/mik.guest/projects/tag-files-amd64-final-20261115
# exit 0; runtime fixture 63.953 s, excluding builds/cleanup
# Chromium 2.9 s; WebKit 3.4 s; Playwright: 2 passed in 7.0 s
scripts/check-image-notices storage \
  sha256:eee0946b0efa11ce431a2bfcd67670548fbb402b95d37cc373e1468078191cbc
# exit 0; 343 notice/inventory files; all distributed layers exclude unused Rust
```

| Image | Identity | Digest |
| --- | --- | --- |
| App | Tested OCI index | `sha256:d435b2f4f3610825c99044dd39c732e291dfc29cf17cb7d6ba785fcd86d79683` |
| App | Runtime manifest | `sha256:53e29406608128152d74b9fcb84621803e79e76651b096047e648baa995b2c28` |
| App | Runtime config | `sha256:1e55873ba51e5cf12fca317f29c94bdfe0f62bc89cf8396e17953917554c3296` |
| Storage | Tested OCI index | `sha256:eee0946b0efa11ce431a2bfcd67670548fbb402b95d37cc373e1468078191cbc` |
| Storage | Runtime manifest | `sha256:c2dafc679e75ee12f348ac7fd731777a2ed4e08e7f3bc18c382b3f06404060be` |
| Storage | Runtime config | `sha256:3af852e2f07f08b4744a47c11b12dcd8adef1bddacbc47a1f9f5dacaba294619` |

The QEMU Go build/build-info/notices step took 201.8 s. Both expanded browser
cases also checked search Enter, native option selection, all pending editor
controls and persisted metadata after a delayed real PATCH. The video fixture
again converted 57,908 source bytes to 12,409 H264/AAC MP4 bytes and a 265-byte
JPEG poster. The complete primary SHA256 values match the preceding run's
values below; all three primaries and the whole catalog matched after both
services restarted. The runtime reported `x86_64`, UID 10001 and FFmpeg
8.0.1-3ubuntu2. HEAD, exact Range, empty ready batches and SPA deep links passed.

The storage artifact audit covered 244 actual `weed` dependencies and two each
for both supervisors, original notice/source hashes, pinned runtime files and
every distributed layer. The 274 selected production/build inputs were identical
before and after the parallel test-harness work; both JSON manifests have SHA256
`fc4052cd7021904ebaecf2a243394918c3d0a548d0c7ea43449aeac3b246ee2b`.

Build metadata/logs, browser/runtime logs, `report.json`, `notices.log`, source
manifests/comparison and `cleanup.json` are retained in the artifact directory
above. The owned project `tag-files-amd64-e82f8e600902` has no remaining
containers, networks or volumes. Only this run's newly registered `qemu-x86_64`
handler was removed; the pre-existing `python3.14` handler is byte-identical.
Native arm64 image aliases were preserved. Native amd64 CI, hosted publication,
maximum-size/fault-matrix execution on amd64 and physical Mac acceptance remain
separate gates.

## Earlier run after the final multipart-proof corrections

Passed again on **2026-11-15**, using the final corrected multipart-proof runtime
sources and the same digest-pinned QEMU installer above. The app and storage
images were rebuilt for `linux/amd64`; the exact resulting IDs were used for
all checks. This remains QEMU execution on Linux arm64, with native arm64 host
browser processes. It is separate from native arm64 or native amd64 evidence.

```sh
scripts/check-amd64 --artifacts /tmp/tag-files-amd64-proof-final-20261115
# exit 0; runtime fixture 59.424 s, excluding builds/cleanup
# Chromium 4.5 s; WebKit 2.8 s; Playwright: 2 passed in 8.0 s
```

That run's exact image identities are:

| Image | Identity | Digest |
| --- | --- | --- |
| App | Tested OCI index | `sha256:fca33d70aecae48dd08bf9cf9aea6409ff9d53c6a95c739302fdfda7e0452013` |
| App | Runtime manifest | `sha256:73ed2746307e309e829b23e3fe6c75a2497572739e9190170126efb4bef87344` |
| App | Runtime config | `sha256:48f41ebddafdc2405b1ebcf7fd4fea7da9999dc7536b9398131cf21bfcf20cab` |
| Storage | Tested OCI index | `sha256:1a688f8722c053f9bcc0b2b5f2f90395da2004367002a77bddf2901a8a928929` |
| Storage | Runtime manifest | `sha256:7c5b64edc1974e95336f6c2c08bc1508b025f68ef52ad10b7b4a278f158c9394` |
| Storage | Runtime config | `sha256:948a6a23029ed684be489b2415b56be156a65dc5a1ed292d5cc883b010064182` |

App CGO/build-info/notices compilation took 217.5 s under QEMU. Build metadata
retains the pinned bases and resolved Dockerfile frontend. The container reported
`x86_64`, application UID 10001 and FFmpeg 8.0.1-3ubuntu2. The image version argument
was `amd64-check`; these locally built check images are not published releases.

This run confirmed both browser upload cases, including Chromium's accepted
part response loss and reconciliation, then converted the real 57,908-byte
MPEG4/PCM MOV to H264/AAC 80×48 with duration 0.6 s, MP4 faststart and full decoded
primary. The 12,409-byte primary SHA256 was
`51b013e178cf5eba43cd754696a2c7741a30fd9d63812d4b81630607b121a5a8`;
the 265-byte JPEG poster SHA256 was
`5656504cebcdee08a3018d083469d02be55fada0e6a7fbe9b09e0c0eed87791d`.
Both photo primaries matched their full original PNG bytes, SHA256
`060a2853ddd984094ed5ee791d8c4633b10a0933f07be02313d3e48097fe8269`.
All three complete primary hashes and the whole catalog matched after both
services restarted on their existing disposable volumes. Readiness, media HEAD,
exact 32-byte Range, empty ready-upload batches and SPA deep links passed.

Source identity is recorded for the actual dirty working tree rather than
inferred from a commit label. Before the build, HEAD was
`573428e638c5e96f09b516d2b1ac636393c130d7` with the multipart-proof changes still
uncommitted. `source-manifest.json` hashes 359 selected app/web/build-and-run input files,
including untracked proof sources, with their paths, sizes and modes:

- Before-build selected-input manifest SHA256:
  `e294f89e5dd645c29f4c42b2858b422b998aa5360a9fe4cd3e7c6c37039e28a2`.
- Post-run selected-input manifest SHA256:
  `d45258fc83b14f2c1f72a08dd993e6e898a47f5e7724767d54e9d4a25d86aef2`.
- The 210 runtime-source inputs were identical before and after, SHA256:
  `68d74eb90ea9837672279d8fa25e281a99ff0cc2c855643135ac28578f3fa0f5`.

The runtime subset covers production Go/module/embedded SQL, SPA build
source/assets/config/notices, runtime Dockerfiles/filer config/Compose, LICENSE
and the Go notice collector. Its definition and all per-file hashes are retained
in `runtime-source-comparison.json` and `runtime-source-manifest.json`.
Among those selected inputs, changes occurred only in `.dockerignore`,
`app/internal/storageprofile/README.md` and
`web/e2e/large/upload-memory.spec.ts` during the parallel work. Those files do not
change compiled Go/SPA or runtime inputs; all final multipart-proof source hashes
remained identical. `multipart-proof-source-hashes.json` retains the individual
proof/gateway/coordinator/recovery hashes. No repeat build was needed solely
for these documentation/ignore/browser-harness edits.

Commits advanced during that parallel work: app build provenance records
`573428e638c5e96f09b516d2b1ac636393c130d7`, storage build provenance records
`4b1e320212b46abb6a83c2bb7abfa4ea4d383423`, and the post-run working HEAD was
`55411fbd15a653b33b97aca5331495fdd760f44c`. The runtime content hash above, source
snapshots and exact image identities provide the evidence for tested bytes.

Evidence is retained under `/tmp/tag-files-amd64-proof-final-20261115/`: build
metadata/logs, browser/runtime logs, `report.json`, initial status/tracked diff,
selected/runtime source manifests and `cleanup.json`. Fixture project
`tag-files-amd64-97061e0af962` and its three named volumes, both networks and
secret files were removed. Only this run's newly registered `qemu-x86_64`
handler was unregistered after completion. The pre-existing `python3.14`
handler stayed byte-identical. Shared/native image tags and parallel native
browser resources were not changed. The dedicated amd64 check image tags remain
for inspection. No source code, test harness, Dockerfile, Compose, release or
registry was changed by this rerun; only this evidence document was updated.

This scope verifies the latest amd64 image workflow with small real media and
restart. It does not repeat the full multipart-proof fault matrix, 20GiB gate,
whole-library restore, native amd64 CI or physical durability acceptance.

The later catalog-only fix in `bdcff1a` disables pending metadata controls and
prevents implicit search-Enter saves. It and the later distribution-notice
packaging are outside this earlier run's source/image scope. The latest run
above covers both on rebuilt amd64 images. Native amd64 CI remains separate.

## Earlier run before the final multipart-proof corrections

Passed on **2026-11-15**. Host: Linux aarch64, Docker Engine 29.9.3,
BuildKit 0.34.2. The added handler used flags `POCF`; emulator reports
`binfmt/e29e7d7 qemu/v10.2.3 go/1.26.4`. Container `uname -m` returned `x86_64`
and application runtime UID was 10001. The image's FFmpeg was 8.0.1-3ubuntu2.

```sh
scripts/check-amd64 --artifacts /tmp/tag-files-amd64-evidence-20261115
# exit0; runtime fixture 61.303 s, excluding builds/cleanup
# Chromium 2.5 s; WebKit 2.7 s; Playwright: 2 passed in 5.7 s
```

The tested OCI index digests (including provenance) are:

| Image | Exact tested digest |
| --- | --- |
| Application | `sha256:045a780d907fbcac68a0bfeda1e23fae2a30f148419f17d51bd5cfbe9b37a780` |
| Storage | `sha256:71153c7fd89000699a51c6636cea69a01a9e1c5c9a1797d73f6c3c0e131f6c71` |

Their runtime image manifest/config pairs are respectively:

- App manifest `sha256:0ff743a32f453e1c0e94a59e5d341728a45b565caa0489cc4e2e876107f4d859`,
  config `sha256:51fd8338a0eaacb901aa2a56f0753a2d3a797ba55a1946f099d3d1641dbfb89f`.
- Storage manifest `sha256:8461e17354d948556ca1697f7d4b8cc21244bde63a4281b639ab8beea1c7115d`,
  config `sha256:93d85f420e27a974534ce22a436c7fb01d98d0d5ce64d09addd55fbb050ed205`.

The original cold app build completed with its CGO/build-info/notices step in
205.6s under QEMU; its runtime package installation took 215.6 s in parallel.
The final gate rebuilt with cached layers and retained build metadata before
pinning the exact identities above. No registry push occurred. The provenance
records the Go/Node/Ubuntu/Seaweed base index pins and requested platforms.
BuildKit's Dockerfile frontend resolved to
`sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e`.

The working tree was uncommitted on main at HEAD
`b06eadb1f626d87c96a1b3715a8815034a1cf79b`. Inspected input hashes:

| Input | SHA256 |
| --- | --- |
| `deploy/app/Dockerfile` | `1ddad5b6773d22af8781080c8704dd58b9513b8a6f0c9f132a1600e536556125` |
| `deploy/storage/Dockerfile` | `e4b1c55616d5620a231179e263c08dd5fc19a3b731c0d3775c491c60853de1d1` |
| `deploy/compose.yml` | `49786aa938dbc79e00f59d11d827d2e9a6adae842172e32657e43db66ed87b3c` |
| `web/e2e/release/uploads.spec.ts` | `ac002155d85ee480d432594459a0d9626782ab2cb26daa915b94ea6facdb1550` |

The source video was 57,908 bytes, SHA256
`767471d0ccd0d1727bb9cda9087d55d414b1031a8bf55de39c63253f71d04070`.
The converted MP4 was 12,409 bytes, SHA256
`51b013e178cf5eba43cd754696a2c7741a30fd9d63812d4b81630607b121a5a8`;
the same-origin JPEG poster was 265 bytes, SHA256
`5656504cebcdee08a3018d083469d02be55fada0e6a7fbe9b09e0c0eed87791d`.
Both independently uploaded photos matched the complete original PNG bytes,
SHA256 `060a2853ddd984094ed5ee791d8c4633b10a0933f07be02313d3e48097fe8269`.
All three primary hashes and the entire catalog matched after restarting the
pair; the ready uploads queue was empty. Report includes source/output stream
probes, identity details and checks rather than only item counts.

Retained local evidence: `/tmp/tag-files-amd64-evidence-20261115/`
contains build provenance/logs, browser/runtime logs, `report.json` and
`cleanup.json`. The fixture project `tag-files-amd64-9a4de012bae1` and its
three volumes, both networks and credential files were removed. After all QEMU processes exited,
only the newly added `qemu-x86_64` handler was unregistered. The pre-existing
`python3.14` handler remained byte-identical; no other handler was changed.
Dedicated test image tags/cache remain for inspection. BuildKit can retain a
cached platform advertisement after handler removal; the script checks the
actual handler before attempting another arm64-host run.

## Remaining acceptance

The native amd64 GitHub Actions matrix has not been run or published from this
local session. This rehearsal covers small real media and restart under QEMU;
it does not imply native amd64 performance, the complete media/fault matrix,
large valid-video transcoding, physical durability, or real Mac browser and
accessibility acceptance. Those gates stay explicit in the rewrite acceptance
matrix.
