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

## Recorded run

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
