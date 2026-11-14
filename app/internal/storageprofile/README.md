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
