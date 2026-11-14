# New Tag Files application

Implementation is in progress. Overall scope/evidence: [rewrite progress](../docs/rewrite/progress.md).
The legacy C#/Angular application remains in its original directories until the
replacement passes full acceptance. This module is not a releasable app yet.

Use Go 1.27.4 with CGO and a C compiler. Runtime dependencies and generators are
separately pinned in `go.mod` and `../tools/codegen/go.mod`.

From the repository root:

```sh
scripts/generate
scripts/check
```

`scripts/check` verifies generation reproducibility, formatting, vet, ordinary
and race tests. Integration tests use real temporary SQLite libraries. They do
not access existing media, MinIO or user databases. Storage/FFmpeg/browser/tool
and Mac acceptance suites are still to be implemented and run.

To prepare module/toolchain caches for offline checks, run the above online
once. Go generators can then run with `GOPROXY=off` using the installed pinned
toolchain. Avoid exporting a GOROOT for a different Go installation.
