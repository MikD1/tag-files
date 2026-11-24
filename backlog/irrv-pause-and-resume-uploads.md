---
status: todo  # todo | done | dropped
priority: p3  # p1 (high) | p2 | p3 (low)
created: 2026-11-24
---
# Pause and resume uploads

## Summary

Uploads resume automatically after a reload or restart, but there are no controls to pause and resume a running upload.

## Decisions

<!-- After brainstorming: 3–7 bullets, what was chosen and why. Plain language, no code. -->

## Outcome

<!-- When finished: what changed, decisions the agent made on its own, follow-up tasks. -->

---

# Agent notes

<!-- Everything below is for agents. The developer doesn't need to read it. -->

## Spec

Source: deferred list in the historical rewrite docs (`docs/rewrite/06-product-questions.md` "Separate backlog", `05-ux-ui-audit.md`, `07-delivery-and-acceptance.md`; removed in commit 203ce73).

- State machine: `web/src/uploads/coordinator.ts`. Server sessions never expire, and technical resume already exists.
- Docs: `docs/product/README.md` Uploads.

## Plan

<!-- Checkboxes, ticked as work progresses. `bk` shows progress from this list. -->

## Log

<!-- Decisions made during the work: what — why — cost if wrong. -->
