---
status: todo  # todo | done | dropped
priority: p3  # p1 (high) | p2 | p3 (low)
created: 2026-11-24
---
# Looser similarity for related items

## Summary

The "same tags" related items need exactly the same non-empty tag set as the open item. A looser similarity mode, such as shared tags, was named as a later extension.

## Decisions

<!-- After brainstorming: 3–7 bullets, what was chosen and why. Plain language, no code. -->

## Outcome

<!-- When finished: what changed, decisions the agent made on its own, follow-up tasks. -->

---

# Agent notes

<!-- Everything below is for agents. The developer doesn't need to read it. -->

## Spec

Source: deferred list in the historical rewrite docs (`docs/rewrite/06-product-questions.md` "Separate backlog", `05-ux-ui-audit.md`, `07-delivery-and-acceptance.md`; removed in commit 203ce73).

- Exact set equality: `app/internal/catalog/search.go:292-315` (`GET /items/{id}/similar`).
- Docs: `docs/product/README.md` Viewer.

## Plan

<!-- Checkboxes, ticked as work progresses. `bk` shows progress from this list. -->

## Log

<!-- Decisions made during the work: what — why — cost if wrong. -->
