# Decisions

Newest first. Only decisions that are costly to reverse or would surprise a newcomer. Replaced decisions are marked "Superseded", not deleted.

## 2026-11-16: Library data lives in user-owned host directories
In the context of a single-user library on the owner's machine, facing Docker volumes that hide the library from the host and make its size, location and backup unclear, we decided to bind-mount `data/metadata`, `data/media` and `scratch` from the deployment directory and run both services as the host owner's recorded UID/GID, to achieve visible, user-owned files that can be placed on any disk, accepting that the installer must create these directories in advance (Compose never creates them) and that native storage files must still be treated as application-managed.

## 2026-11-14: Backup, restore and migration are external offline tools
In the context of a service that owns all library files, facing rarely used, data-critical operations that need a stopped, consistent library, we decided to implement migration, cold backup/restore and updates as separate command-line tools outside the application API, UI and scheduler, to keep the running service small and the copies consistent, accepting downtime for backups and updates and that backup schedules are left to the owner.

## 2026-11-14: SeaweedFS `weed mini` behind an owned private gateway
In the context of hundreds of GB of media kept as a single verified working copy, facing the need for durable writes, resumable multipart uploads, ranges and checksums in one ARM64/AMD64 container without a separate metadata database, we decided on SeaweedFS 4.53 `weed mini` with embedded LevelDB2 and a global fsync rule, wrapped by an owned gateway that checks storage identity, fences restarts with a boot epoch and records multipart part proofs, to achieve private, verifiable object storage, accepting more internal components, reconciling deletes through durable receipts, and cold backups that must capture SeaweedFS metadata and volumes together.

## 2026-11-14: Go modular monolith with a React SPA
In the context of rewriting the C#/Angular version, with development done by AI agents, facing work dominated by HTTP/S3 streaming, short SQLite transactions, FFmpeg and durable state reconciliation, we decided on one Go `net/http` process with OpenAPI-generated handlers, sqlc, Goose and an in-process job worker, plus a React/Mantine SPA, to keep a small framework, explicit SQL and fixed module contracts, accepting more hand-written SQL and lifecycle code, a CGO SQLite driver and Go's short support window.

## 2026-11-14: SQLite with one immediate writer holds metadata and durable jobs
In the context of one user and thousands of items, facing the need for crash-safe metadata and jobs without a database server or queue, we decided on SQLite (WAL, `synchronous=FULL`, foreign keys) with one immediate-transaction writer, jobs with leases and generation fences in the same database, and S3/FFmpeg calls kept outside transactions, to achieve durable state in one file paired with the media, accepting serialized writes and one application process per library (file lock).

## 2026-11-14: Only processed working copies are kept
In the context of a minimal library managed only by Tag Files, facing the cost of storing originals next to browser-ready copies, we decided to keep one verified working copy per item at the source resolution (compatible images unchanged, other images as PNG, video as H.264 MP4) plus a poster, and to delete the uploaded input after publication, to keep storage simple and every item playable in the browser, accepting that originals of converted uploads are not recoverable from the library.

## 2026-11-14: Item deletion is permanent
In the context of a single-user library, we decided that deleting an item removes it and its media for good, with no trash, restore period or undo, to keep the model simple, accepting that mistakes can only be recovered from a cold backup.

## 2026-11-14: Tags are identified by ID; names are case sensitive and may repeat
In the context of migrating legacy tags as they are, facing names that repeat or differ only by case, we decided to keep tag names byte-exact without uniqueness or normalization, identify tags by integer ID in links and API operations, and bind saved-search terms to tag IDs, to achieve lossless migration and categories that survive renames, accepting that a search term matches every tag with that name.

## 2026-11-14: Durations are nullable integer 100 ns ticks
In the context of migrating legacy durations stored as .NET `TimeSpan` values, we decided to store durations as nullable integer 100 ns ticks, to migrate them exactly without rounding, accepting that the public API converts them to floating-point `durationSeconds`.
