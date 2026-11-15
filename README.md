# Tag Files

A local photo and video library with tag search, collections, saved searches,
favorites, and a browser viewer. Resumable uploads, media processing, and
background cleanup run in a Go application; SQLite stores metadata and a private
SeaweedFS service stores media.

## Install and run

Install and start **Docker with Compose v2**. On macOS, use Docker Desktop; on
Linux, use Docker Engine with the Compose plugin or Docker Desktop. Supported
Docker platforms are Linux amd64 and arm64. No Git, Python, or source checkout is
needed.

Download **[setup.sh from the latest stable release](https://github.com/MikD1/tag-files/releases/latest/download/setup.sh)**
to your Downloads folder. In a terminal, run it once:

```sh
sh "$HOME/Downloads/setup.sh"
```

The installer pulls both ready-to-run images, creates `$HOME/tag-files` with the
Compose file, settings, and private credentials, and starts the service.
Open **http://localhost:8080**.

After installation, start and stop from that directory:

```sh
cd "$HOME/tag-files"
docker compose stop
docker compose up -d
```

Images and configuration are reused. To view status or logs, run
`docker compose ps` or `docker compose logs --tail 100` there. For a different
directory or port, pass `--directory PATH` or `--port PORT` to the installer;
`sh setup.sh --help` lists its options. Run the installer only for a fresh library.

## Where your data lives

With the default installation:

| Location | Contents |
| --- | --- |
| Docker volume `tag-files_app_data` | SQLite database `/data/library.sqlite`: media metadata, tags, collections, saved searches, and jobs |
| Docker volume `tag-files_storage_data` | Media originals, processed media, and previews, together with SeaweedFS storage metadata |
| Docker volume `tag-files_scratch` | Disposable media-processing workspace |
| `$HOME/tag-files` | `compose.yml`, `.env`, private `secrets/`, and the installation receipt; database and media bytes are in the volumes above |

Docker manages these volumes. On Linux, inspect their host paths with
`docker volume inspect tag-files_app_data tag-files_storage_data`. With Docker
Desktop, they live inside Docker's virtual machine disk and appear in its
**Volumes** view. Media is stored in SeaweedFS's native format, rather than as a
folder of individual photos and videos.

Stopping containers or running `docker compose down` preserves the volumes.
**`docker compose down --volumes` deletes the library.** Keep the database volume,
storage volume, and credentials together; use the documented
[cold backup procedure](docs/operations.md#cold-backup-and-restore) to back up a
library.

## Documentation

- [Architecture, data model, technologies, and media pipeline](docs/architecture.md)
- [Configuration, development, updates, and backup/restore](docs/operations.md)
- [Data migration](docs/migration.md)
- [HTTP API contract](api/openapi.yaml)
- [Agent guide](AGENTS.md)

## Development checks

Source builds and native checks are described in [operations](docs/operations.md#development-and-checks).
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
