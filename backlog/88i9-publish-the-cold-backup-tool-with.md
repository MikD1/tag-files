---
status: todo  # todo | done | dropped
priority: p2  # p1 (high) | p2 | p3 (low)
created: 2026-11-24
---
# Publish the cold backup tool with releases

## Summary

No backup tool image is published. To back up a library, the owner needs a source checkout of the installed version, Python 3 and a local Docker build, while installing needs only `setup.sh`.

Publishing the tool with each release would make backups work from release assets and make updates less fragile.

## Decisions

<!-- After brainstorming: 3–7 bullets, what was chosen and why. Plain language, no code. -->

## Outcome

<!-- When finished: what changed, decisions the agent made on its own, follow-up tasks. -->

---

# Agent notes

<!-- Everything below is for agents. The developer doesn't need to read it. -->

## Spec

Found while validating the docs against the code during af-setup (2026-11-24).

- `scripts/release-images:16` publishes only the app, storage and migrate images. Build: `tools/backup/Dockerfile`. Host script: `tools/backup/cold-backup` (Python).
- Options: publish `ghcr.io/mikd1/tag-files-backup:<version>` and attach `cold-backup` to the release, or bundle the tool in an existing image.
- `scripts/update --backup-tool-image` could then default to the published image of the installed version.
- Docs: `docs/operations/README.md` (updates, backups) and the README backup paragraph.
- Related: `72t8` (Check backup tool schema before update stops services).

## Plan

<!-- Checkboxes, ticked as work progresses. `bk` shows progress from this list. -->

## Log

<!-- Decisions made during the work: what — why — cost if wrong. -->
