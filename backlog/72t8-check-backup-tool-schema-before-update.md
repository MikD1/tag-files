---
status: todo  # todo | done | dropped
priority: p1  # p1 (high) | p2 | p3 (low)
created: 2026-11-24
---
# Check backup tool schema before update stops services

## Summary

`scripts/update` stops both services before it finds out whether the backup tool can read the library. The tool refuses any library whose schema version differs from the one it was built with. So a tool from the wrong version fails the pre-update backup while the library is stopped, and the failed receipt then blocks every later automated update.

The update should check the tool first and refuse while the library is still running.

## Decisions

<!-- After brainstorming: 3–7 bullets, what was chosen and why. Plain language, no code. -->

## Outcome

<!-- When finished: what changed, decisions the agent made on its own, follow-up tasks. -->

---

# Agent notes

<!-- Everything below is for agents. The developer doesn't need to read it. -->

## Spec

Found while validating the docs against the code during af-setup (2026-11-24).

- Today the tool image is only checked for platform before the stop: `scripts/deployment.py:300-301`. Failure path: `deployment.py:274,293-296,340-349`.
- The schema check is in `tools/backup/backup.go:608-609`; verify (`:1255`) and restore apply it too.
- Needs a way to ask the tool image which schema and format it supports without a library, e.g. a `version` subcommand. The update compares that with the installed library's schema before stopping anything.
- Acceptance: a mismatched tool makes the update exit non-zero with services still running and no failed receipt left behind; a matching tool keeps today's flow. Cover it in `scripts/test_install_update.py` (Docker integration).
- Docs: `docs/operations/README.md#updates`.
- Related: `88i9` (Publish the cold backup tool with releases).

## Plan

<!-- Checkboxes, ticked as work progresses. `bk` shows progress from this list. -->

## Log

<!-- Decisions made during the work: what — why — cost if wrong. -->
