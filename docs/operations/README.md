# Operations

How a library is installed, configured, updated, backed up and released. The
deployment diagram is in [../architecture/c4/README.md](../architecture/c4/README.md).
Building from source and running checks: [development](development.md).

## Environments

| Environment | Where | How it is deployed |
|---|---|---|
| Local installation | Owner's machine with Docker (Linux amd64/arm64, or Docker Desktop); `http://localhost:8080`, loopback only | `setup.sh` from the latest stable release; updates with `scripts/update` |
| Release channel | GitHub Releases and `ghcr.io/mikd1/tag-files-{app,storage,migrate}` | `Publish` workflow on a `v*` tag |
| Development | Developer machine | Source builds and disposable libraries, see [development](development.md) |

## Deploy

### Fresh installation

Follow [the quick start](../../README.md#install-and-run). `setup.sh` checks that
the Docker daemon and the images are linux amd64 or arm64, pulls the versioned
app and storage images (unless `--skip-pull`), and runs `storage-run setup`
inside the storage image with `--network none` to generate the deployment files
and random credentials. It then runs `docker compose up -d --wait` (180 s
timeout). Options: `--directory`, `--port` (8080), `--project` (`tag-files`),
`--version`, `--app-image`, `--storage-image`, `--skip-pull`, `--help`.

The installer refuses a non-empty or symlinked directory, a Compose project that
already has containers or volumes, and the named volumes `<project>_app_data`,
`_storage_data` and `_scratch`. A failed setup leaves `.setup-incomplete` in the
directory; inspect it or choose a new directory before running again. If only the
Compose start failed, the configuration is kept and `docker compose up -d` retries.

The root `compose.yml` is the single deployment template. It is bundled in the
storage image and attached to stable releases. The app image contains the web
assets and FFmpeg/FFprobe.

### Layout

Configuration and all library data live in `$HOME/tag-files` by default
(`--directory PATH` selects another root, also on another disk):

- `data/metadata/library.sqlite` and its WAL files: SQLite metadata.
- `data/media/`: complete SeaweedFS native state and media.
- `scratch/`: disposable processing workspace.
- `secrets/`: storage credentials.
- `compose.yml`, `.env`, `installation.json` and `updates/`: configuration and receipts.

The installer creates `data`, `data/metadata`, `data/media`, `scratch` and
`secrets` with mode 0700, owned by the directory's owner, and records that owner
as `TAG_FILES_UID`/`TAG_FILES_GID` for both containers. Compose bind-mounts the
directories and never creates them. See
[host storage paths](../../README.md#where-your-data-lives) for disk usage.

The metadata and media directories share persistent library identities. Keep the
pair and the credentials together, and treat the native storage files as
application-managed data.

### Managing a deployment

Use the deployment directory for every Compose command:

```sh
deployment="$HOME/tag-files"
compose() {
  docker compose --project-directory "$deployment" --env-file "$deployment/.env" \
    -f "$deployment/compose.yml" "$@"
}
compose ps
compose logs --tail 100 app storage
```

For everyday start and stop, `cd "$deployment"` and run `docker compose stop` or
`docker compose up -d`. Compose starts storage first and waits for its health
check. Use `stop`, not `down`: backups and updates need the stopped containers.
Removing containers keeps the host directories.

### Updates

`scripts/update` upgrades an installed library. It needs a source checkout and
Python 3, separate from normal installation and startup. Build or load the
target app and storage images, and a backup-tool image built from the source of
the **currently installed** version: the backup tool refuses libraries with a
different schema version.

```sh
installed=../tag-files-installed
git worktree add "$installed" INSTALLED_TAG
docker build -f "$installed/tools/backup/Dockerfile" \
  -t tag-files-cold-backup:installed "$installed"
scripts/update --directory "$deployment" --version VERSION \
  --app-image APP_IMAGE --storage-image STORAGE_IMAGE --no-pull \
  --backup-tool-image tag-files-cold-backup:installed \
  --backup-destination "$HOME/tag-files-backups"
```

Replace the uppercase values, and keep a checkout of each installed version for
the next update's backup tool. The version must be strictly newer than the
installed one. Without `--no-pull`, images must be stable `:X.Y.Z` tags or
`@sha256:` digests. `--backup-generation` names the backup (default
`<UTC timestamp>-before-<version>`).

The script validates the images, stops both services, creates and verifies a
full cold backup, starts storage and runs its profile check, then starts the app.
It keeps the existing `compose.yml`, mounts and runtime owner; a newer release's
`compose.yml` is not applied. If startup fails it stops both services and does
not roll back to the old images. Receipts live in `updates/<timestamp>-<uuid>/`.
An incomplete operation or a failed installation receipt blocks further
automated updates; inspect the receipt and container logs before recovery.
Configuration files must still match the hashes in `installation.json`, and
`.env` may contain only the keys the installer writes; manually customized
deployments need review before updating.

`scripts/install` prepares a deployment from the checkout's `compose.yml` with the
given image references: configuration and directories, but no containers unless
`--start` is given. Its default port is 4200. It refuses directories inside the
checkout or Docker's data root.

### Releases

Push a version tag to run native verification on amd64 and arm64, publish the
tested images and create a GitHub release. Prepare `v0.21.0` on the desired
commit and push the branch and tag together:

```sh
git tag -a v0.21.0 -m "Tag Files v0.21.0"
git push --atomic origin main v0.21.0
```

The `Publish` workflow promotes the tested images to
`ghcr.io/mikd1/tag-files-app:0.21.0`, `ghcr.io/mikd1/tag-files-storage:0.21.0` and
`ghcr.io/mikd1/tag-files-migrate:0.21.0`. Every stable publication also moves the
`0.21` and `latest` tags, even for an older patch line. Native checks include a
disposable SQLite/MinIO migration and rollback rehearsal before the image
identities are sealed. After publication the workflow creates the release and
attaches `setup.sh`, `compose.yml`, `migrate.sh`, `migration.env` and
`SHA256SUMS`. The installer embeds the release version and pins the pulled image
IDs in `.env`.

Publishing a release on GitHub's Releases page also runs the workflow.
Prerelease tags such as `v0.21.0-rc.1` publish only version-specific images and a
prerelease without installer assets.

For the first publication, set the three GHCR packages to **Public** so images
can be pulled anonymously. The publish job uses `GITHUB_TOKEN` with
`contents: write` and `packages: write`. To prepare the installer assets locally
(`--repository` defaults to `$GITHUB_REPOSITORY`; the directory must not exist):

```sh
scripts/release-images installer-assets --version v0.21.0 \
  --repository MikD1/tag-files --directory /path/to/new/release-assets
```

## Configuration and secrets

The installer writes `.env` with `COMPOSE_PROJECT_NAME`, the exact
`TAG_FILES_APP_IMAGE` and `TAG_FILES_STORAGE_IMAGE` IDs, `TAG_FILES_PORT`,
`TAG_FILES_MIN_FREE_BYTES` and `TAG_FILES_UID`/`TAG_FILES_GID`. Compose publishes
the app only on `127.0.0.1:${TAG_FILES_PORT}`; storage is reachable only on the
internal Compose network.

Secrets (values never leave the deployment):

- `secrets/s3_access_key`, `secrets/s3_secret_key`: random storage credentials,
  mounted into both containers as Compose secrets.
- `data/media/config/s3.json`: the storage's copy of the app credential and a
  bootstrap admin credential.
- `TARGET_DIR.migration/` after a migration: copies of source and target
  credentials.

The application reads these environment variables:

| Variable | Default | Purpose |
| --- | --- | --- |
| `TAG_FILES_LISTEN` | `:8080` | HTTP listener inside the container |
| `TAG_FILES_DATA_DIR` | `/data` | Metadata directory |
| `TAG_FILES_SCRATCH_DIR` | `/scratch` | Processing workspace |
| `TAG_FILES_WEB_DIR` | unset: API only | Built SPA directory; the image and Compose set `/usr/share/tag-files/web` |
| `TAG_FILES_S3_ENDPOINT` | `http://storage:8333` | Private storage gateway |
| `TAG_FILES_ACCESS_KEY_FILE` | `/run/secrets/s3_access_key` | Access-key file |
| `TAG_FILES_SECRET_KEY_FILE` | `/run/secrets/s3_secret_key` | Secret-key file |
| `TAG_FILES_MIN_FREE_BYTES` | 1 GiB | Free-space reserve |
| `TAG_FILES_SCRATCH_MAX_BYTES` | 64 GiB | Scratch reservation limit |
| `TAG_FILES_MAX_OUTPUT_BYTES` | 20 GiB | Processed media size limit |

Compose sets `TAG_FILES_S3_ENDPOINT`, `TAG_FILES_WEB_DIR` and
`TAG_FILES_MIN_FREE_BYTES`; data, scratch, secret paths and the listener use the
binary defaults, which match the Compose mounts. Define application variables in
Compose and use `.env` only for values referenced there. Keep enough physical
disk space and inodes on the host filesystem and in Docker.

## Backups and restore

Backups are cold: both services stopped, made with `tools/backup/cold-backup`.
No backup-tool image is published. Use a source checkout at the installed
version, Python 3, and a tool image built from it (backup, verify and restore
all require the same schema version). The app and storage containers must exist
and be stopped. For a migrated installation, use its project's container names
instead of `tag-files-*-1`.

```sh
docker build -f tools/backup/Dockerfile -t tag-files-cold-backup:local .
compose stop app
compose stop storage
tools/backup/cold-backup backup --image tag-files-cold-backup:local \
  --app-container tag-files-app-1 --storage-container tag-files-storage-1 \
  --metadata-directory "$deployment/data/metadata" \
  --storage-directory "$deployment/data/media" \
  --deployment "$deployment" --destination "$HOME/tag-files-backups" \
  --generation library-001

tools/backup/cold-backup verify --image tag-files-cold-backup:local \
  --archive "$HOME/tag-files-backups/library-001"
```

Keep the destination outside the deployment directory and Docker's data root.
A complete generation contains native metadata and storage archives, deployment
configuration and secrets, a checked SQLite snapshot, per-file hashes and a
`complete` marker; `backup` verifies it before publishing. A `.part` directory is
incomplete. The generation is private. Keep the exact recorded app, storage and
tool images with it.

Restore into fresh host directories with both services stopped. The output
directory may contain only the empty data directories created below:

```sh
restored="$HOME/tag-files-restored"
mkdir -m 700 "$restored" "$restored/data" \
  "$restored/data/metadata" "$restored/data/media"
tools/backup/cold-backup restore --image tag-files-cold-backup:local \
  --app-container tag-files-app-1 --storage-container tag-files-storage-1 \
  --metadata-directory "$restored/data/metadata" \
  --storage-directory "$restored/data/media" \
  --archive "$HOME/tag-files-backups/library-001" \
  --deployment-output "$restored"
mkdir -m 700 "$restored/scratch"
```

The tool verifies the archive, rebases the physical directory pairing on the
restored copies and publishes configuration only after the native restore
succeeds. A failed restore keeps `.cold-restore-incomplete` markers and a
`.<output>.restore-*` staging directory for inspection. The recorded images must
be present locally.

The restored `compose.yml` uses relative paths, so it selects these exact data
directories. Its `.env` keeps the original project name and port: stop the
original deployment before starting the restored one. Start storage first and
check it:

```sh
deployment="$restored"
compose up -d --wait storage
compose exec -T storage storage-run check
```

Then validate object hashes, metadata and unfinished parts. Exercise application
recovery and browser workflows on a second restored copy before using the
verified pair. `tools/backup/check-drill` runs the isolated backup/restore
integration suite.

## Monitoring and logs

- `compose ps` and `compose logs --tail 100 app storage` in the deployment.
- `/health/live`: 200 while the process runs.
- `/health/ready`: 200 only when the app is serving and SQLite and storage answer
  within 3 s; otherwise 503. `tag-files check` uses it as the app health check;
  `storage-run check` is the storage health check.
- `/api/v1/system/status`: app, schema and format versions, library ID, job queue
  counts and disk capacity for metadata, scratch and storage.
- Update receipts: `updates/<timestamp>-<uuid>/operation.json`.

## Runbooks

- [Migrate a legacy installation](migration.md), including interruption and
  recovery.
- Failed setup: see [fresh installation](#fresh-installation).
- Failed or blocked update: see [updates](#updates); restore from the backup the
  update made if needed.
