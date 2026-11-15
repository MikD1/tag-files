# External cold backup and restore

This is a separate host script and Linux tool image for the owned rewrite
profile: SQLite schema 6/library format 1 and SeaweedFS 4.53, LevelDB2, storage
format 2. It is not an app command, endpoint, UI or scheduler. Legacy migration
and historical migration archives remain separate. Keep the legacy deployment
and any previously verified generations until replacement acceptance.

Build from the repository root:

```sh
docker build -f tools/backup/Dockerfile -t tag-files-cold-backup:local .
python3 -m unittest discover -s tools/backup -p host_test.py
(cd tools/backup && go test -race ./...)
tools/backup/check-drill
```

The tool uses the production CGO SQLite driver. The pinned Go/Alpine builder
produces a static binary in a separate scratch image; it never receives the
Docker socket or a network interface. Native filesystem operations run inside
Linux. Python 3 and Docker CLI are needed on the host. The script checks all
containers for mounts of the protected named volumes, requires explicitly named
stopped app and storage containers, and holds their native ownership locks for
the operation. A paused/restarting container also counts as running. It never
stops or starts a service automatically.

The scratch image also ships `/usr/share/tag-files/notices`: the original
project MIT LICENSE, Go 1.27.4 LICENSE, notices for the actual Go dependency
graph (including the locally replaced app module),
[musl 1.2.6 COPYRIGHT](https://git.musl-libc.org/cgit/musl/plain/COPYRIGHT?h=v1.2.6),
and GCC 15.2.0's original
[GPL 3 text](https://github.com/gcc-mirror/gcc/blob/releases/gcc-15.2.0/COPYING3)
and [GCC Runtime Library Exception](https://github.com/gcc-mirror/gcc/blob/releases/gcc-15.2.0/COPYING.RUNTIME).
The static link map records the actual musl/libgcc members, alongside compiler
version and Go build metadata. All license assets retain their upstream bytes.
`scripts/check-image-notices backup IMAGE` checks the resulting image rather
than assuming that a Docker build copied these files.

Only the owned storage profile is supported: `/data` contains the entire
master, LevelDB2, volumes/indexes, hidden state and configuration. The application
has a separate named `/data` volume and optional named `/scratch`; scratch is
recreated after restore. Read-only individual secret binds under `/run/secrets`
are captured. The host rejects other mounts, volume drivers/options, extra
persistent paths and filer overrides. It does not silently omit custom storage
metadata: a deployment using that configuration requires an explicitly extended
and drilled tool profile first. Symlinks, hard links, sockets/FIFOs/devices,
non-UTF-8 paths and overlapping roots are refused without publishing a backup.

## Back up

Select an external destination outside the Docker data root/disk image and
both active volumes. A removable disk mounted on the host is suitable. The
script cannot establish that an arbitrary macOS path resides on an independent
physical device; verify the chosen carrier before Docker Desktop reset or
uninstall. Retention, carrier, schedule and RPO remain the owner's choices.
No generation is automatically removed.

Stop the app completely, wait for exit, then stop storage. Use the actual
Compose project/container/volume names for the installation:

```sh
docker compose stop -t 60 app
docker compose stop -t 30 storage

tools/backup/cold-backup backup \
  --image tag-files-cold-backup:local \
  --app-container tag-files-app-1 \
  --storage-container tag-files-storage-1 \
  --metadata-volume tag-files-metadata \
  --storage-volume tag-files-storage \
  --deployment /path/to/deployment-files \
  --destination /Volumes/Backup/tag-files \
  --generation 20261115T120000

tools/backup/cold-backup verify \
  --image tag-files-cold-backup:local \
  --archive /Volumes/Backup/tag-files/20261115T120000
```

The deployment directory contains the exact Compose file and other host
configuration needed to recreate this installation. The script adds stopped
container inspections (including environment/commands), exact local image
content IDs, available registry digests, platform details, the effective
`filer.toml`, and both effective secret files from the stopped storage
container. Captured secrets must match the owned storage credential digest.
These files and the generation are private; keep the external carrier protected.
The app/storage/tool image content IDs are required to exist locally at restore;
retain the corresponding registry images or separately save them with Docker.
Image rebuilding is not proof that the resulting digest is identical.

The tool estimates full archive bytes, PAX headers, SQLite workspace and a
64 MiB free-space reserve before starting. Restore charges volumes sharing a
filesystem together. Recheck actual space for a full generation plus additional
baseline and disposable recovery copies; this is significant for a 100 GB
library. SQLite temporary extraction/snapshots live in a private directory on
the external host destination, mounted as `/work`; native object bytes stream
with fixed-size buffers. The tool never places whole storage volumes in RAM.
The manifest is limited to one million entries/256 MiB; exceeding a limit fails
closed.

A generation is built as `<generation>.part`:

```text
backup.json                  # format/version/pair/image IDs and artifact hashes
metadata-volume.tar          # whole app volume, including WAL and dotfiles
storage-volume.tar           # whole native /data, including hidden/MPU state
deployment.tar               # Compose/config, inspections, effective secrets
sqlite-snapshot.sqlite       # independently checked coherent SQLite snapshot
manifest.jsonl               # every path/type/size/SHA-256/mode/UID/GID/mtime
checkpoint.json              # last durable archive/verification stage and UTC
complete                     # SHA-256 of backup.json, published last
```

Original source volumes are read-only. SQLite WAL recovery and SQLite Backup
API run only on disposable extractions. `integrity_check`, foreign keys,
supported format/history and persistent library/storage pairing are checked.
The standalone snapshot must match the archived SQLite+WAL byte-for-byte after
the same backup operation. Archive files are reread fully; each tar entry is
checked against the manifest. Source composition/metadata/full file hashes are
reread under both locks before publication. Master/filer/config presence,
config hash and volume/index pairs are checked. A fully synced completion
marker is followed by atomic directory rename; a `.part` directory is never an
accepted generation. Failed generations remain for investigation, and a retry
uses a new generation name. The format uses checksums, not cryptographic
signatures against an attacker who can rewrite the entire generation.

After success or failure, preserve the source pair. To resume normal operation,
start storage first and run its external preflight before starting the app:

```sh
docker compose start storage
docker exec tag-files-storage-1 storage-run check
docker compose start app
```

Storage health alone and unit tests do not establish physical power-loss
survival. The real restore drill below is required for every archive/tool
format change.

## Restore into fresh volumes

Stop both current services in the same order. Create two new plain local named
volumes and a new empty host configuration directory; never overwrite the
active pair. The `--app-container`/`--storage-container` names still identify the
stopped current deployment. Destination volume names identify the fresh pair.

```sh
docker volume create tag-files-restored-metadata
docker volume create tag-files-restored-storage
mkdir -m 700 /path/to/restored-deployment

tools/backup/cold-backup restore \
  --image tag-files-cold-backup:local \
  --app-container tag-files-app-1 \
  --storage-container tag-files-storage-1 \
  --metadata-volume tag-files-restored-metadata \
  --storage-volume tag-files-restored-storage \
  --archive /Volumes/Backup/tag-files/20261115T120000 \
  --deployment-output /path/to/restored-deployment
```

The host verifies the read-only archive independently before granting target
writes. The tool refuses incomplete/mixed/changed generations, unsafe tar
paths/links, nonempty destinations and any active container using the target or
current pair. It preserves complete contents, Unix permissions (including
special bits), UID/GID and modification times. Checksums, structure and SQLite
integrity/relations are checked again before the controlled restore rebase.

Storage format 2 intentionally pins a physical filesystem/root inode. Copying
its marker to a fresh volume must not admit an ordinary runtime rebind. The
tool preserves the logical library and storage UUIDs, then performs the one
external identity adjustment on the already verified fresh copies:

1. Place `.cold-restore-incomplete` in both roots before extraction and hold
   both ownership locks. Both application and storage refuse that marker before
   normal startup mutations, including a directory or dangling symlink marker.
2. Compute the new storage root fingerprint. On SQLite, `BEGIN IMMEDIATE` with
   FULL/foreign keys temporarily removes only `immutable_storage_pair`, updates
   `library_meta.storage_volume_id` guarded by the original three identities,
   and recreates the exact saved trigger DDL in the same transaction. Logical
   identities, all other rows and integer durations are retained.
3. After SQLite commits and closes, atomically replace/sync only the copied
   storage ownership marker's physical volume ID. Reload both sides and check
   the complete rebased pair and SQLite integrity.
4. Remove/sync the metadata incomplete marker, then the storage incomplete
   marker; keep locks until completion. Any earlier interruption leaves at
   least one guarded root or an identity mismatch. No service is started by
   this script.

Filesystem and SQLite commits are separate durability boundaries. A failed or
interrupted restore is never switched into deployment, and it is not silently
resumed into a nonempty pair. Preserve its checkpoints/markers for diagnosis,
then repeat into another new empty pair. Do not remove incomplete markers or
rebind either half manually. The original archive remains unchanged. The host
JSON report records original/restored physical IDs and the retained logical
identities; save it with the external operational log.

Start only the restored storage at the **exact saved image digest**, with the
restored secrets and effective config on an isolated network. Run
`storage-run check` to establish persisted global fsync, supported paths,
private access and bucket settings. Keep the app stopped. Read-only external
checks must compare every object through HEAD and full GET/hash, Content-Type
and metadata, Range responses, and `ListParts` (MPU identity, numbers, ETags,
sizes and available checksums) against the saved SQLite sessions/part bindings.
A storage marker is a pairing guard, not proof of coherent backup.

Do not launch app recovery/browser/FFmpeg on this verified baseline. Restore a
second disposable pair from the same archive and run normal recovery there.
Verify old leases are fenced, queued inputs publish or retain visible errors,
failed/idle input survives age, completed session history remains absent,
`items.upload_id` provides ready replay, and independent ready/cancel cleanup
finishes without an Uploads row. Resume requires the browser to verify the full
content fingerprint after file reselection; a name/size/clientFileId match
alone is insufficient, and browser file handles cannot be restored.

After full acceptance, stop the baseline storage, point a stopped deployment
at that baseline pair using the saved config/images/secrets and fresh scratch,
start storage, rerun external preflight, then start the ordinary application.
Keep the previous pair and archive for rollback. Before any new writes,
returning to the stopped previous coherent pair is possible. After new uploads,
edits, views or deletions, returning to an old backup loses that delta: first
stop both new services and freeze their whole coherent pair, then repair/restore
it or use a separately verified reverse migration. Do not describe an old
snapshot switch after new writes as lossless rollback.

## Reproducible Linux drill

`tools/backup/check-drill` builds the separate tool and owned storage image,
then runs the real Docker test with the race detector. It uses random disposable
container/volume names, removes only those resources, and performs the actual
host `backup`, `verify` and fresh-volume `restore` commands. It does not prune
Docker or touch an existing library. The test checks full hashes and metadata,
not only counts. Its source is [drill_test.go](drill_test.go).

The fixture includes all five legacy kinds with retained IDs/NULL origin,
duplicate/case-sensitive Unicode tags and joins, max int64 100 ns ticks,
ready items whose session is gone, failed durable input, a 2001 idle receiving
session, a confirmed 5 MiB partial MPU, interrupted queued input with a fully verified unpublished candidate/expected
SHA-256 descriptor, hidden native
state, and independent ready/cancel cleanup. Baseline validation checks actual
SQLite and every full S3 GET/hash/HEAD/Range/Content-Type/metadata and private
access. Native ListParts survives restore with the same ID/part/ETag/size.
On a second fresh copy it recovers leases, resumes the partial MPU and verifies
fresh Complete/full incoming bytes before queue, runs actual FFmpeg/worker
publication and independent cleanup, retains failed input/lost-MPU state, and
proves those writes did not affect the stopped baseline or archive.

This is synthetic Linux arm64 evidence. It does not replace a full-library
100 GB rehearsal/RTO measurement, shipping Safari/File-reselection acceptance,
native linux/amd64 CI, macOS sleep/Docker Desktop restart, or physical
power-loss checks. Preserve those explicit release gates until actually run.
The separate [Linux maximum-browser evidence](../../docs/rewrite/BROWSER-MEMORY.md)
now records the valid 20 GiB upload/close/reselect/fingerprint/recovery run;
that result does not establish shipping Safari or real-library acceptance.
