---
status: todo  # todo | done | dropped
priority: p3  # p1 (high) | p2 | p3 (low)
created: 2026-11-24
---
# Make check-migration-large portable

## Summary

The large migration check hard-codes one storage image ID, so it probably runs only on the machine that built that image.

## Decisions

<!-- After brainstorming: 3–7 bullets, what was chosen and why. Plain language, no code. -->

## Outcome

<!-- When finished: what changed, decisions the agent made on its own, follow-up tasks. -->

---

# Agent notes

<!-- Everything below is for agents. The developer doesn't need to read it. -->

## Spec

Found while validating the docs against the code during af-setup (2026-11-24).

- `scripts/check-migration-large:7` exports `TAG_FILES_MIGRATE_STORAGE_IMAGE=sha256:70aa8659…`.
- Take the image from the environment or a local tag, like the other checks do (`tag-files-storage:local-check`).
- Docs: the checks table in `docs/operations/development.md`.

## Plan

<!-- Checkboxes, ticked as work progresses. `bk` shows progress from this list. -->

## Log

<!-- Decisions made during the work: what — why — cost if wrong. -->
