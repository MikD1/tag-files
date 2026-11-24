---
status: todo  # todo | done | dropped
priority: p3  # p1 (high) | p2 | p3 (low)
created: 2026-11-24
---
# Back up and update without existing containers

## Summary

`cold-backup` and `scripts/update` need the stopped app and storage containers. After `docker compose down` the owner must recreate them before backing up or updating; the docs now warn about this.

## Decisions

<!-- After brainstorming: 3–7 bullets, what was chosen and why. Plain language, no code. -->

## Outcome

<!-- When finished: what changed, decisions the agent made on its own, follow-up tasks. -->

---

# Agent notes

<!-- Everything below is for agents. The developer doesn't need to read it. -->

## Spec

Found while validating the docs against the code during af-setup (2026-11-24).

- `tools/backup/cold-backup:33-36` inspects the containers and copies `filer.toml` and the secrets out of the storage container. `scripts/deployment.py:253-256` has the same requirement.
- Options: create the stopped containers automatically (`docker compose create`), or read the needed files from the image and the deployment directory.
- Docs: the README backup paragraph and `docs/operations/README.md`.

## Plan

<!-- Checkboxes, ticked as work progresses. `bk` shows progress from this list. -->

## Log

<!-- Decisions made during the work: what — why — cost if wrong. -->
