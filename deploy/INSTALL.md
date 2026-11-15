# Versioned local installation and updates

Use the new [compose.yml](compose.yml) through the external host scripts.
Root Compose mirrors this new deployment; the old configurations remain as
`compose-legacy.yml` and `compose-dev-legacy.yml` for separate legacy rollback.
These commands do not migrate a legacy library or publish any images.
Python 3, Docker Compose v2 and a retained versioned source/release checkout are
required on the host; Python is not added to the application image.

Both scripts acquire one exclusive POSIX host lock for the canonical deployment
path before reading installed versions or state, and retain it through image
resolution, backup, service startup and final receipts. A competing install or
update fails immediately; retry after the owner finishes so validation reads
the current installed version. This also covers publication of a fresh install
directory, which otherwise could race another installer.

The empty private lock file is a sibling named
`.<deployment-directory-name>.install-update.lock`; it stays outside the
atomically renamed deployment directory and is opened relative to its parent
directory descriptor without following symlinks. Symlink/nonregular/hard-linked
or nonprivate lock entries and parent-path replacement are refused. Keep the
same inode: do not remove or replace the file to force ownership. The kernel
releases ownership when its process exits; the remaining empty file does not
mean an operation is still running. A durable incomplete operation receipt
continues to require diagnosis after a crash even though the host lock is free.

## Prepare a fresh installation

Build/load the exact app and owned storage images, then choose a new external
deployment directory and unused Compose project. For local image testing:

```sh
docker build -f deploy/app/Dockerfile --target app -t tag-files-app:local .
docker build -f deploy/storage/Dockerfile -t tag-files-storage:local .

scripts/install \
  --directory "$HOME/tag-files-deployment" \
  --version v0.1.0 \
  --app-image tag-files-app:local \
  --storage-image tag-files-storage:local \
  --project tag-files \
  --port 4200 \
  --no-pull
```

Without `--no-pull`, provide the published stable version tags or registry
SHA-256 digest references. The script pulls and inspects both images before
preparing anything; `latest` is refused. With `--no-pull`, local tags or
preloaded image IDs are accepted. Either way, `.env` pins exact local SHA-256
image content IDs and `installation.json` records requested refs, available
registry digests, architecture, version and configuration hashes. Both images
must match the native Linux architecture reported by the Docker daemon, including
when using `--no-pull`. Update checks the separate backup-tool image too, before
stopping either service or changing configuration. An inspectable foreign image
does not establish that QEMU is installed; these scripts require native images.
Stable `major.minor.patch` versions
with an optional `v` prefix are supported; prerelease version ordering is not
inferred.

Preparation requires an absent or empty directory outside the checkout and
Docker's native data root. It refuses existing containers or data volumes for
the selected project. It never attaches an existing library through `install`.
The resulting private directory contains:

```text
compose.yml
.env
installation.json
secrets/s3_access_key
secrets/s3_secret_key
```

The deployment and secrets directories have mode 0700. The two independent
credentials are generated with the operating system's cryptographic randomness;
files have mode 0444 so app UID10001 can read individual read-only secret binds.
Other host users cannot traverse the private parent directories. Secret values
are not printed in reports, command arguments or `.env`. Do not make the parent
directories public. Restored deployment secrets use the same permission model.
This avoids Linux UID changes on macOS host files; actual Docker Desktop/Mac
acceptance remains a separate gate.

Preparation validates Compose but leaves services stopped. Add `--start` to
create the new empty pair and start storage, run the full external
`storage-run check`, and then start the app with health waiting. A failed
first start retains `install-failure.json` and all newly created resources for
diagnosis. Before Docker startup begins, `installation.json` is durably marked
`starting`; normal failure records `failed`, while abrupt interruption leaves
`starting`. Both states block automated update until the installation has been
examined, including any Docker child that was still running when the host
process stopped. It does not delete volumes.

To start a prepared installation manually:

```sh
cd "$HOME/tag-files-deployment"
docker compose --env-file .env up -d --pull never --wait storage
docker compose --env-file .env exec -T storage storage-run check
docker compose --env-file .env up -d --pull never --wait app
```

The new app is available at `http://127.0.0.1:4200` in this example; SPA, API,
uploads and media share that origin. Only the app port is published. Storage
stays on an internal Docker network. The default minimum free-space reserve is
1 GiB. Named volumes contain app metadata, the complete storage `/data`, and
separate disposable scratch. Never copy SQLite or SeaweedFS native files out of
an active volume through host filesystem internals.

## Update with a cold backup

Retain the current deployment directory and the separately built offline backup
tool image. A complete cold backup immediately before starting new images is
mandatory in this workflow. Choose an external backup directory outside the
deployment and Docker data root/disk image, with space for a full generation and
its verification workspace. The owner chooses carrier, frequency and retention.

```sh
docker build -f tools/backup/Dockerfile -t tag-files-cold-backup:local .

scripts/update \
  --directory "$HOME/tag-files-deployment" \
  --version v0.2.0 \
  --app-image tag-files-app:v0.2.0 \
  --storage-image tag-files-storage:v0.2.0 \
  --backup-tool-image tag-files-cold-backup:local \
  --backup-destination /Volumes/Backup/tag-files \
  --backup-generation before-v0.2.0 \
  --no-pull
```

