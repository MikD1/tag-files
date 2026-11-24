---
status: todo  # todo | done | dropped
priority: p3  # p1 (high) | p2 | p3 (low)
created: 2026-11-24
---
# Manual collection cover and order

## Summary

A collection's cover is always the poster of its oldest ready item, and its items follow the library sort. Choosing the cover and ordering items by hand were deferred.

## Decisions

<!-- After brainstorming: 3–7 bullets, what was chosen and why. Plain language, no code. -->

## Outcome

<!-- When finished: what changed, decisions the agent made on its own, follow-up tasks. -->

---

# Agent notes

<!-- Everything below is for agents. The developer doesn't need to read it. -->

## Spec

Source: deferred list in the historical rewrite docs (`docs/rewrite/06-product-questions.md` "Separate backlog", `05-ux-ui-audit.md`, `07-delivery-and-acceptance.md`; removed in commit 203ce73).

- Cover query: `app/internal/store/queries/organization.sql:37-44`.
- Needs an additive migration and API changes.
- Docs: `docs/product/README.md` and `docs/architecture/data-model.md`.

## Plan

<!-- Checkboxes, ticked as work progresses. `bk` shows progress from this list. -->

## Log

<!-- Decisions made during the work: what — why — cost if wrong. -->
