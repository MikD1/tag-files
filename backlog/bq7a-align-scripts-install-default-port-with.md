---
status: todo  # todo | done | dropped
priority: p3  # p1 (high) | p2 | p3 (low)
created: 2026-11-24
---
# Align scripts/install default port with setup.sh

## Summary

`scripts/install` defaults to port 4200, while `setup.sh`, `compose.yml` and the docs use 8080. 4200 looks like a leftover from the old Angular dev server.

## Decisions

<!-- After brainstorming: 3–7 bullets, what was chosen and why. Plain language, no code. -->

## Outcome

<!-- When finished: what changed, decisions the agent made on its own, follow-up tasks. -->

---

# Agent notes

<!-- Everything below is for agents. The developer doesn't need to read it. -->

## Spec

Found while validating the docs against the code during af-setup (2026-11-24).

- `scripts/deployment.py:363`; `compose.yml:8` (`TAG_FILES_PORT:-8080`); `scripts/setup.sh`.
- Check `scripts/test_install_update.py` for tests that rely on 4200.
- Docs: drop "Its default port is 4200" in `docs/operations/README.md#updates`.

## Plan

<!-- Checkboxes, ticked as work progresses. `bk` shows progress from this list. -->

## Log

<!-- Decisions made during the work: what — why — cost if wrong. -->