Use actual versioned refs or loaded local images. Remove `--no-pull` when
using published version tags/digests; the backup tool image must already exist
locally. All candidate images are pulled/resolved and the new release Compose
configuration is validated before any current service stops. Unavailable tags,
platform mismatch, a non-increasing version, edited tracked deployment files,
or an unfinished earlier update cause refusal. The script does not silently
reinterpret a downgrade as an update.

The update then:

1. Saves the previous `.env`, Compose file and installation record under a
   private `updates/<operation>/before/` directory.
2. Stops app completely, then storage. The independent cold-backup host script
   verifies stopped containers/all protected mounts and writer locks, archives
   the full current pair plus deployment/config/secrets, verifies every file,
   and publishes a complete generation. The app is never used as a backup CLI.
3. Activates the prepared exact image IDs and Compose file while both services
   are stopped. It starts storage, checks the persisted global fsync/private
   native profile externally, then starts the app and waits for health.
4. Records the completed version and configuration hashes. Credentials,
   project identity and named data volumes are retained.

`updates/<operation>/operation.json` records images, phase, backup identities
and outcome. Keep this receipt with the complete generation. If backup fails,
current configuration remains intact and both services stay stopped. If new
startup fails, the script stops the pair and preserves candidate config, the
previous config, receipt/error and archive. It never deletes evidence,
automatically starts old binaries, rewrites a database to an older schema, or
pretends that reverting an image tag rolls back application writes.

An incomplete update blocks another automated update. Inspect the recorded
phase, current container state and logs before deciding the next action. The
previous configuration is evidence, not an instruction to restart old images.
If a new app has started, it may already have applied supported Goose schema
migrations or accepted worker writes. Runtime rejects unsupported future
schemas without rewriting them; the host script does not bypass that check.

## Restore and rollback

Follow the standalone [cold restore runbook](../tools/backup/README.md) into two
new empty named volumes, with both services stopped. Use the exact saved image
versions/config/secrets, recreate scratch, validate the restored storage
profile and every object/MPU read before app startup. Run browser/recovery work
on a second disposable copy; keep the original verified restored pair as the
baseline. Do not clear an incomplete restore marker or manually rebind either
half of a mismatched pair.

Before new writes, a stopped previous coherent pair can provide a rollback
point. After any new uploads, edits, views or deletions, an old snapshot omits
that delta. First stop and freeze the complete current pair; prefer repair or
restore of the new library, or a separately verified reverse migration.
Switching old images/config against newly migrated metadata is not a safe
schema rollback. Retain the legacy source and its archives until real
replacement acceptance; installation scripts do not remove them.

## Verification scope

Run the isolated host orchestration tests with:

```sh
python3 -m unittest discover -s scripts -p test_install_update.py
```

For actual local images, the optional disposable Docker test installs a random
project, writes a Unicode tag through HTTP, performs a cold-backed update,
checks preserved tag ID/value/credentials and re-verifies the complete archive:

```sh
TAG_FILES_DEPLOYMENT_INTEGRATION=1 \
TAG_FILES_APP_IMAGE=tag-files-app:local \
TAG_FILES_STORAGE_IMAGE=tag-files-storage:local-check \
TAG_FILES_BACKUP_IMAGE=tag-files-cold-backup:local-drill \
python3 -m unittest discover -s scripts -p test_install_update.py -v
```

The test selects only its own random project/volumes for removal. The installed
and updated app image may have identical content IDs in this orchestration
smoke test; it does not prove a future schema migration, release-pull behavior,
or a supported downgrade. Keep separate release gates for full-library
migration/restore, linux/amd64 execution, Apple Silicon Docker Desktop, real
Safari/Chrome/VoiceOver, Mac sleep/restart and physical power loss.

Local Linux arm64 evidence on 2026-11-15: the complete command above passed all
eight tests (seven isolated failures/safety cases plus the real Docker test) in
40.953s. The real install/update/backup sequence measured 39.357s. The random
project and its three named volumes were removed afterward. This used the
local app/storage images and the separately verified cold-backup image; it is
synthetic orchestration evidence under the scope limits above.

The host-ownership regression suite additionally reproduces overlapping
v1.1/v1.2 updates and overlapping installs, rejects a busy operation before any
installed-state read, refuses unsafe lock entries and a replaced lock parent,
and kills a separate lock-owning process to verify kernel release plus durable
incomplete-receipt refusal. A targeted real Docker install/cold-backup/update
run after the ownership fix passed in 40.804s (39.293s for its real sequence).

## Promotion of the exact verified release images

