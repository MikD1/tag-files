# Application lifecycle

`Open` constructs the real application without accepting requests or claiming
work. It validates the media/storage profile, holds the library and scratch
locks, rejects overlapping canonical roots, verifies the durable storage pair,
binds every S3 consumer, preflights/recovers SQL jobs and then reclaims only
proven disposable scratch. Failed startup releases owners, preserves future
payloads and exposes no handler. An unsupported job prevents scratch reclamation.

`Serve` owns its accepted listener and starts exactly one serial runner with
ingest/poster/cleanup/delete handlers. The HTTP adapter registers each admitted
request. Shutdown closes admission, cancels request/worker contexts, shuts down
connections, joins the worker and every admitted handler, and finally closes
scratch and SQLite. A forced HTTP close alone does not prove handlers stopped;
ownership remains held until their callbacks actually return. Errors remain
visible to the caller. The library/scratch locks can then be reacquired.

`/health/live` reports the process. `/health/ready` checks running state, SQLite
and the authenticated current storage identity/epoch with a bounded deadline.
A storage outage makes readiness fail while SQL reads/liveness remain available.
`/api/v1/system/status` reports the app/schema/format/library and durable queue
counts. Unmeasured storage capacity is explicitly unknown; no S3 free-space
value is inferred from object listings or HEAD.

The development entry point is `go run ./cmd/tag-files`. `tag-files check` only
requests readiness and does not acquire a library owner. SIGINT/SIGTERM use the
same joined shutdown. Configuration:

| Variable | Default |
| --- | --- |
| `TAG_FILES_LISTEN` | `:8080` |
| `TAG_FILES_DATA_DIR` | `/data` |
| `TAG_FILES_SCRATCH_DIR` | `/scratch` |
| `TAG_FILES_S3_ENDPOINT` | `http://storage:8333` |
| `TAG_FILES_ACCESS_KEY_FILE` | `/run/secrets/s3_access_key` |
| `TAG_FILES_SECRET_KEY_FILE` | `/run/secrets/s3_secret_key` |
| `TAG_FILES_SCRATCH_MAX_BYTES` | 64 GiB |
| `TAG_FILES_MAX_OUTPUT_BYTES` | 20 GiB |

Use the owned storage image and matching private secret files, not a user's
existing MinIO/database. Secret reads are bounded, reject nonregular files and
control bytes, and never include contents in errors. Scratch keeps a physical
1 GiB reserve in this command. The roots must be separate. The existing
`media-runtime` image supplies the pinned tools; packaging this CGO server and
the completed React SPA into the final application image is still pending.

Tests use real SQLite and authenticated HTTP control for startup, foreign pair,
future codec, live scratch ownership, path aliases, failure cleanup and normal/
forced joined shutdown. The owned-image integration also uses actual FFmpeg,
SeaweedFS, an interrupted ingest, HTTP catalog/media/poster, paused storage,
the built server command, its health check and actual SIGTERM. The command
currently exposes the implemented catalog/organization/navigation/operations/
deletion/media/system APIs; upload HTTP, complete storage/metadata/scratch disk
admission and the SPA remain unfinished. It is not a release-ready product.
