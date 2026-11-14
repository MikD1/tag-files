# Owned storage image

Build from the repository root:

```sh
docker build -f deploy/storage/Dockerfile -t tag-files-storage:local .
scripts/check-storage-image
```

Both Go 1.27.4 and SeaweedFS 4.53 base images are pinned by their multi-platform
index digests in the Dockerfile. The Go supervisor and diagnostic checker are
static binaries. The image runs `weed mini` with embedded LevelDB2 and keeps
the entire `/data` tree on one persistent volume, including master/raft state,
filer metadata, data/index files, multipart state and owned configuration.

The container requires two regular secret files, `/run/secrets/s3_access_key`
and `/run/secrets/s3_secret_key`. Supply independent random static values of
20–256 bytes; a trailing newline is accepted. Never publish port 8333 or the
backend control ports on the host. The eventual application Compose setup will
mount only its app credentials, use the private S3 network, and depend on storage
health. Root Compose still runs the legacy system until that replacement is
complete. Only the disposable integration harness publishes an ephemeral
loopback S3 port.

On first startup, `storage-run` takes an OS lock and saves an `initializing`
ownership intent before starting the backend. It refuses arbitrary nonempty
volumes. The intent, storage UUID, independently generated bootstrap identity,
application identity and configuration hashes survive interrupted startup.
The app identity has Read/Write/List on `tag-files`; bootstrap credentials remain
inside storage. Secret/config mismatch and unknown formats fail closed.

The first backend process has S3 disabled and no automatic buckets. Only a new
owned installation with an empty ruleset may initialize the global fsync rule.
After reading that persisted rule back, the supervisor stops/reaps the process
and reopens the backend. It verifies the rule again before creating the private
bucket and enabling all four public-access-block settings. It checks the full
read-only profile before atomically publishing `ready`. Every backend interface,
including S3 on 8334, binds loopback. Only after the full preflight does the Go
supervisor open the network-facing streaming S3 gateway on 8333. The gateway
preserves the signed Host, raw bytes, Range and conditional responses, and does
not decompress, buffer whole bodies or replay writes. An existing app reconnecting
during storage restart therefore cannot reach the provisioning backend.
Iceberg/Lance endpoints,
WebDAV, automatic bucket creation, telemetry and the admin UI are disabled.
Volume capacity uses mini's free-space-based automatic limit.

Every restart follows the same S3-disabled preflight. Existing unsafe rules,
public access settings, bucket policies, lifecycle rules, changed secrets or
incompatible state are refused; startup does not silently repair them. The
health command `storage-run check` requires the durable ready marker and matching
config hash, then runs the read-only profile checker. `storage-check` remains a
separate diagnostic binary without that publication-marker prerequisite.

SIGTERM/SIGINT drains the owned backend process group before releasing the
volume lock. A bounded grace period escalates to SIGKILL and waits for reaping.
An interrupted initialization resumes with the same storage identity; completed
objects and confirmed partial MPU parts are verified after a container
SIGKILL/restart in the image tests, including completion/full hash and Range/
conditional responses through the gateway.

For cold backup/restore, stop both application and storage and preserve the whole
volume, including dotfiles and `config/s3.json`, plus the external app secret
files and exact image digests. The separate backup/restore tooling and drills
remain a later rewrite task. These container tests do not establish physical
power-loss durability or macOS/Docker Desktop acceptance.
