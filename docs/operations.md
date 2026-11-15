# Operations and development

## Deployment

Follow [the quick start](../README.md#install-and-run) to install with Docker and
the downloadable `setup.sh`. It needs no source checkout or Python. The script
pulls versioned application/storage images, checks their native Docker platform,
and runs `storage-run setup` offline inside the storage image to generate the
deployment files and random credentials. It then starts Compose and waits for
healthy services. Existing libraries and Docker project volumes are refused.

The application image includes the web assets and media tools; runtime browser
assets do not use a CDN. The repository's root `compose.yml` is the single
deployment template, also bundled in the storage image and attached to stable
releases. The installer prepares it with `.env` and credentials; a downloaded
Compose file alone needs those settings before startup.

Installation configuration and all library data live in `$HOME/tag-files` by
default. `data/metadata/library.sqlite` holds SQLite; `data/media/` holds complete
SeaweedFS native state and media; `scratch/` is disposable processing workspace.
Compose bind-mounts these host directories on Linux and Docker Desktop. The
installer creates them with private permissions and records their owner's
`TAG_FILES_UID`/`TAG_FILES_GID` for both container processes.
`--directory PATH` selects the root for configuration and all data, including when
the chosen path is on another disk. `--project NAME` selects container/network
names. See [host storage paths](../README.md#where-your-data-lives) for disk usage
commands and the backup set.

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

For everyday start and stop, the deployment directory and Docker are sufficient:

```sh
cd "$deployment"
docker compose stop
docker compose up -d
```

Compose starts storage before the application and waits for its health check.

The metadata and media directories share persistent library identities. Keep the
pair and credentials together. Do not edit native storage files or copy an active
SQLite/storage directory as a backup. Removing containers preserves these host
directories.

## Configuration

The installer writes exact image IDs, `TAG_FILES_PORT`, and the runtime UID/GID to
the deployment's `.env`. Secrets are individual files in its private `secrets/`
directory. Storage is reachable only on the internal Compose network. Compose
requires the bind directories to exist and does not silently create empty data
directories at a mistaken path.

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

The source-based maintenance scripts in this section and the cold backup section
require a matching source checkout and Python 3. They are separate from normal
installation and startup. Do not rerun `setup.sh` over an existing library.

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
inspect its receipt and container logs before recovery. Updates retain the
installation's Compose mounts and runtime owner. No data directories are deleted.

## Cold backup and restore

Build the backup image as above. For the quick-start project, stop both services
and create a generation outside the deployment directory and Docker's data root:

```sh
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

A complete generation contains native metadata/storage archives, deployment
configuration and secrets, a checked SQLite snapshot, and per-file hashes. It is
private and must be protected. A `.part` directory is incomplete. Retain the exact
recorded app/storage/tool images as well as the generation.

Restore into fresh host directories, with both services stopped. The output
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

The tool checks the archive and adjusts physical directory pairing on the
restored copies. Configuration is published after native restore succeeds;
incomplete operations retain their markers and staging directory for inspection.
The restored Compose file's relative paths select these exact data directories.
Use the recorded images, runtime owner, and restored secrets. Run Compose from
the restored directory after selecting it as the library to start. Start storage
first, run `storage-run check`, and validate object hashes/metadata and unfinished parts.
Exercise application recovery and browser workflows on a second restored copy
before selecting the verified pair for use. Never remove incomplete-restore
markers or manually rebind a mismatched pair. `tools/backup/check-drill` runs the
isolated automated backup/restore integration suite.

## Development and checks

For source builds, install Git and Python 3 as well as Docker, then run from the
repository root. Pick the version being built and an unused deployment directory
and project:

```sh
docker build -f deploy/app/Dockerfile --target app \
  --build-arg VERSION=v0.1.2 -t tag-files-app:local .
docker build -f deploy/storage/Dockerfile -t tag-files-storage:local .
sh scripts/setup.sh --version 0.1.2 \
  --app-image tag-files-app:local --storage-image tag-files-storage:local \
  --skip-pull --directory "$HOME/tag-files-dev" --project tag-files-dev --port 8081
```

For an offline tool that needs an initialized deployment without starting the
application, use `scripts/install` with the same image references, `--no-pull`,
and no `--start`. This advanced host tool requires Python 3.

Native checks require Go 1.27.4, a C compiler, Node.js 26, Python 3, and FFmpeg 8.
Install pinned web/generator dependencies as described in the root README.
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

## Release distribution

Publishing a stable GitHub release runs native verification on amd64 and arm64,
promotes the tested images to `ghcr.io/mikd1/tag-files-app:VERSION` and
`ghcr.io/mikd1/tag-files-storage:VERSION` (without the leading `v`), and attaches
`setup.sh`, `compose.yml`, and `SHA256SUMS` to the release. The installer embeds that
release version, pulls the matching images, and pins their exact local IDs in the
generated `.env`. Subsequent `docker compose up -d` uses those images.
Prereleases publish version-specific image tags without stable installer assets.

To prepare the downloadable files locally without publishing:

```sh
scripts/release-images installer-assets --version v0.1.2 \
  --repository MikD1/tag-files --directory /path/to/new/release-assets
```
