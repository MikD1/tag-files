# Development and checks

## Requirements

Native checks require Go 1.27.4, a C compiler (CGO for SQLite), Node.js 26,
Python 3 and FFmpeg 8.0.1 (`scripts/check-media` requires exactly
`ffmpeg version 8.0.1-*`). Docker is required for storage, image and
integration checks. Install the pinned dependencies:

```sh
npm ci --prefix web
npm ci --prefix tools/codegen
(cd web && npx playwright install --with-deps chromium webkit)
```

## Source builds

For a disposable deployment from source, also install Git. Pick the version
being built and an unused directory and project:

```sh
docker build -f deploy/app/Dockerfile --target app \
  --build-arg VERSION=v0.20.0 -t tag-files-app:local .
docker build -f deploy/storage/Dockerfile -t tag-files-storage:local .
sh scripts/setup.sh --version 0.20.0 \
  --app-image tag-files-app:local --storage-image tag-files-storage:local \
  --skip-pull --directory "$HOME/tag-files-dev" --project tag-files-dev --port 8081
```

`scripts/install` prepares a deployment from the checkout instead; see
[updates](README.md#updates).

Frontend development uses `npm --prefix web run dev` at `http://127.0.0.1:5173`.
Vite proxies `/api` and `/media` to `http://127.0.0.1:8080`; set
`TAG_FILES_API_ORIGIN` to change the target. API and schema generators live in
`tools/codegen`.

## Checks

| Command | Coverage |
| --- | --- |
| `scripts/generate` | sqlc and OpenAPI Go/TypeScript generation |
| `scripts/check` | Generated output, gofmt, Go vet/tests/race for `app`, race tests for `tools/migrate` and `tools/backup`, Python tests for `tools/backup` and `scripts/test_*.py` (integration cases skip without their flags), web checks |
| `scripts/check-web` | TypeScript, ESLint, unit tests, production web build |
| `npm --prefix web run test:browser` | Chromium/WebKit against a Go HTTP/SQLite fixture; needs Go and free ports 18080 and 15173 |
| `scripts/check-media` | Host FFmpeg and worker/media integration |
| `scripts/check-media-image` | Pinned media runtime and process/scratch checks |
| `scripts/check-storage` | Storage integration against the pinned `chrislusf/seaweedfs:4.53` image, which must already be pulled |
| `scripts/check-storage-image` | Owned storage image and runtime integration |
| `scripts/check-app-image` | Actual image upload, media and restart workflows; reads `TAG_FILES_APP_TEST_IMAGE`, `TAG_FILES_STORAGE_TEST_IMAGE`, `TAG_FILES_SKIP_IMAGE_BUILD`, `TAG_FILES_VERSION`. Its default storage tag `tag-files-storage:local-check` is expected by other image checks |
| `scripts/check-offline-images` | Offline image checks; reads `TAG_FILES_OFFLINE_APP_IMAGE`, `TAG_FILES_OFFLINE_STORAGE_IMAGE` |
| `scripts/check-amd64` | amd64 checks (`--skip-build`, `--artifacts`); arm64 hosts need the qemu-x86_64 binfmt handler |
| `scripts/check-large-upload` | 20 GiB upload (50-minute timeout) |
| `scripts/check-browser-large`, `scripts/check-browser-performance` | Large and performance browser runs; require `TAG_FILES_BROWSER_APP_IMAGE`, `TAG_FILES_BROWSER_STORAGE_IMAGE` |
| `scripts/check-performance` | Performance checks |
| `scripts/check-migration-large` | Large migration run |
| `tools/backup/check-drill` | Isolated backup/restore integration suite |

CI also runs the Docker integration suites `TAG_FILES_DEPLOYMENT_INTEGRATION=1`
for `scripts/test_install_update.py` and `scripts/test_host_data.py`, and
`TAG_FILES_MIGRATE_INTEGRATION=1 TAG_FILES_MIGRATE_STORAGE_IMAGE=…` for
`go test` in `tools/migrate`. Integration harnesses create isolated libraries
and may retain ignored failure reports.

## Migration rehearsal

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
another host-backed disk with enough capacity. `TAG_FILES_MIGRATION_APP_IMAGE`,
`TAG_FILES_MIGRATION_IMAGE` and `TAG_FILES_MIGRATION_MINIO_IMAGE` override the
images; `TAG_FILES_MIGRATION_KEEP_FAILURES` keeps failed runs.
