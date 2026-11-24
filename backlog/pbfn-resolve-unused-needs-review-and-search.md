---
status: todo  # todo | done | dropped
priority: p3  # p1 (high) | p2 | p3 (low)
created: 2026-11-24
---
# Resolve unused needs_review and search_generation

## Summary

Two pieces of schema state are only half used. A category in `needs_review` cannot run and the UI shows that state, but no code ever sets it. `library_meta.search_generation` goes up on every catalog or organization change, but nothing reads it.

Either give them a purpose or retire them.

## Decisions

<!-- After brainstorming: 3–7 bullets, what was chosen and why. Plain language, no code. -->

## Outcome

<!-- When finished: what changed, decisions the agent made on its own, follow-up tasks. -->

---

# Agent notes

<!-- Everything below is for agents. The developer doesn't need to read it. -->

## Spec

Found while validating the docs against the code during af-setup (2026-11-24).

- Code that reads `needs_review`: `app/internal/organization/tags.go:46,95`, `saved_searches.go:48`, `app/internal/httpapi/dto.go:128` and `web/src/catalog/OrganizationPage.tsx`. It is also in the CHECK in `00001_library.sql` and the enum in `api/openapi.yaml`. It might be meant for future parser-version upgrades.
- `search_generation`: `UPDATE` in `app/internal/store/queries/library.sql:8`; no reader in `app/`, `tools/` or `web/`.
- Schema changes must be additive Goose migrations, and the API changes go into `api/openapi.yaml` first.
- Docs: `docs/architecture/data-model.md`.

## Plan

<!-- Checkboxes, ticked as work progresses. `bk` shows progress from this list. -->

## Log

<!-- Decisions made during the work: what — why — cost if wrong. -->
