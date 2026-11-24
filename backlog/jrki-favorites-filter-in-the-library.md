---
status: todo  # todo | done | dropped
priority: p3  # p1 (high) | p2 | p3 (low)
created: 2026-11-24
---
# Favorites filter in the library

## Summary

Items can be marked as favorites, but the library cannot filter by them: the star only shows on cards and is toggled in the viewer. A favorites filter or view needs a new search and URL contract.

## Decisions

<!-- After brainstorming: 3–7 bullets, what was chosen and why. Plain language, no code. -->

## Outcome

<!-- When finished: what changed, decisions the agent made on its own, follow-up tasks. -->

---

# Agent notes

<!-- Everything below is for agents. The developer doesn't need to read it. -->

## Spec

Source: deferred list in the historical rewrite docs (`docs/rewrite/06-product-questions.md` "Separate backlog", `05-ux-ui-audit.md`, `07-delivery-and-acceptance.md`; removed in commit 203ce73).

- Today: the `items.is_favorite` column and `PUT /items/{id}/favorite`. There is no filter in `app/internal/catalog/search.go` or `web/src/catalog/url.ts`.
- Change `api/openapi.yaml` first. Decide how it combines with categories, which cannot be mixed with `q`/`kind` today.
- Docs: `docs/product/README.md`.

## Plan

<!-- Checkboxes, ticked as work progresses. `bk` shows progress from this list. -->

## Log

<!-- Decisions made during the work: what — why — cost if wrong. -->
