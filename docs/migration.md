# Transfer a C#/Angular library

The separate offline tool transfers SQLite metadata and MinIO objects into the
Go application with SQLite and SeaweedFS. It preserves IDs, duplicate tag names
and exact duration ticks, and copies media without converting it.

1. Stop the old application and all source writers. Make and verify a consistent
   cold backup of its SQLite directory, including WAL/SHM/journal files, complete
   MinIO storage and configuration/secrets. Keep this rollback copy. Serve frozen
   objects through MinIO and keep the source unchanged throughout migration.
2. Build both local images using [installation](../README.md), then prepare the
   deployment below. **Do not start the target application** before migration.
   Use a fresh target with empty `app_data` and `storage_data` volumes.
3. Give the tool read-only source credentials able to list buckets, versions,
   metadata and tags and read all objects. Include every visible source bucket.
   Credential files must be private regular files (`chmod 600`), each containing
   one value of at most 4096 bytes. Keep values out of command arguments and Git.
4. Allow disk space for source, archive, target, journal, working space and rollback.
   Measure target write peaks and metadata space in a representative pilot;
   replace the example budget below with those measurements.

Run commands from the repository root. Put frozen SQLite in `/srv/source` and
source credential files in `/srv/migration-secrets`. Create `/srv/migration/archive`
and `/srv/migration/journal`; leave the archive's `run-001` directory absent.
The source, archive, target metadata and journal directories must be disjoint.

```sh
deployment="$HOME/tag-files"
scripts/install --directory "$deployment" --version 0.1.2 --app-image tag-files-app:local --storage-image tag-files-storage:local --project tag-files --no-pull
compose() { docker compose --project-directory "$deployment" --env-file "$deployment/.env" -f "$deployment/compose.yml" "$@"; }
install -d -m 700 /srv/migration-target-secrets
install -m 600 "$deployment/secrets/s3_access_key" /srv/migration-target-secrets/s3_access_key
install -m 600 "$deployment/secrets/s3_secret_key" /srv/migration-target-secrets/s3_secret_key
docker build --build-arg APP_IMAGE=tag-files-app:local -f tools/migrate/Dockerfile -t tag-files-migrate:local .
compose up -d --wait storage
docker volume create tag-files_app_data
docker network connect --alias frozen-minio tag-files_library SOURCE_MINIO_CONTAINER
```

Replace `SOURCE_MINIO_CONTAINER` with the frozen source MinIO container name.
This connects it to the private target network without publishing storage ports.
Keep the application stopped; `copy` creates and pairs SQLite with storage.
The private credential copies are for the transfer tool; keep the deployment's
original secret files unchanged.

Save `/srv/migration/config.json`, adapting source paths, buckets and measurements:

```json
{
  "Source": {
    "Endpoint": "http://frozen-minio:9000",
    "Region": "us-east-1",
    "Buckets": ["library", "temporary", "thumbnail"],
    "AccessKeyFile": "/source-secrets/source_access",
    "SecretKeyFile": "/source-secrets/source_secret"
  },
  "Database": "/source/app.sqlite",
  "Archive": "/work/archive/run-001",
  "SnapshotID": "frozen-source-001",
  "RollbackReference": "verified cold source backup at /srv/rollback/run-001",
  "RollbackVerified": true,
  "Target": {
    "Endpoint": "http://storage:8333",
    "Region": "us-east-1",
    "AccessKeyFile": "/target-secrets/s3_access_key",
    "SecretKeyFile": "/target-secrets/s3_secret_key"
  },
  "TargetMetadata": "/target",
  "Journal": "/work/journal/migration.sqlite",
  "Budget": {"writePeakBytes": 2147483648, "metadataBytes": 1073741824, "reserveBytes": 0},
  "Exceptions": [],
  "FFprobe": "ffprobe"
}
```

Set `RollbackVerified` only after checking the backup. Zero target reserve selects
max(10 GB, 10% of planned target bytes); the archive keeps 10 GB by default.
Check physical disks and Docker disk limits separately.

Define this shell helper for the stopped target's named volume and private network:

```sh
migration() {
  docker run --rm --network tag-files_library \
    --mount type=volume,src=tag-files_app_data,dst=/target \
    --mount type=bind,src=/srv/source,dst=/source,readonly \
    --mount type=bind,src=/srv/migration,dst=/work \
    --mount type=bind,src=/srv/migration-secrets,dst=/source-secrets,readonly \
    --mount type=bind,src=/srv/migration-target-secrets,dst=/target-secrets,readonly \
    tag-files-migrate:local "$@" -config /work/config.json
}
migration scan
migration probe > /srv/migration/probe.json
```

Review `/srv/migration/archive/run-001/manifest.json`, its blocking issues and the
probe report. The archive retains raw SQLite rows and all inventoried objects,
including orphans and historical versions. An interrupted scan needs a new archive
path. Never edit a completed archive. Probe failures need review; hashes alone do
not establish playable media.

Resolve blockers before copying. Owner decisions go in `Exceptions` as
`{"issueId":"<manifest issue ID>","decision":"archive_only","reason":"...","owner":"..."}`.
Use `missing_media` for `missing_primary`, `clear_reference` for
`dangling_collection`, and `archive_only` for other supported malformed rows or
categories. Unsupported schemas require a matching reviewed tool. Freeze exceptions
when copying begins; accepted losses remain in the verification report.

```sh
migration copy
migration verify > /srv/migration/verify.json
```

Resume an interrupted copy with the same `migration copy` command, archive, target,
journal and configuration, then verify again. Stop on changed source, hash
mismatches, insufficient space or unresolved ambiguity. Verification rehashes
objects and checks metadata, joins and category results; require `complete: true`
and review exceptions and per-item provenance. An immutable report is also saved
beside the journal. The tool cannot recover absent originals or execute old jobs.

The tool writes as root. Give the imported metadata to the application user:

```sh
docker run --rm --network none --user 0:0 --entrypoint chown \
  --mount type=volume,src=tag-files_app_data,dst=/target \
  tag-files-app:local -R 10001:10001 /target
```

Stop target storage with `compose stop storage`. Keep this verified pair
frozen while testing startup, browsing, search, favorites and video seeking on a
disposable copy; see [backup and restore](operations.md). When switching to the
verified original pair, run `compose up -d --wait`. Keep its named volumes
and credentials together.

Keep the source backup, archive, journal, configuration and secrets for rollback.
Before new writes, stop the new services and return to the frozen source deployment
if needed. After new writes, reverting the source snapshot loses that delta;
preserve the complete new SQLite/storage pair before recovery.
