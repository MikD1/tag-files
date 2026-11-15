# Storage preflight

`cmd/storage-check` is a separate binary for the storage container. It runs no
application server, opens no library database and never repairs configuration.
The app container will not mount the storage filesystem or Docker socket.

The current check validates the owned pinned LevelDB2 file, metadata paths
inside the complete `/data` volume, non-anonymous S3 identity configuration,
live persisted version-2 filer rules with explicit root fsync and inheritance
without fsync=false/TTL/read-only/WORM overrides. Owned identity JSON permits
only known fields, and every identity must explicitly grant Read/Write/List
globally or for `tag-files`. Write covers deletion/cleanup in this S3 profile.

Anonymous/signed bucket HEAD probes are combined with read-only access metadata:
all four S3 public-access-block settings enabled, no bucket policies or lifecycle
rules, and a private owner-only bucket ACL. The public-access block also ignores
historical per-object public ACLs; its behavior is checked on the real server.
The selected control credential must be able to read these access settings.

Configuration files must be regular and at most 1MiB. Control
output is bounded (1MiB stdout/64KiB stderr) and execution has a deadline.
The bounded writer does not expose a promoted ReadFrom fast path that could
bypass its limit. Each S3 access document is bounded to 1MiB and exactly one XML
document.

`scripts/check-storage` builds the static Linux binary and runs it inside the
pinned real container, including restart and negative configurations. The
integration fixture is disposable. It deliberately edits its own test rules and
files to prove refusal; the checker itself has no write path.

The owned production image and resumable bootstrap live in
[`deploy/storage`](../../../deploy/storage/README.md). Its supervisor keeps S3
disabled until persisted fsync has been checked, then provisions the private
bucket and publishes a durable ready marker. `scripts/check-storage-image`
exercises that actual built image independently of the older wire fixture.
Application upload admission and root Compose wiring remain open. The older
fixture provisions its bucket before fsync and is only wire/checker evidence.
These checks do not prove physical power-loss durability or the final cold
backup/restore contract.

Storage ownership format2 now binds one logical library UUID and records a
physical root fingerprint. Format1 upgrades retain logical UUID/configuration;
a copied format2 marker on another root is refused. The private authenticated
control endpoint publishes bounded identity/epoch data and permits only one
idempotent durable binding. S3 library/root guards run before forwarding, while
ordinary unbound S3 calls remain available for isolated conformance/offline work.
Application bootstrap details and the required cold-restore fingerprint rebase
are documented in [librarybinding](../librarybinding/README.md). The restore tooling and
actual application admission wiring remain open.
## Durable multipart checksums

The owned gateway compensates for SeaweedFS4.53's missing ListParts SHA256 with
an independent proof journal at `/data/.tag-files-mpu-proofs`. It authenticates
the original pinned SDK SigV4 request before binding a part, durably records its
immutable tuple/size/SHA, then hashes and counts the actual streamed body. Only
matching actual bytes and native checksum/ETag acknowledgement can publish a
validated proof. No part payload is buffered in a file or retained in memory.

ListParts preserves authoritative native selectors/paging and supplies SHA only
for an exact proven match. Pending or uncertain replacement writes block old
proofs even when ETags remain equal. A later ACK does not settle an earlier
unknown writer; the new-backend barrier invalidates uncertain historical proofs
and requires a fresh actual-byte resend. Exact native `400 BadDigest` is the
explicit no-write response; generic errors remain uncertain.

Bindings/proofs and their directory ancestry are synced before forwarding,
acknowledgement or listing, including retry after a failed parent-directory
sync. Per-MPU terminal admission closes durably before draining writers.
Cleanup requires quiescence and authoritative native absence; uncertain state
waits for the next backend epoch. It has no age-based deletion. Full external
cold backup must retain this journal with the rest of `/data`.

Missing, corrupt, foreign or unversioned proof metadata is rejected without
inventing a checksum or deleting native parts. Application bindings and opaque
ETags are not substitutes. The direct raw-backend conformance suite intentionally
observes missing SHA; production ingestion requires the owned gateway's proof.

