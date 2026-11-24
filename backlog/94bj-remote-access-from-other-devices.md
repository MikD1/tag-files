---
status: todo  # todo | done | dropped
priority: p3  # p1 (high) | p2 | p3 (low)
created: 2026-11-24
---
# Remote access from other devices

## Summary

The app listens on the host's loopback only and has no accounts. Access from phones or other computers on the LAN was deferred; it needs authentication and a decision on how the app is exposed.

## Decisions

<!-- After brainstorming: 3–7 bullets, what was chosen and why. Plain language, no code. -->

## Outcome

<!-- When finished: what changed, decisions the agent made on its own, follow-up tasks. -->

---

# Agent notes

<!-- Everything below is for agents. The developer doesn't need to read it. -->

## Spec

Source: deferred list in the historical rewrite docs (`docs/rewrite/06-product-questions.md` "Separate backlog", `05-ux-ui-audit.md`, `07-delivery-and-acceptance.md`; removed in commit 203ce73).

- `compose.yml:8`. The historical decision D-01 was one user on the same machine. Security review required.

## Plan

<!-- Checkboxes, ticked as work progresses. `bk` shows progress from this list. -->

## Log

<!-- Decisions made during the work: what — why — cost if wrong. -->
