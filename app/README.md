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
the catalog, organization, operations and navigation tags; the remaining upload,
media and system routes are described for subsequent implementation. Binary
handlers will bypass JSON binding. Generated Go and TypeScript files are checked
in and must be regenerated together through `scripts/generate`.

`scripts/check` verifies generation reproducibility, formatting, vet, ordinary
and race tests. Integration tests use real temporary SQLite libraries. They do
not access existing media, MinIO or user databases. Storage/FFmpeg/browser/tool
and Mac acceptance suites are still to be implemented and run.

To prepare module/toolchain caches for offline checks, run the above online
once. Go generators can then run with `GOPROXY=off` using the installed pinned
toolchain. Avoid exporting a GOROOT for a different Go installation.
