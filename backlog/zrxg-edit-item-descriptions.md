---
status: todo  # todo | done | dropped
priority: p3  # p1 (high) | p2 | p3 (low)
created: 2026-11-24
---
# Edit item descriptions

## Summary

Items have a description field that only the legacy migration fills; it cannot be edited. Editing descriptions was deferred.

## Decisions

<!-- After brainstorming: 3–7 bullets, what was chosen and why. Plain language, no code. -->

## Outcome

<!-- When finished: what changed, decisions the agent made on its own, follow-up tasks. -->

---

# Agent notes

<!-- Everything below is for agents. The developer doesn't need to read it. -->

## Spec

Source: deferred list in the historical rewrite docs (`docs/rewrite/06-product-questions.md` "Separate backlog", `05-ux-ui-audit.md`, `07-delivery-and-acceptance.md`; removed in commit 203ce73).

- `app/internal/catalog/metadata.go` edits only tags and the collection.
- The gallery and viewer are kept minimal (historical D-12), so decide where a description appears.
- Related: `eant` (Full-text search).

## Plan

<!-- Checkboxes, ticked as work progresses. `bk` shows progress from this list. -->

## Log

<!-- Decisions made during the work: what — why — cost if wrong. -->
