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

`scripts/check-media` includes the native media/ingestion/worker host race suite.
The owned-storage test `TestIngestionWorkerOwnedImageProcessesPhotoAndRetainsFailedInput`
requires both `TAG_FILES_MEDIA_INTEGRATION=1` and `TAG_FILES_STORAGE_IMAGE` and
exercises real SDK MPU/complete, SQLite, FFmpeg, ready cleanup and failed-input
retention. The current disposable runtime image tests media/process/scratch;
the CGO SQLite worker is not yet packaged into a runnable server/SPA image.

Startup reset of interrupted jobs, safe stale-scratch reclamation, persistent
library/storage pairing, whole-volume upload admission/HTTP507, poster/deletion
handlers and application HTTP/shutdown wiring remain integration gates. Merely
opening a store must not be treated as completion of those recovery steps.
