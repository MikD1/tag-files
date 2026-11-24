---
status: todo  # todo | done | dropped
priority: p3  # p1 (high) | p2 | p3 (low)
created: 2026-11-24
---
# Roll back images when an update fails to start

## Summary

If the new version fails to start, `scripts/update` stops both services and leaves the library down; it does not return to the previous images. Recovery is manual, from the receipt and the backup the update made.

## Decisions

<!-- After brainstorming: 3–7 bullets, what was chosen and why. Plain language, no code. -->

## Outcome

<!-- When finished: what changed, decisions the agent made on its own, follow-up tasks. -->

---

# Agent notes

<!-- Everything below is for agents. The developer doesn't need to read it. -->

## Spec

Found while validating the docs against the code during af-setup (2026-11-24).

- `scripts/deployment.py:340-349`. Receipts keep the old `.env`, `compose.yml` and `installation.json` in `updates/<ts>-<uuid>/before/`.
- Constraint: schema migrations are forward-only ("schema rollback is never automatic", `deployment.py:290`). After the new app has migrated the schema, going back means restoring the cold backup this update made.
- Needs a brainstorm: automatic image rollback only when the schema is unchanged, or an explicit `scripts/update --rollback` that restores the backup.
- Docs: `docs/operations/README.md#updates`.

## Plan

<!-- Checkboxes, ticked as work progresses. `bk` shows progress from this list. -->

## Log

<!-- Decisions made during the work: what — why — cost if wrong. -->
