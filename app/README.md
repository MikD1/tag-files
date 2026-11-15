# New Tag Files application

Implementation is in progress. Overall scope/evidence: [rewrite progress](../docs/rewrite/progress.md).
The legacy C#/Angular application remains in its original directories until the
replacement passes full acceptance. This module is not a releasable app yet.

Use Go 1.27.4 with CGO and a C compiler, plus Node 26 and npm. Runtime dependencies
and generators are separately pinned in `go.mod`, `../tools/codegen/go.mod` and
`../tools/codegen/package-lock.json`.

From the repository root:

```sh
npm ci --prefix tools/codegen
scripts/generate
scripts/check
```

`api/openapi.yaml` owns the full HTTP contract. Go JSON adapters currently expose
the catalog, organization, operations and navigation tags. A separately wired
media handler now streams GET/HEAD, byte ranges and conditional responses;
system health/status is wired, while upload adapters still require implementation. Binary handlers bypass
JSON binding. Generated Go and TypeScript files are checked
in and must be regenerated together through `scripts/generate`.

`scripts/check` verifies generation reproducibility, formatting, vet, ordinary
and race tests. Integration tests use real temporary SQLite libraries. They do
not access existing media, MinIO or user databases. `scripts/check-storage`
runs the optional isolated Docker S3 conformance suite; see the
[wire profile](internal/objectstore/README.md) and the separate
[storage preflight](internal/storageprofile/README.md). Final paired release
images, upload HTTP/batch/retry admission, full capacity/browser/tool and Mac acceptance
gates remain open. The development server is `go run ./cmd/tag-files`; its
startup/configuration and current scope are documented in
[application lifecycle](internal/application/README.md). It is not the completed
rewritten product or a release image.

To prepare module/toolchain caches for offline checks, run the above online
once. Go generators can then run with `GOPROXY=off` using the installed pinned
toolchain. Avoid exporting a GOROOT for a different Go installation.
