---
status: todo  # todo | done | dropped
priority: p3  # p1 (high) | p2 | p3 (low)
created: 2026-11-24
---
# Trim unused ports from the storage image

## Summary

The storage image declares eight ports inherited from upstream SeaweedFS, but only the gateway port 8333 is used; the backend listens on loopback. The extra `EXPOSE` entries mislead readers and tools.

## Decisions

<!-- After brainstorming: 3–7 bullets, what was chosen and why. Plain language, no code. -->

## Outcome

<!-- When finished: what changed, decisions the agent made on its own, follow-up tasks. -->

---

# Agent notes

<!-- Everything below is for agents. The developer doesn't need to read it. -->

## Spec

Found while validating the docs against the code during af-setup (2026-11-24).

- `deploy/storage/Dockerfile`: `EXPOSE 18080 18888 19333 7333 8080 8333 8888 9333`.
- Gateway on `0.0.0.0:8333` (`app/internal/storageprofile/gateway.go:40`); backend on 127.0.0.1 (`run.go:52-55,108`).
- Not checked in a running container. Confirm with `scripts/check-storage-image`.

## Plan

<!-- Checkboxes, ticked as work progresses. `bk` shows progress from this list. -->

## Log

<!-- Decisions made during the work: what — why — cost if wrong. -->
