# Tag Files

A local photo and video library with tag search, collections, saved searches,
favorites, and a browser viewer. Resumable uploads, media processing, and
background cleanup run in a Go application; SQLite stores metadata and a private
SeaweedFS service stores media.

## Install and run

Install and start **Docker with Compose v2**. On macOS, use Docker Desktop; on
Linux, use Docker Engine with the Compose plugin or Docker Desktop. Supported
Docker platforms are Linux amd64 and arm64.

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

All library files live under **`$HOME/tag-files` on your host machine**, including
when using Docker Desktop:

| Host path | Contents |
| --- | --- |
| `$HOME/tag-files/data/metadata/` | SQLite database `library.sqlite`, its journal files, metadata, tags, collections, saved searches, and jobs |
| `$HOME/tag-files/data/media/` | Media originals, processed media, previews, and SeaweedFS storage metadata; this directory holds most of the library's bytes |
| `$HOME/tag-files/scratch/` | Disposable media-processing workspace |
| `$HOME/tag-files/secrets/` | Private storage credentials |
| `$HOME/tag-files/` | `compose.yml`, `.env`, and installation/update receipts |

The installer creates these private directories and configures both services to
write as their owner. Compose mounts them directly into the containers. The
database and media consume space on the disk containing this directory. To check
usage and available space:

```sh
du -sh "$HOME/tag-files/data" "$HOME/tag-files/scratch"
df -h "$HOME/tag-files"
```

To put everything on another disk, run the installer once with, for example,
`--directory /mnt/photos/tag-files`. All paths above then use that directory.
On Docker Desktop, allow that directory in its file-sharing settings if needed.
Media uses SeaweedFS's native storage format inside `data/media/`.

Back up **`data/`, `secrets/`, `compose.yml`, `.env`, and installation/update
receipts together**, using the documented
[cold backup and restore procedure](docs/operations.md#cold-backup-and-restore).
Both services must be stopped for a consistent backup. `scratch/` can be excluded.
Stopping or removing containers preserves these host directories.

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
