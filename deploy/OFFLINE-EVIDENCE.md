# Isolated-network image rehearsal

Passed on 2026-11-15 against the final already-loaded Linux arm64 images. This
checks the delivery requirement that the built application can perform its
tested workflows without an external network dependency. It does not publish,
pull, rebuild or retag either image, and does not use an existing library.

| Role | Exact tested image |
| --- | --- |
| App | `sha256:ef3cc12a4a58ecd6e36a6689210a6e33f3879d2571507ec1b82d4956f09cf3d9` |
| Storage | `sha256:70aa8659fd1622ffce9c39a65024c83b3619c248eb8a71c55ccc6af6f0e17ea5` |

These images were built from `60f35ca2e3c9ac002e75be3d5f0e27ddfd4db70e`, version
`v0.1.0`. Their [sealed archive receipt](LOCAL-EVIDENCE.md) is unchanged. The test
checkout was `5b5d8a546830adbd9cd207ba2c6412fd435ca154` with this test harness
uncommitted. Production app/SPA sources, dependencies and deployment files did
not change for this check.

## Reproduce on the Linux Docker host

```sh
TAG_FILES_OFFLINE_APP_IMAGE=sha256:ef3cc12a4a58ecd6e36a6689210a6e33f3879d2571507ec1b82d4956f09cf3d9 \
TAG_FILES_OFFLINE_STORAGE_IMAGE=sha256:70aa8659fd1622ffce9c39a65024c83b3619c248eb8a71c55ccc6af6f0e17ea5 \
scripts/check-offline-images
```

The harness uses a private copy of the production Compose configuration with
three fresh named volumes and random credentials. Both app frontend and storage
library networks are **internal**, IPv4 only. Neither container has a published
port or a default route. The harness asserts the actual immutable image IDs,
network membership, Docker network flags, routes and port bindings before use.

Native host browsers reach the app through a host-only `127.0.0.1` forwarder to
its private bridge IP; the forwarder streams through 64 KiB buffers. This keeps
the browser's normal trusted localhost origin while the services have no route
outside their two owned networks. This fixture requires the Linux host of the
Docker daemon; it is not a Docker Desktop host-port implementation or a Mac
acceptance script. The production launcher remains unchanged.

## Checks and observed results

- Both expanded release browser cases passed: Chromium 6.4 s, WebKit 2.9 s,
  runner 10.1 s. Real hash worker/Blob XHR/S3 publication, image decoding,
  HEAD/Range, empty ready queue and metadata editing/pending controls ran.
  Chromium lost one accepted part response and reconciled without resending it.
- Browser-context request listeners rejected any origin other than the app
  origin. No external page/worker dependency was observed on these paths.
- Both complete PNG primaries equalled the original 128×96 fixture bytes,
  SHA256 `060a2853ddd984094ed5ee791d8c4633b10a0933f07be02313d3e48097fe8269`.
  App and storage restarted on the same isolated volumes/images. The whole
  catalog and both complete primary hashes matched afterward.
- Fresh Chromium and WebKit instances then opened both canonical `/content/:id`
  links, executed the embedded SPA and decoded the correct retained 128×96
  image. API item IDs/content URLs and decoded image URLs matched; screenshots
  and `viewer.json` retain this evidence. Both browser reports have zero page/
  HTTP errors and external requests. This checks actual viewer rendering beyond
  merely receiving SPA fallback HTML.
- Readiness returned HTTP 200 with its valid empty body. The owned project's
  containers, networks, three volumes and credential files were removed.

Raw evidence remains under
`/home/mik.guest/projects/tag-files/.performance.offline-6ku1gcru/`: copied Compose
configuration, startup/browser/restart/readiness/viewer/runtime logs,
`report.json`, `viewer.json`, four viewer screenshots and `cleanup.json`.
The latter records no remaining container, network or volume for project
`tag-files-offline-3de1c0151156`; an independent Docker inspection confirmed it.
Output is outside ordinary Playwright `test-results`.

Earlier harness attempts failed because a private-IP HTTP origin lacked secure
browser APIs, and a readiness assertion incorrectly required a nonempty body.
Those fixtures were cleaned and provide no passing result. The revised cleanup
also guarantees teardown after a diagnostic log failure. Independent review
closed those findings and checked the final runtime evidence.

This is a two-PNG Linux offline check. It does not cover all application paths,
offline installation of missing images/toolchains, real Safari/Chrome codecs,
the owner's Mac or library, native amd64 CI, registry pulls or publication.
Those requirements retain their separate acceptance evidence and gates.
