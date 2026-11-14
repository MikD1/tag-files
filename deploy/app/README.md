# Application runtime foundation

`Dockerfile` currently provides `media-runtime` and `media-check` stages. The
final app binary/SPA/CGO builder and release integration remain open. Do not
publish this test image as a runnable rewritten application.

The runtime foundation pins Ubuntu 26.04's multiarch index and FFmpeg 8.0.1's
Ubuntu package version. `scripts/check-media-image` builds the exact native test
image, captures its image ID, and runs media/process/scratch suites as UID/GID 10001 with
network disabled, a read-only root and a disposable bounded scratch tmpfs. It
mounts no user data and removes its test containers. No third delivery service is
introduced; the test stages belong to the eventual single application image.

The FFmpeg package enables GPL components including libx264. Distribution notices,
full dependency/license inventory, transitive package/digest capture, final app
image checks and both architecture execution remain release gates. This native
arm64 fixture is not evidence of amd64 execution or physical Mac durability.
