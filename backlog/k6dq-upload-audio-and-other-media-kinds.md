---
status: todo  # todo | done | dropped
priority: p3  # p1 (high) | p2 | p3 (low)
created: 2026-11-24
---
# Upload audio and other media kinds

## Summary

New uploads accept only photos and video; other kinds come only from the legacy migration. Accepting audio or other kinds was named as a possible extension after the release.

## Decisions

<!-- After brainstorming: 3–7 bullets, what was chosen and why. Plain language, no code. -->

## Outcome

<!-- When finished: what changed, decisions the agent made on its own, follow-up tasks. -->

---

# Agent notes

<!-- Everything below is for agents. The developer doesn't need to read it. -->

## Spec

Source: deferred list in the historical rewrite docs (`docs/rewrite/06-product-questions.md` "Separate backlog", `05-ux-ui-audit.md`, `07-delivery-and-acceptance.md`; removed in commit 203ce73).

- `app/internal/media/probe.go:229-231` rejects files without an image or video stream.
- Needs normalization and poster rules and a viewer (see `wgz3` (Viewers for legacy audio and text items)).
- The historical decision D-02 chose photo and video only. Record a new decision if this changes.
- Docs: `docs/architecture/media-pipeline.md` and `docs/product/README.md`.

## Plan

<!-- Checkboxes, ticked as work progresses. `bk` shows progress from this list. -->

## Log

<!-- Decisions made during the work: what — why — cost if wrong. -->
