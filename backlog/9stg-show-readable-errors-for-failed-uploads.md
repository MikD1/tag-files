---
status: todo  # todo | done | dropped
priority: p2  # p1 (high) | p2 | p3 (low)
created: 2026-11-24
---
# Show readable errors for failed uploads

## Summary

A failed upload in the Uploads view shows the raw server code, such as `unsupported_codec`, instead of a readable message. The page already has friendly text for failed uploads, but it is never reached when the server sends a code.

## Decisions

<!-- After brainstorming: 3–7 bullets, what was chosen and why. Plain language, no code. -->

## Outcome

<!-- When finished: what changed, decisions the agent made on its own, follow-up tasks. -->

---

# Agent notes

<!-- Everything below is for agents. The developer doesn't need to read it. -->

## Spec

Found while validating the docs against the code during af-setup (2026-11-24).

- `web/src/uploads/coordinator.ts:457` sets `row.error = error ?? session.errorCode`.
- `rowDetail` in `web/src/uploads/UploadsPage.tsx:21-25` returns `row.error` first, so the `session.state === 'failed'` message is unreachable whenever a code is present.
- Permanent codes: `unsupported_media_type`, `unsupported_codec`, `invalid_media`, `media_limit_exceeded`, `object_integrity` (`app/internal/worker/ingest.go:24-42`). Capacity and storage errors come on top.
- Acceptance:
  - each known code maps to an English message;
  - unknown codes fall back to the generic text;
  - local coordinator errors keep their text;
  - unit test in `web/src/uploads/UploadsPage.test.tsx`.
- Docs: `docs/product/README.md` Uploads says "shows its error code"; update it.

## Plan

<!-- Checkboxes, ticked as work progresses. `bk` shows progress from this list. -->

## Log

<!-- Decisions made during the work: what — why — cost if wrong. -->
