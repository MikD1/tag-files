# Durable worker execution

`Runner` owns one serial consumer. SQLite persists claims, lease generations,
attempts, retry times and failures. Wake channels contain no authoritative jobs.
Claims filter registered kinds so an unfinished handler is never substituted for
real processing. `Run`/`RunOne` on the same runner cannot overlap. Application
startup must construct exactly one runner for its exclusively owned library.

Defaults: a 60s lease, 10s heartbeat and 1s idle poll. A handler completes by
committing/removing its job; returning nil with a retained job is an error.
Shutdown cancels the handler, waits for it and the heartbeat goroutine, then
requeues the interrupted attempt with a higher generation without charging a
retry. Every publication/failure/release still checks the actual lease.

The current retry interpretation is three automatic retries after initial
execution, with persisted 5/30/120s delays; the fourth failed execution stays
failed. This resolves the specification's three-delay/three-attempt ambiguity
in favor of applying all named delays. Invalid media/codec/hash/limits are
permanent failures. Cleanup receipts keep retrying with the delay capped at 120 s;
their unresolved writers must not be abandoned because a counter was exhausted.

`Ingestion` streams and verifies incoming bytes into private scratch, performs
real media processing, and uses the existing candidate/verified/ready protocol.
Session and job failures are atomic; manual retry resets attempts and fences the
old generation while preserving input/MPU/bindings. Future payload versions are
retained and refused. `Cleanup` executes the independent receipt protocol;
ready/cancel continue to remove visible uploads without a history or TTL.

`Poster` independently reads a verified ready primary, prepares a bounded JPEG,
and publishes a unique verified candidate without recreating uploads. Failure
only updates the poster job. `Cleanup` dispatches strict receipt codecs to their
resource module, including item-owned `poster_candidate` work without uploadID.
Every poster candidate has its cleanup receipt before PUT; publication removes
its own receipt while attaching the poster/version in the same SQL transaction.

`Deletion` hides the item and cancels publication through the domain command,
then processes paged owned primary/poster/incoming resources. It waits for
independent ready/orphan receipts and uncertain writers, records deletion epochs
before I/O, and retains minimal key inventory for verification after storage
restart even when item metadata is gone. Its retries are not exhausted; an
awaited restart is diagnosed as `deletion_awaiting_storage_restart`.

`scripts/check-media` includes the native media/ingestion/worker host race suite.
The owned-storage test `TestIngestionWorkerOwnedImageProcessesPhotoAndRetainsFailedInput`
requires both `TAG_FILES_MEDIA_INTEGRATION=1` and `TAG_FILES_STORAGE_IMAGE` and
exercises real SDK MPU/complete, SQLite, FFmpeg, poster publication, ready cleanup
and failed-input retention. Native poster faults additionally cover lost PUT,
same-volume restart, source-only recovery after scratch loss and orphan cleanup.
The current disposable runtime image tests media/process/scratch;
the CGO SQLite worker is not yet packaged into a runnable server/SPA image.

Startup SQL recovery is now available through `ingestion.RecoverInterrupted`.
It requires a stored pair and preflights every job/manifest/cleanup/poster payload
and external-write codec in pages. Running generations are fenced/requeued even
if the wall clock moved back; queued jobs become available, retry waits are
bounded by the shared delay policy, and failed jobs remain. No journal, source,
candidate or receipt is retired. Recovery is sealed after success or first claim.
Ready primary/poster work never recreates an upload session.

Scratch has guarded, bounded reclamation after producer quiescence. Whole-volume
upload admission/HTTP507 and application startup/HTTP/shutdown
wiring remain integration gates. Persistent pairing primitives are in `librarybinding`; merely
opening a store must not be treated as completion of the startup sequence.
