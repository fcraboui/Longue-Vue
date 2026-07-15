---
title: "ADR-0042: Uniform list search & sort contract"
status: "Accepted"
date: "2026-07-10"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "cmdb", "api", "pagination", "search", "sort"]
supersedes: ""
superseded_by: ""
---

# ADR-0042: Uniform list search & sort contract

## Status

Proposed | **Accepted** | Rejected | Superseded | Deprecated

- **Date:** 2026-07-10
- **Supersedes:** none
- **Superseded by:** none

## Context

List capabilities were inhomogeneous: 7 of ~25 list endpoints had a
name filter (with divergent semantics — exact on clusters, substring
elsewhere), none had sort parameters, and the shared pagination cursor
was a positional `<RFC3339Nano>|<uuid>` string that could not name a
sort column or carry a text value. The UI compensated with client-side,
current-page-only sorting on two panels — misleading past page one.

## Decision

1. Every paginated list endpoint accepts `name=` (case-insensitive;
   plain term = substring, `*` = anchored glob; LIKE metacharacters
   literal), `sort=<key>` validated against a per-entity allowlist,
   and `order=asc|desc` (default asc when sort is set). Without
   `sort=`, each endpoint's historical order is preserved.
2. The cursor is a tagged, versioned base64-JSON envelope
   `{v, col, val, id, dir}`. A cursor replayed under different sort
   parameters — and any legacy pipe-format cursor — is rejected with
   400 (`ErrInvalidCursor`; cursors are ephemeral tokens).
3. ORDER BY is built only from allowlist constants (sortSpec) — never
   from user input. Nullable sort columns order NULLS LAST with a
   null-aware keyset predicate; `id` is always the tiebreaker.
4. Computed-at-read columns are not sortable (native columns only).
5. Entities without a name column map `name=` to their identifying
   field (users/sessions → username); audit events have none and take
   sort/pagination only.
6. `GET /v1/clusters?name=` changed from exact match to the uniform
   semantics — zero live callers depended on exactness (collector
   bootstrap is the idempotent POST, ADR-0016; MCP already filtered
   by substring).

## Consequences

- ~20 List* store methods share one pagination/sort helper set
  (namePattern, sortSpec/resolve, keysetCond, orderBy, tagged codec)
  instead of 20 hand-rolled blocks; ListApplicationMembers (3-source
  OFFSET walk) and ListImageVersionsByRepo (two-phase DISTINCT) stay
  bespoke.
- New composite indexes (migration 00058): (LOWER(name), id) on
  pods/workloads/virtual_machines; (reconcile_seen_at, id) on
  security_groups/network_policies (their existing sort had no index).
  Other tables top-N sort — documented tradeoff, no pg_trgm.
- In-flight legacy cursors 400 once at rollout (ephemeral; clients
  re-request page 1).
- Fixed en route: sessions page-boundary cursor bug; unescaped ILIKE
  in pods/workloads image= and image-origin-mappings q=; OpenAPI
  drift (application-blocks display_name, members kind filter, audit
  limit clamp); restored server-side `vpc_id` exact filter on security
  groups (was silently in-memory before, briefly dropped, now SQL);
  members `kind` filter implementation.
- `GET /v1/container-freshness` (ADR-0041, landed on main in parallel)
  is a read-time aggregation outside this contract — same deferred
  family as the EOL dashboard; phase 2 (UI) is a separate plan.
- Phase 2 (UI) consumes the contract via shared SearchInput/SortHeader
  components — separate plan.

## References

- ADR-0016 — Stateless ingest gateway (idempotent cluster POST)
- ADR-0041 — Container image freshness as a distinct signal from EOL
