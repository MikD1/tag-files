# Architecture

Tag Files is a local media library: a browser SPA, one Go application, and a
private object store, run by Docker Compose on the owner's machine. SQLite holds
metadata and durable work; SeaweedFS holds media bytes. The application serves
the SPA, JSON API, uploads and media from one origin and runs background jobs
and FFmpeg in the same process. Installation, backup and migration are separate
command-line tools.

## Diagrams

[C4 views](c4/README.md): system context, containers, application and storage
components, deployment. They are generated from the LikeC4 model in `c4/`.

## Main parts

| Part | Responsibility | Technology |
|---|---|---|
| Web UI (`web/`) | Library, viewer, uploads queue, tags, collections, categories; resumable uploads with client-side hashing | React, TypeScript, Vite, Mantine, React Router, TanStack Query |
| Application (`app/cmd/tag-files`) | SPA assets, JSON API, part uploads, media delivery, job worker, media processing | Go `net/http`, FFmpeg/FFprobe |
| Metadata (`data/metadata/`) | Catalog, organization, uploads, jobs, receipts, idempotency results | SQLite via `mattn/go-sqlite3` |
| Scratch (`scratch/`) | Disposable media-processing workspace | Host directory |
| Storage (`app/cmd/storage-run`) | Private S3 gateway in front of SeaweedFS; storage profile, identity and fencing | Go, SeaweedFS 4.53 `weed mini`, LevelDB2 |
| Installer and scripts (`scripts/`) | Fresh installation, deployment preparation, updates, release assets | POSIX sh, Python, Docker Compose |
| Cold backup tool (`tools/backup/`) | Backup, verify and restore of the stopped library pair | Go, Python host script |
| Migration tool (`tools/migrate/`) | One-time offline migration from the legacy C#/Angular installation | Go, POSIX sh |

## Key concepts

**Same origin, private storage.** Compose publishes only the application on
`127.0.0.1`. Storage sits on an internal network; storage credentials stay in the
backend. Browsers load media through `/media/{assetId}` application URLs, which
support GET, HEAD, byte ranges and conditional requests.

**Modular monolith.** Domain packages own catalog, organization (tags, saved
searches, collections), search, navigation, command receipts, ingestion,
posters, cleanup and deletion rules; `mediaserve` and `capacity` own media
delivery and admission. HTTP handlers adapt them to the API; `store` adapts
SQLite and `objectstore`/`objectio` adapt S3. `application` coordinates startup,
request admission, background work, pruning and shutdown in one process.

**SQLite consistency.** WAL, `synchronous=FULL`, foreign keys and a 5 s busy
timeout. A file lock (`.library.lock`) protects each library. One writer
connection uses immediate transactions; four read-only connections serve reads.
Embedded Goose SQL owns the schema; sqlc generates static queries. S3 and FFmpeg
calls run outside SQL transactions; short transactions commit their outcomes.
See the [data model](data-model.md).

**Durable jobs.** One queue consumer claims jobs with a 1 minute lease, renews it
every 10 s, and increments a lease generation on each claim so stale results are
rejected. Before serving, startup verifies the storage pairing, validates the
write-intent journal, requeues interrupted jobs and reclaims abandoned scratch;
the owning jobs later reconcile uncertain object writes.

**Storage identity and fencing.** The `storage-run` supervisor starts
`weed mini` on loopback, enforces global fsync, the private bucket and
public-access block, and checks the storage profile before opening the gateway
on port 8333. The gateway rejects requests whose expected storage, library or
volume identity differs. Each gateway start draws a new boot epoch; requests
bound to an earlier epoch are refused, so writes in flight across a storage
restart are reconciled, not replayed. The gateway hashes multipart parts as
they stream and keeps a durable proof journal for reconciliation. Besides S3
(AWS SDK v2), the application uses signed control endpoints on the gateway for
one-time library pairing and capacity.

**Uploads and media.** Browsers hash files and parts (SHA-256) in a Web Worker
and upload hash-bound parts through the application; durable jobs verify,
normalize and publish them. See the [media pipeline](media-pipeline.md).

**Resource bounds.** Each processing attempt reserves scratch space for source
and output. Capacity admission checks free bytes and inodes on the metadata,
scratch and storage volumes. Object writes (2 slots), media streams (16),
process output, FFmpeg threads and subprocess lifetimes are bounded. Scratch is
disposable; incoming bytes stay in object storage until publication and cleanup.

**HTTP contract.** `api/openapi.yaml` owns the contract and generates the Go
handlers and TypeScript types. PATCH and DELETE use `If-Match` version checks;
POST commands carry an `Idempotency-Key` with durable replayable results. Part
uploads and media are raw handlers outside JSON binding.

**Offline tools.** Migration and cold backup/restore live in `tools/`, outside
the application API and UI. See [operations](../operations/README.md).

## Contents

- [Data model](data-model.md)
- [Upload and media pipeline](media-pipeline.md)
- [Decisions](decisions.md)
