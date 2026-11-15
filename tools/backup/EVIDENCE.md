# Local verification, 2026-11-15

Executed on Linux arm64 with Docker 29.9.3 and the production SQLite driver.
This report concerns disposable synthetic volumes only.

After the durable multipart proof gateway corrections, a fresh full Go race
suite passed against the exact images below in **72.629s**. The native Docker
drill took **70.27s**; its measured cold backup + clean baseline restore +
independent recovery sequence took **47.312s**. Five Python host safety tests
and `go vet ./...` also passed. These are small-fixture timings, not a
full-library RTO claim.

Only the external tool image was rebuilt, under the unique local tag
`tag-files-cold-backup:proof-r2-drill-1794736289`. The corrected storage image
was reused by immutable ID; no shared image alias or storage rebuild wrapper
was used. The test-only assertion/log changes do not enter the tool executable.

```sh
cd tools/backup
TAG_FILES_BACKUP_IMAGE=sha256:ad0b0ae810fd237203388399dd325d8e888b08259c57073f29b4f2173912c622 \
TAG_FILES_STORAGE_IMAGE=sha256:c7061aaa1d9fb08d7143346c6ba0997dd1f7ee7defa8bd821a258410ac8fe1e5 \
env -u GOROOT go test -race ./... -count=1 -v -timeout=10m
```

Exact local image content IDs in that run:

- Tool: `sha256:ad0b0ae810fd237203388399dd325d8e888b08259c57073f29b4f2173912c622`
- Storage: `sha256:c7061aaa1d9fb08d7143346c6ba0997dd1f7ee7defa8bd821a258410ac8fe1e5`

The drill verified 155 archived entries/428,907 native bytes, ten complete
S3 object hashes with HEAD/Range/Content-Type/user metadata, private access,
and one confirmed 5 MiB multipart part with unchanged MPU identity/ETag/size/SHA.
The initial part must independently match the SHA256 of the actual generated
5 MiB source bytes, including number 1, exact size and nonempty opaque ETag.
The restored baseline ListParts must match that full record. A missing proof
journal would make the owned gateway refuse this listing rather than returning
an inferred SHA from the application binding.

The fully verified archive manifest contained **five regular multipart proof
files / 1750 bytes** under `.tag-files-mpu-proofs/`, including the format marker
and the retained partial/cancelled MPU state. Both fresh storage restores passed
startup recovery with these captured proof bytes. The independent recovery
copy then resumed the partial MPU, uploaded a fresh final part, completed it
and matched the full incoming object against the source SHA256. This proves
native copy/restore and checksum recovery for this fixture; it does not simulate
a power cut during fsync.

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
