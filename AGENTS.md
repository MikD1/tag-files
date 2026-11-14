# Tag Files rewrite

The complete specification is `docs/rewrite/README.md` and its linked documents.
Preserve F-01…F-38 under D-01…D-16. The accepted v27 UI is an immutable visual
reference, not a production backend. Do not change the reference artifacts.

Work locally on `main`, as explicitly requested. Do not publish or push.
Read `docs/rewrite/progress.md` and the implementation plan before continuing.
Keep progress grounded in current code and actual test output.

## Boundaries

- `app/`: new Go application; `web/`: new React SPA; `tools/`: external offline
  migration and cold backup/restore. Tools never become app CLI/API/UI features.
- `api/openapi.yaml` owns the HTTP contract. Goose SQL migrations own schema;
  sqlc owns static query generation. Do not hand edit generated code.
- Domain rules belong to their module, not independent transport/storage copies.
- Use real SQLite with the production driver, WAL, FULL, foreign keys and an
  immediate writer transaction. No S3/FFmpeg calls inside SQL transactions.
- All media and binary uploads stream through the same origin; bounded memory,
  immutable part bindings and verified object hashes are required.
- Uploads contain pending/error only, with no age based deletion. Ready/cancel
  remove the session atomically and keep independent cleanup work.
- Preserve legacy IDs, duplicate/case sensitive Unicode tag names and integer
  100 ns duration ticks. Never silently merge or discard source data.

Run the checks relevant to each change. Use `scripts/check` for the available
local suite; explicit integration gates must remain listed until actually run.
Do not claim release readiness from unit tests alone. Leave the legacy app in
place until replacement workflows and release containers have been verified.