`release.published` invokes the reusable native Linux amd64/arm64 verification
matrix with the **release tag as `VERSION`**. Each runner exports one Docker
archive per image, loads that archive, and exercises the loaded application and
storage images through the release browser/upload/restart check. Publication
has no image build step. After all verification gates pass, the runner seals a
receipt containing the release version, architecture, source commit, workflow
run/attempt, complete archive SHA256, original image config digest and ordered
uncompressed filesystem-layer hashes. At sealing, it re-exports the recorded immutable tested image through a unique
temporary tag and compares its complete original config digest and every
ordered layer hash with the source archive. This covers Cmd, Entrypoint, User,
Env and all other config bytes. It checks the exact aliases consumed by runtime
tests before and after export; a changed alias fails sealing. Engine image IDs
may be manifest/index IDs and are never treated as config digests. Proof archives
live beside the source archives and are removed with their private tags on
success or rejection.

The publication job downloads both sealed artifacts from the same workflow
run/attempt and validates **both** bundles before loading or pushing anything.
It loads the saved images, pushes architecture-specific staging references and
checks each registry manifest's config digest against the tested config. Only
after all four manifests pass does it assemble app/storage multiarch channel
tags. Stable `v1.2.3` produces `1.2.3`, `1.2` and `latest`; a prerelease such as
`v1.2.3-rc.1` produces only `1.2.3-rc.1`. A disagreement between the SemVer
prerelease suffix and GitHub's prerelease flag fails validation. Build metadata
suffixes and invalid Docker/SemVer tags are refused.

The artifacts are named by run ID, run attempt and architecture and are retained
for seven days. A partial retry that cannot retrieve both current-attempt
artifacts fails closed; rerun the complete workflow rather than rebuilding in
publication. Version/architecture/commit/run mismatch, changed archive bytes,
config/layer mismatch and unsealed receipts all fail. Partial publication keeps
an incremental `publication.json` with staging refs, verified config/digests,
channel progress and errors; the workflow retains it for thirty days. Updating
tags across separate registry packages is not transactional. A failed run does
not automatically rebuild, roll back a channel or delete the staging evidence.

For portable [Docker archive/load transport](https://docs.docker.com/reference/cli/docker/image/save/),
the build explicitly disables OCI provenance and SBOM attestations. Docker's
[classic image store does not preserve those attestations](https://docs.docker.com/build/metadata/attestations/).
The embedded Go/web license notices, build information and runtime package list
remain available in the image; they are **not OCI SBOM/provenance attestations**.
Archive transport and registry compression can change outer manifest/index
identities. The preserved release identity is the exact original config plus
ordered decoded layer bytes; no compilation, dependency installation or web
build runs during publication.

Local focused verification on 2026-11-15:

```sh
python3 -m unittest discover -s scripts -p test_release_images.py -v
# 17 passed, including corrupted archive/receipt, wrong version/architecture/
# source/run, changed runtime config with the same rootfs/version/recorded ID,
# aliases changed during export, registry mismatch and no-rebuild promotion.
env -u GOROOT go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.9 \
  .github/workflows/verify.yml .github/workflows/publish.yml
# exit 0
```

A real save→load→save roundtrip on the existing dedicated amd64 check images
also passed without rebuilding, executing architecture-specific binaries or
publishing. The archives lived on the host-backed project filesystem, and the
complete configs and every decoded layer SHA256 matched after re-export:

| Image | Original/preserved config digest | Layers | Initial archive SHA256 |
| --- | --- | ---: | --- |
| App | `sha256:51fd8338a0eaacb901aa2a56f0753a2d3a797ba55a1946f099d3d1641dbfb89f` | 13 | `387a4fbf70c79163aa541142c4cdbf5228c9f4025403ae42639dea9e4de599e4` |
| Storage | `sha256:93d85f420e27a974534ce22a436c7fb01d98d0d5ce64d09addd55fbb050ed205` | 15 | `080729eeecc46c67015bdd17f002d65cdab26a6b59eeadd0acd621d1bf4feef9` |

Docker29's containerd store saved gzip layer blobs; the archive verifier decoded
and hashed them against the original diffIDs. Unit tests also cover classic
plain `layer.tar` archives. The local report is
`/tmp/tag-files-release-roundtrip-evidence.json`; disposable aliases/archives
were removed. Hosted native CI, cross-host image-store transport and GHCR
publication have **not** been executed from this local session. Their runtime
results remain open; local structural/transport checks do not imply them.

A subsequent exact-config sealing review reproduced and fixed the gap where
rootfs/version and a recorded engine ID could conceal a different Cmd or
Entrypoint. RED cases for Cmd, Entrypoint, User and Env all passed incorrectly
before the fix; all are refused by the full exported-config comparison now.
The focused suite also accepts exact configs when the engine ID differs from
the config digest and rejects an alias replaced during export.

The actual seal export/hash stage then passed on both existing dedicated amd64
images in 21.824 s, after real save/load, without rebuilding, executing the
foreign architecture or publishing. Its config digests and ordered 13/15 layer
hashes matched the table above; an intentionally wrong expected config digest
was refused for each image. These older local images retain their differing
base-image version/revision labels, so this real check exercises the exact
export/config/layer stage, while the common release-version bundle orchestration
is covered by the isolated tests. Report:
`/tmp/tag-files-release-seal-evidence.json`. Temporary proof tags, fixture aliases
and all host-backed archive bytes were removed after positive and negative
checks. Hosted release pipeline execution remains open.
