# Operations and development

## Deployment

Follow [the quick start](../README.md) to build and install. Host scripts require
Python 3 and native Linux Docker images. The application image includes all web
assets and media tools; runtime browser assets do not use a CDN.

Use the external deployment directory for every Compose command:

```sh
deployment="$HOME/tag-files"
compose() {
  docker compose --project-directory "$deployment" --env-file "$deployment/.env" \
    -f "$deployment/compose.yml" "$@"
}
compose ps
compose logs --tail 100 app storage
```

To stop, run `compose stop app` followed by `compose stop storage`. To start:

```sh
compose up -d --pull never --wait storage
compose exec -T storage storage-run check
compose up -d --pull never --wait app
```

`app_data` contains SQLite; `storage_data` contains the complete SeaweedFS native
state and write-proof journal; `scratch` contains disposable processing attempts.
The metadata and storage volumes share persistent library identities. Keep the
pair and credentials together. Do not edit native storage files or copy an active
SQLite/storage volume as a backup.

## Configuration

The installer writes exact image IDs and `TAG_FILES_PORT` to the deployment's
`.env`. Secrets are individual files in its private `secrets/` directory. Storage
is reachable only on the internal Compose network.

The application accepts these environment variables:

| Variable | Default | Purpose |
| --- | --- | --- |
| `TAG_FILES_LISTEN` | `:8080` | HTTP listener inside the container |
| `TAG_FILES_DATA_DIR` | `/data` | Metadata directory |
| `TAG_FILES_SCRATCH_DIR` | `/scratch` | Processing workspace |
| `TAG_FILES_WEB_DIR` | Image-configured path | Built SPA directory |
| `TAG_FILES_S3_ENDPOINT` | `http://storage:8333` | Private storage gateway |
| `TAG_FILES_ACCESS_KEY_FILE` | `/run/secrets/s3_access_key` | Access-key file |
| `TAG_FILES_SECRET_KEY_FILE` | `/run/secrets/s3_secret_key` | Secret-key file |
| `TAG_FILES_MIN_FREE_BYTES` | 1 GiB | Free-space reserve |
| `TAG_FILES_SCRATCH_MAX_BYTES` | 64 GiB | Scratch reservation limit |
| `TAG_FILES_MAX_OUTPUT_BYTES` | 20 GiB | Processed media size limit |

Compose supplies the standard paths. Adjusting app environment requires an
explicit Compose configuration change; `.env` values alone do not automatically
become container environment variables. Keep sufficient physical disk space and
inodes in both Docker and the host filesystem.
Automated updates require configuration matching the installation receipt;
manually customized deployments need configuration review before updating.

`/health/live` reports process liveness; `/health/ready` reports admission readiness.
`/api/v1/system/status` reports the app/schema versions, queue, and capacity.

## Updates

Build or load the requested application/storage images and a backup-tool image
that supports the installed library schema. Choose a strictly increasing stable
version and retain its matching source checkout.

```sh
docker build -f tools/backup/Dockerfile -t tag-files-cold-backup:local .
scripts/update --directory "$deployment" --version VERSION \
  --app-image APP_IMAGE --storage-image STORAGE_IMAGE --no-pull \
  --backup-tool-image tag-files-cold-backup:local \
  --backup-destination "$HOME/tag-files-backups"
```

Replace the uppercase values with the intended version/image references.
The script validates images, stops the pair, creates and verifies a full cold
backup, starts storage with its profile check, and starts the app. Receipts live
under `updates/`. An incomplete operation blocks another automated update;
inspect its receipt and container logs before recovery. No volumes are deleted.

## Cold backup and restore

Build the backup image as above. For the quick-start project, stop both services
and create a generation outside the deployment directory and Docker's data root:

```sh
compose stop app
compose stop storage
tools/backup/cold-backup backup --image tag-files-cold-backup:local \
  --app-container tag-files-app-1 --storage-container tag-files-storage-1 \
  --metadata-volume tag-files_app_data --storage-volume tag-files_storage_data \
  --deployment "$deployment" --destination "$HOME/tag-files-backups" \
  --generation library-001

tools/backup/cold-backup verify --image tag-files-cold-backup:local \
  --archive "$HOME/tag-files-backups/library-001"
```

A complete generation contains native metadata/storage archives, deployment
configuration and secrets, a checked SQLite snapshot, and per-file hashes. It is
private and must be protected. A `.part` directory is incomplete. Retain the exact
recorded app/storage/tool images as well as the generation.

Restore into fresh volumes and an empty configuration directory, with both
services stopped:

```sh
docker volume create tag-files-restored-app
docker volume create tag-files-restored-storage
mkdir -m 700 "$HOME/tag-files-restored"
tools/backup/cold-backup restore --image tag-files-cold-backup:local \
  --app-container tag-files-app-1 --storage-container tag-files-storage-1 \
  --metadata-volume tag-files-restored-app \
  --storage-volume tag-files-restored-storage \
  --archive "$HOME/tag-files-backups/library-001" \
  --deployment-output "$HOME/tag-files-restored"
```

The tool checks the archive and adjusts physical volume pairing on the restored
copies. Before startup, configure Compose to mount these exact restored volumes,
use the recorded images and restored secrets, and allocate fresh scratch. The
archived Compose file retains its original volume names. Start storage first,
run `storage-run check`, and validate object hashes/metadata and unfinished parts.
Exercise application recovery and browser workflows on a second restored copy
before selecting the verified pair for use. Never remove incomplete-restore
markers or manually rebind a mismatched pair. `tools/backup/check-drill` runs the
isolated automated backup/restore integration suite.

## Development and checks

Frontend development uses `npm --prefix web run dev` at `http://127.0.0.1:5173`.
Vite proxies API/media to `http://127.0.0.1:8080`; set `TAG_FILES_API_ORIGIN` to
change that target. API/schema generators live in `tools/codegen`.

| Command | Coverage |
| --- | --- |
| `scripts/generate` | sqlc and OpenAPI Go/TypeScript generation |
| `scripts/check` | Generated output, Go vet/tests/race, offline tools, web checks |
| `scripts/check-web` | TypeScript, ESLint, unit tests, production web build |
| `npm --prefix web run test:browser` | Chromium/WebKit against HTTP and SQLite |
| `scripts/check-media` | Host FFmpeg and worker/media integration |
| `scripts/check-media-image` | Pinned media runtime and process/scratch checks |
| `scripts/check-storage-image` | Owned storage and runtime integration |
| `scripts/check-app-image` | Actual image upload, media, and restart workflows |

Install browser runtimes with the command in the root README. Integration
harnesses create isolated libraries and may retain ignored failure reports.
