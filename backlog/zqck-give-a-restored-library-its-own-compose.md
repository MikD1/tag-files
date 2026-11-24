---
status: todo  # todo | done | dropped
priority: p2  # p1 (high) | p2 | p3 (low)
created: 2026-11-24
---
# Give a restored library its own Compose project

## Summary

A restored copy keeps the original `.env`, including the Compose project name and port. Starting it while the original deployment exists probably takes over the original's containers. Editing `.env` to avoid this makes `scripts/update` refuse the deployment.

The docs now say to stop the original first. The restore tool should let the owner choose a project name and port safely.

## Decisions

<!-- After brainstorming: 3–7 bullets, what was chosen and why. Plain language, no code. -->

## Outcome

<!-- When finished: what changed, decisions the agent made on its own, follow-up tasks. -->

---

# Agent notes

<!-- Everything below is for agents. The developer doesn't need to read it. -->

## Spec

Found while validating the docs against the code during af-setup (2026-11-24).

- The takeover is an inference and was not tested. Verify it first on disposable libraries (`tools/backup/check-drill`).
- `scripts/deployment.py:165-168` rejects unknown `.env` keys, and the update compares configuration hashes with `installation.json`.
- Possible fix: `cold-backup restore --project NAME --port PORT`, which rewrites `.env` and the installation receipt consistently.
- Docs: `docs/operations/README.md#backups-and-restore`.

## Plan

<!-- Checkboxes, ticked as work progresses. `bk` shows progress from this list. -->

## Log

<!-- Decisions made during the work: what — why — cost if wrong. -->
