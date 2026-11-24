---
status: todo  # todo | done | dropped
priority: p2  # p1 (high) | p2 | p3 (low)
created: 2026-11-24
---
# Keep latest tag on the newest stable release

## Summary

Every stable publication moves the minor and `latest` image tags, even when it is a patch for an older release line. Releasing such a patch would point `latest` back to an older version.

## Decisions

<!-- After brainstorming: 3–7 bullets, what was chosen and why. Plain language, no code. -->

## Outcome

<!-- When finished: what changed, decisions the agent made on its own, follow-up tasks. -->

---

# Agent notes

<!-- Everything below is for agents. The developer doesn't need to read it. -->

## Spec

Found while validating the docs against the code during af-setup (2026-11-24).

- `scripts/release-images:38-42` and `.github/workflows/publish.yml`.
- Acceptance: `latest` moves only when the version is newer than every published stable version; `X.Y` moves only for the newest patch of that line. Test in `scripts/test_release_images.py`.
- Also check that GitHub's "latest release" (which `setup.sh` and `migrate.sh` download from) stays on the newest version.
- Docs: `docs/operations/README.md#releases`.

## Plan

<!-- Checkboxes, ticked as work progresses. `bk` shows progress from this list. -->

## Log

<!-- Decisions made during the work: what — why — cost if wrong. -->
