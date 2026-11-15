# Tag Files

A local photo and video library with tag search, collections, saved searches,
favorites, and a browser viewer. Resumable uploads, media processing, and
background cleanup run in a Go application; SQLite stores metadata and a private
SeaweedFS service stores media.

## Run locally

Install Docker with Compose v2 and Python 3. Run these commands from the repository
root; Docker builds include the frontend, Go application, and FFmpeg.

```sh
docker build -f deploy/app/Dockerfile --target app \
  --build-arg VERSION=v0.1.2 -t tag-files-app:local .
docker build -f deploy/storage/Dockerfile -t tag-files-storage:local .

scripts/install \
  --directory "$HOME/tag-files" \
  --version 0.1.2 \
  --app-image tag-files-app:local \
  --storage-image tag-files-storage:local \
  --project tag-files --port 8080 --no-pull --start
```

Open **http://localhost:8080**. Choose an empty deployment directory and an unused
Compose project for a fresh installation. The installer generates private
credentials and pins both images. The images must match the Docker host's native
architecture.

The deployment directory contains `compose.yml`, `.env`, secrets, and installation
receipts. Named volumes retain metadata, object storage, and processing scratch.
Only the application port is published, on loopback. Keep the metadata and storage
volumes together; `docker compose down --volumes` deletes library data.

To inspect or stop this installation:

```sh
docker compose --project-directory "$HOME/tag-files" \
  --env-file "$HOME/tag-files/.env" -f "$HOME/tag-files/compose.yml" ps

docker compose --project-directory "$HOME/tag-files" \
  --env-file "$HOME/tag-files/.env" -f "$HOME/tag-files/compose.yml" stop
```

## Documentation

- [Architecture, data model, technologies, and media pipeline](docs/architecture.md)
- [Configuration, development, updates, and backup/restore](docs/operations.md)
- [Data migration](docs/migration.md)
- [HTTP API contract](api/openapi.yaml)
- [Agent guide](AGENTS.md)

## Development checks

Native checks require Go 1.27.4, a C compiler, Node.js 26, Python 3, and FFmpeg 8.
Docker is required for storage and container integration checks.

```sh
npm ci --prefix web
npm ci --prefix tools/codegen
scripts/check

# Browser tests use a disposable HTTP/SQLite fixture.
(cd web && npx playwright install --with-deps chromium webkit)
npm --prefix web run test:browser
```

The project is [MIT licensed](LICENSE). Distribution notices and required source
archives are kept alongside their container build inputs.
