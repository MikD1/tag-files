# Operations and development

## Deployment

Follow [the quick start](../README.md#install-and-run) to install with Docker and
the downloadable `setup.sh`. The script pulls versioned application/storage
images, checks their native Docker platform, and runs `storage-run setup` offline
inside the storage image to generate deployment files and random credentials.
It then starts Compose and waits for healthy services. Existing libraries and
Docker project volumes are refused.

The application image includes the web assets and media tools. The application
serves browser assets from its own origin. The repository's root `compose.yml`
is the single deployment template, also bundled in the storage image and attached
to stable releases. The installer prepares it with `.env` and credentials for
startup.

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
pair and credentials together. Treat native storage files as application-managed
data and use the cold backup tool with both services stopped to capture a
consistent copy. Removing containers preserves these host directories.

## Configuration

The installer writes exact image IDs, `TAG_FILES_PORT`, and the runtime UID/GID to
the deployment's `.env`. Secrets are individual files in its private `secrets/`
directory. Storage is reachable only on the internal Compose network. Compose
requires existing bind directories.

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

Compose supplies the standard paths. Define application environment variables in
Compose; use `.env` for values referenced by that configuration.
Keep sufficient physical disk space and inodes in both Docker and the host
filesystem.
Automated updates require configuration matching the installation receipt;
manually customized deployments need configuration review before updating.

`/health/live` reports process liveness; `/health/ready` reports admission readiness.
`/api/v1/system/status` reports the app/schema versions, queue, and capacity.

## Updates

The source-based maintenance scripts in this section and the cold backup section
require a matching source checkout and Python 3. They are separate from normal
installation and startup. Use `setup.sh` for fresh installations and
`scripts/update` for installed libraries.

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
installation's Compose mounts and runtime owner.

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
before selecting the verified pair for use. Preserve incomplete-restore markers
for diagnosis and use the restore tool to establish a consistent pair.
`tools/backup/check-drill` runs the isolated automated backup/restore integration
suite.

## Development and checks

For source builds, install Git and Python 3 as well as Docker, then run from the
repository root. Pick the version being built and an unused deployment directory
and project:

```sh
docker build -f deploy/app/Dockerfile --target app \
  --build-arg VERSION=v0.20.0 -t tag-files-app:local .
docker build -f deploy/storage/Dockerfile -t tag-files-storage:local .
sh scripts/setup.sh --version 0.20.0 \
  --app-image tag-files-app:local --storage-image tag-files-storage:local \
  --skip-pull --directory "$HOME/tag-files-dev" --project tag-files-dev --port 8081
```

To prepare a stopped deployment for an offline tool, use `scripts/install` with
the same image references and `--no-pull`. Add `--start` to start the services
after preparation. This advanced host tool requires Python 3.

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

After building the local app and storage images, rehearse the complete migration
and native rollback on disposable SQLite/MinIO data:

```sh
docker build --build-arg APP_IMAGE=tag-files-app:local \
  -f tools/migrate/Dockerfile -t tag-files-migrate:local .
TAG_FILES_MIGRATION_INTEGRATION=1 \
  TAG_FILES_MIGRATION_STORAGE_IMAGE=tag-files-storage:local \
  python3 -m unittest discover \
  -s scripts -p test_migrate.py -v
```

The test builds its pinned upstream MinIO fixture if needed. It uses a temporary
directory beside the checkout; set `TAG_FILES_MIGRATION_TEST_ROOT` to choose
another host-backed disk with sufficient migration capacity.

## Migration recovery

The [migration guide](migration.md) covers the ordinary transfer. Migration state
is retained beside the target in `TARGET_DIR.migration`: rollback data, a working
MinIO copy, source archives, configuration, reports, and the resumable journal.
The original installation and retained state are never removed automatically.

After an interruption, keep the source stopped and rerun the same command with
the same files and paths. Completed archives and copy checkpoints are reused;
an interrupted archive is retained and a new archive attempt starts. Changed
source bytes, operation settings, or unrelated target data cause a refusal.

If source issues block startup, read the retained manifest and reports. To accept
a supported loss explicitly, add an entry to the `Exceptions` array in
`TARGET_DIR.migration/config.json`, then rerun:

```json
{"issueId":"<manifest issue ID>","decision":"missing_media","reason":"<why this loss is accepted>","owner":"<person accepting it>"}
```

Use `missing_media` for `missing_primary`, `clear_reference` for
`dangling_collection`, and `archive_only` for supported malformed rows or
categories. Decisions are frozen when copying begins. Unknown schemas and probe
failures for existing image/video bytes require investigation.

To return to the original installation before accepting new writes, stop the new
services and start the old Compose deployment. After new writes, preserve the
complete new metadata/storage pair first: returning to the old snapshot loses
those changes. See [cold backup and restore](#cold-backup-and-restore).

`sh migrate.sh --help` lists options for the environment file, port, project, and
image overrides.

## Release distribution

Push a version tag to run native verification on amd64 and arm64, publish the
tested images, and create a GitHub release. For example, prepare `v0.21.0` on the
desired commit and send the branch and tag together:

```sh
git tag -a v0.21.0 -m "Tag Files v0.21.0"
git push --atomic origin main v0.21.0
```

The `Publish` workflow promotes the tested app, storage, and migration images to
`ghcr.io/mikd1/tag-files-app:0.21.0`, `ghcr.io/mikd1/tag-files-storage:0.21.0`, and
`ghcr.io/mikd1/tag-files-migrate:0.21.0`, and publishes the `0.21` and `latest`
tags. Native amd64 and arm64 checks include a disposable SQLite/MinIO migration
and rollback rehearsal before sealing the image identities. It creates the
release after image verification and publication, then attaches `setup.sh`,
`compose.yml`, `migrate.sh`, `migration.env`, and `SHA256SUMS`. The installer embeds the
release version and pins the pulled image IDs in the generated `.env`.
Subsequent `docker compose up -d` uses those images.

Publishing a release through GitHub's Releases page also runs this workflow.
Prerelease tags such as `v0.21.0-rc.1` publish their version-specific images and a
prerelease. Stable releases provide installer assets.

For the first publication, set all three GHCR packages' visibility to **Public** in
their package settings so users can pull the images anonymously. Publication
uses the workflow's `GITHUB_TOKEN` with `contents: write` and `packages: write`.

To prepare the downloadable files in a local directory:

```sh
scripts/release-images installer-assets --version v0.21.0 \
  --repository MikD1/tag-files --directory /path/to/new/release-assets
```
