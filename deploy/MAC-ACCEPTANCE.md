# Native Mac acceptance handoff

These checks remain open until they run on the actual target Mac. Linux arm64,
QEMU amd64 and Playwright WebKit on Linux do not establish Safari, VoiceOver,
Docker Desktop sleep or physical durability. Use a disposable library; do not
attach the real legacy volumes for these checks.

## Record the environment

Keep the checkout commit, app/storage image references and immutable IDs,
`docker version`, `docker compose version`, `docker info`, macOS/browser versions,
Mac model, Docker Desktop CPU/RAM allocation, available host/VM disk and volume
filesystem in the evidence. Redact credentials from any shared output. The
reference budget profile is four CPUs/four GiB for the Docker VM; measurements
on other allocations must say so.

`scripts/check-amd64` is an emulation helper for a Linux arm host, not the native
Mac acceptance runner. Install/update require images native to the Docker daemon.

## Repeat the image/browser workflow

With Docker Desktop, Python3, Node26.13 and npm available in the checkout:

```sh
npm ci --prefix web
cd web
npx playwright install chromium webkit
cd ..
scripts/check-app-image
```

This builds the new Linux app/storage images for the daemon's native platform,
tests actual browser uploads and pair restart, and removes only its disposable
project/volumes. Keep its exact image IDs and test output. It does not test the
shipping Safari application or VoiceOver.

For manual browser checks, create another independent empty installation using
the [install runbook](INSTALL.md) with `--no-pull --start` and those tested local
images. Choose an unused project name, external private directory and loopback
port; record all three. Preserve this fixture and its receipts while examining
failures. Installation never imports the old library automatically.

## Shipping browsers and accessibility

In current Chrome and Safari against the same disposable origin, record:

- Photo/video upload, collection selection, byte progress and cancel. Close or
  reload during processing; the pending/error queue recovers, completed rows
  disappear and the new primary opens. Retrying an unsupported file retains a
  clear error/input until explicit cancel.
- All five sections, direct/reloaded `/content/:id`, grid/list and persisted
  sidebar/seven density levels. Search/type/sort/Clear and page transitions
  preserve the specified URL behavior.
- List single/bulk editor with existing tags and at least350 collections;
  keyboard navigation, no implicit create/save, stable popup/dialog and focus
  return after Cancel/Save. Bulk replace changes both fields atomically.
- Viewer image zoom/rotation and keyboard controls, native video playback/seek/
  volume/autoplay fallback, prev/next/Back, three-second view counting, related
  collection/same-tags links and confirmed permanent deletion.
- With VoiceOver: icon names, active navigation, dialogs/fields/errors, selection,
  sort menu, viewer controls and focus return. Keyboard-only use must expose
  controls otherwise revealed by hover.

Use the [acceptance matrix](../docs/rewrite/ACCEPTANCE.md) and immutable v27
baseline for the detailed behavior; record failures rather than inferring a
pass from screenshots. Measure warm first-useful browser rendering separately
from API latency, without waiting for every image.

## Desktop recovery and external data rehearsal

Upload real photo/video fixtures, retain an idle/error upload and interrupt an
active upload/encode. Exercise Mac sleep and Docker Desktop restart; compare
primary/poster hashes, catalog/relations, upload identities and cleanup after
recovery. Repeat cold backup/restore into fresh volumes using the external
[backup tools](../tools/backup/README.md), preserving the untouched baseline.

Physical power/VM interruption requires a dedicated disposable environment and
an independent verified backup. A process/container kill is a different test;
do not label it power-loss evidence. Record preserved global fsync configuration,
native metadata/chunks and acknowledged parts/objects after restart.

The actual-library cutover additionally needs read-only scan/copy/resume/verify
on an independent copy of the legacy SQLite/MinIO library, full object hashes,
explicit provenance exceptions, measured free-space demand and backup/restore
RTO. Follow [migration](../tools/migrate/README.md) and [operations](../docs/rewrite/04-migration-and-operations.md).
Do not retire or write to the source until that evidence is accepted.
