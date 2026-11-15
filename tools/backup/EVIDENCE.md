# Local verification, 2026-11-15

Executed on Linux arm64 with Docker 29.9.3 and the production SQLite driver.
This report concerns disposable synthetic volumes only.

`env -u GOROOT tools/backup/check-drill` built both exact images and passed
the host/unit/native suite. After adding the verified-but-unpublished candidate
fixture, the fresh full Go race suite against those same exact images passed
in **87.434s**. The expanded native Docker drill took **85.05s**; its measured
cold backup + clean baseline restore + independent recovery sequence took
**61.804s**. Five Python host safety tests also passed. These are small-fixture
timings, not a full-library RTO claim.

```sh
cd tools/backup
TAG_FILES_BACKUP_IMAGE=tag-files-cold-backup:local-drill \
TAG_FILES_STORAGE_IMAGE=tag-files-cold-backup-storage:local-drill \
env -u GOROOT go test -race ./... -count=1 -v -timeout=10m
```

Exact local image content IDs in that run:

- Tool: `sha256:f54abecc9d0cbecaf3290739f0d747ef9e9def564595933243297c596fd82543`
- Storage: `sha256:380d9980ccccd42204b68a0ce98d2b96ede612bbc92c2c97e445bfc32f40975a`

The drill verified 147 archived entries/427,122 native bytes, ten complete
S3 object hashes with HEAD/Range/Content-Type/user metadata, private access,
and one confirmed 5 MiB multipart part with unchanged MPU identity/ETag/size.
Repetitive part data compresses in native storage, so native archive bytes are
smaller than logical object/part bytes. All fixture containers and named
volumes were removed by the test.

The original restored baseline was checked with read-only SQLite and external
S3 reads, then stopped. Normal domain/FFmpeg workers ran only on a second
fresh restore: interrupted lease fencing, queued photo publication, fresh
partial-MPU Complete followed by full incoming hash, ready Complete replay
without an Uploads row, a restored verified candidate with its exact pre-commit
descriptor followed by independent cleanup after newer publication, independent ready/cancel cleanup and retained old
failed/lost-MPU input state were checked. Further full reads proved those
writes did not reach the baseline or immutable archive.

Unit evidence additionally covers crash-left WAL without source writes,
max-int64 100 ns duration preservation, duplicate/case-sensitive Unicode tags,
mutated archive/checkpoint rejection, unsafe tar traversal after a refreshed
outer checksum, symlink/FIFO refusal, both writer locks, credential mismatch,
missing native filer/chunk state, nonempty restore refusal, special Unix mode
preservation, and interruption after SQL rebasing but before storage marker
publication. Both runtimes refuse the retained incomplete markers.

Fresh standalone checks also passed:

```sh
(cd tools/backup && env -u GOROOT go vet ./...)
(cd tools/backup && env -u GOROOT go test -race ./... -count=1)
(cd app && env -u GOROOT go test -race ./internal/store ./internal/storageprofile -count=1)
```

The host script supports private parent directories with individual 0444
secret binds so app UID10001 can read them after restore. Secret values are
never printed in manifests/reports; complete archived deployment contents
remain inside private tar files/generation directories.

Still required before release: full real-library rehearsal and RTO/disk
measurement; real browser fingerprint/resume and Safari/VoiceOver acceptance;
linux/amd64 execution; Apple Silicon macOS/Docker Desktop sleep/restart;
physical VM/power-loss durability. Custom external filer metadata overrides are
refused by this profile and require a separately extended/drilled format.
