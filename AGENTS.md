# Tag Files

A local media library: Go HTTP application, React SPA, SQLite metadata, private
SeaweedFS object storage, and FFmpeg processing. Read [README.md](README.md),
[docs/product](docs/product/README.md) for user-facing rules, and
[docs/architecture](docs/architecture/README.md) for the runtime and data model;
[docs/operations](docs/operations/README.md) describes deployment and configuration,
and [development](docs/operations/development.md) the build and check commands.

## Source layout

- `app/`: application, domain modules, SQLite schema/queries, storage gateway,
  media processing, and background workers.
- `web/`: React/TypeScript UI, upload coordinator, API client, and browser tests.
- `api/openapi.yaml`: HTTP contract; generated Go and TypeScript types.
- `tools/migrate/`, `tools/backup/`: separate offline data tools.
- `tools/codegen/`: pinned Go/TypeScript generators.
- `compose.yml`: deployment template used by installation and runtime checks.
  Runtime data is bind-mounted from deployment `data/metadata`, `data/media`,
  and `scratch`; both services use the recorded host owner's UID/GID.
- `scripts/setup.sh`: Docker-only installer template, stamped for stable release
  downloads; `storage-run setup` creates its configuration inside a container.
- `deploy/`: container builds and runtime configuration.
- `scripts/`: generation, checks, installation, updates, and release tooling.

## Engineering boundaries

- Keep domain rules in their owning Go module and reuse them in transport/storage
  adapters.
- Change `api/openapi.yaml` before changing a public HTTP contract. Schema changes
  belong in additive Goose migrations; static queries belong in sqlc sources.
  Update generated files through `scripts/generate`.
- SQLite uses the production driver, WAL, FULL synchronization, foreign keys,
  and one immediate writer. S3 and FFmpeg calls stay outside SQL transactions.
- Stream media with bounded buffers. Persist expected sizes and SHA-256 hashes
  before writes; verify full object bytes before publication. Published keys
  and byte descriptors are immutable.
- Preserve job leases, generation fences, idempotency, and independent cleanup
  receipts. Retain cleanup obligations after removing an upload session.
- Keep uploads/media on the application origin. Storage remains private and
  storage credentials stay in the backend.
- Preserve integer IDs, duplicate/case-sensitive Unicode tag names, and nullable
  integer 100 ns durations. Make data merges and removals explicit.
- Implement backup and migration as external command-line tools.
- Treat a deployment's SQLite and storage directories as one paired library. Run
  tests against disposable libraries and scope cleanup to their Docker resources.

## Validation and documentation

Install pinned dependencies with `npm ci --prefix web` and
`npm ci --prefix tools/codegen`. Use `scripts/check` for generation, Go vet/tests/
race tests, tool tests, and frontend checks. `scripts/check-web` runs frontend
checks alone; `npm --prefix web run test:browser` runs the disposable API/browser
suite. Use the focused media/storage/container commands in
[development](docs/operations/development.md) when those boundaries change.

Run checks relevant to the change and report actual results. Keep tests,
fixture provenance, lockfiles, and schema history.
Keep documentation in English, concise, and about implemented behavior. Place
human guides in `docs/`. Architecture diagrams come from the LikeC4 model in
`docs/architecture/c4/`; draw other diagrams with Mermaid. Keep local libraries,
credentials, build output, test reports, screenshots, mockups, and other planning
drafts outside version control; backlog tasks in `backlog/` are versioned.

<!-- agent-flow:start -->
## Project docs and workflow (agent-flow)

The developer steers this project through short docs, not by reading code. Keep the docs true.

- `README.md`: what the product is, how to run it, links.
- `docs/product/`: detailed product behaviour and business rules (if present).
- `docs/architecture/`: overview, C4 model (`c4/`, LikeC4), data model, decisions.
- `docs/operations/`: environments, deploy, configuration and secrets, backups, monitoring.
- `backlog/`: tasks, one Markdown file each (af-backlog skill and its `bk` tool).

Rules:

- When a change affects product behaviour, architecture, the data model or operations, update the matching doc in the same branch.
- Docs describe the current state. Rewrite and shorten them instead of appending history.
- Diagrams come from the LikeC4 model. Regenerate them with af-architecture; never edit `docs/architecture/c4/README.md` by hand.
- Record decisions that are costly to reverse in `docs/architecture/decisions.md`.
- Work outside the current task goes to the backlog as a new task.
- Language: write every artifact (code, comments, docs, backlog tasks, commit messages, PR descriptions) in the artifacts language below. Talk to the developer in the language of their request, unless they ask otherwise.
- Skills:
  - af-brainstorm: idea to task;
  - af-work: task to branch and report;
  - af-backlog;
  - af-architecture;
  - af-setup.

### Project settings

- Artifacts language: English
- Main branch: main
- Test command: `scripts/check`; frontend only: `scripts/check-web`; browser suite:
  `npm --prefix web run test:browser`; focused media/storage/container checks are
  listed in `docs/operations/development.md`.
- Pushing: this machine has no push rights. Never push branches or tags; commit
  locally and leave pushing to the developer, who pushes from the host machine.
  A pushed `v*` tag publishes images and a GitHub release.
- Merging: merge into `main` only after the developer approves.
<!-- agent-flow:end -->
