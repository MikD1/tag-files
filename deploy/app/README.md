# Application image

`Dockerfile` provides the final `app` target: CGO Go/SQLite, production React SPA,
local assets and the pinned FFmpeg runtime. Node is only a build dependency.

```sh
docker build --target app -f deploy/app/Dockerfile -t tag-files-app:local .
scripts/check-app-image
```

Use [versioned installation/update](../INSTALL.md) and the new
[Compose](../compose.yml). App UID/GID10001 owns separate metadata and scratch
volumes; the root is read-only. Only app port8080 is published on loopback.
The storage service has no published port and stays on the internal network.
An additional app network permits host access to its published port.

`TAG_FILES_WEB_DIR` is set in the image. The development executable can omit it
to serve API/media while Vite proxies those routes. Static serving preserves
API/media/health protocols, serves immutable hashed assets and no-cache SPA deep
links, and refuses a missing build before accepting requests.

`release.published` still triggers GHCR publication, now of `-app` and `-storage`
multiarch images. The reusable verification workflow runs native amd64/arm64
jobs before publication. Workflow files are local; no GitHub run or publication
has been performed in this workspace.

The runtime foundation pins Ubuntu 26.04's multiarch index and FFmpeg 8.0.1's
Ubuntu package version. `scripts/check-media-image` builds the exact native test
image, captures its image ID, and runs media/process/scratch suites as UID/GID 10001 with
network disabled, a read-only root and a disposable bounded scratch tmpfs. It
mounts no user data and removes its test containers. No third delivery service is
introduced; the test stages belong to the eventual single application image.

The FFmpeg package enables GPL components including libx264. The final image
keeps Ubuntu notices at `/usr/share/doc` and records exact installed versions in
`/usr/share/tag-files/notices/runtime-packages.tsv`. That directory also contains
actual Go runtime module licenses/build metadata, the Go license and production
npm dependency integrity/license texts. Publication requests BuildKit SBOM and
provenance. Distribution/source-offer review and both architecture execution
remain release gates; local arm64 tests do not establish amd64 or Mac durability.
