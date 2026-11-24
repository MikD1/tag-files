---
status: todo  # todo | done | dropped
priority: p3  # p1 (high) | p2 | p3 (low)
created: 2026-11-24
---
# Apply release compose.yml changes during updates

## Summary

Updates copy the installed `compose.yml` unchanged, so changes to the deployment template in newer releases never reach existing installations.

## Decisions

<!-- After brainstorming: 3–7 bullets, what was chosen and why. Plain language, no code. -->

## Outcome

<!-- When finished: what changed, decisions the agent made on its own, follow-up tasks. -->

---

# Agent notes

<!-- Everything below is for agents. The developer doesn't need to read it. -->

## Spec

Found while validating the docs against the code during af-setup (2026-11-24).

- `scripts/deployment.py:312-314`. The template is bundled in the storage image at `/usr/share/tag-files/compose.yml`.
- Must keep the installation's mounts, owner and project, and update the configuration hashes in `installation.json`.
- Test in `scripts/test_install_update.py`. Docs: `docs/operations/README.md#updates`.

## Plan

<!-- Checkboxes, ticked as work progresses. `bk` shows progress from this list. -->

## Log

<!-- Decisions made during the work: what — why — cost if wrong. -->
