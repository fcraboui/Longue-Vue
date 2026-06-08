# 38. NetworkPolicy push surface for air-gapped K8s collectors

Date: 2026-06-08

Status: Accepted (amends ADR-0009, ADR-0016, ADR-0034)

## Context

ADR-0009 wired nine resources (nodes, namespaces, pods, workloads,
services, ingresses, persistentvolumes, persistentvolumeclaims, plus
the idempotent cluster bootstrap) for the push-mode K8s collector
(`cmd/longue-vue-collector`). NetworkPolicy was not in that list —
NetworkPolicy collection did not exist yet.

ADR-0034 (June 2026) added NetworkPolicy collection inside the
`internal/collector/` package, persistence in `network_policies` +
`network_policy_rules` (migration 00050), and the K8s RBAC `list`
permission on both `charts/longue-vue/templates/clusterrole.yaml` **and**
`charts/longue-vue-collector/templates/clusterrole.yaml`. ADR-0034 §4
explicitly described its API surface as *"Read-only"* — six `GET`
endpoints, no `POST`. The Helm chart for the push collector gained the
RBAC permission as if it would be used, but no write path was created.

The resulting silent failure is documented in the code:

- `internal/collector/netpol_collector.go:22-23` —
  *"the push-mode apiclient.Store does not (netpol writes go via the
  in-process path on the server side)"*
- `internal/collector/collector.go:1170-1173` —
  *"ingestNetworkPolicies … no-op when the store does not implement
  NetPolStore (e.g. the push-mode apiclient.Store …)"*

Both comments correctly describe the present state of the code, and
both are wrong as architecture: for any cluster collected by
`cmd/longue-vue-collector` through the DMZ ingest GW (ADR-0016 — the
intended SecNumCloud deployment topology for air-gapped clusters per
ADR-0009), there is no in-process path. Netpols silently never reach
the CMDB.

User-visible symptom that surfaced this gap: the Flow Matrix UI
(ADR-0034 §5) renders empty `internal` flows for clusters fed by the
push collector. The Flow Matrix synthesis (ADR-0036) needs netpols to
classify internal flows; without them, the entire internal layer is
blank regardless of what's actually deployed in the cluster.

The full design (API contracts, transaction shape, apiclient methods,
test plan, implementation order) lives in
`docs/superpowers/specs/2026-06-08-netpol-push-surface-design.md`.

## Decision

Close the gap by adding the missing HTTP write surface for
NetworkPolicy and wiring it through every layer the push collector
already uses.

1. **Server**: two new endpoints, codegen'd via
   `api/openapi/openapi.yaml` (mirror of the 18 existing push pattern,
   not the hand-written flow-matrix pattern):
   - `POST /v1/network-policies` — atomic upsert of a policy and its
     rules in one transaction. Request body carries the rules inline;
     status `201` on insert, `200` on update.
   - `POST /v1/network-policies/reconcile` — namespace-scoped sweep
     reusing the existing `ReconcileNamespaceScoped` schema.

2. **Auth scope**: `write` (consistent with ADR-0009 §Authentication
   and the 18 existing push endpoints). The question of a per-cluster
   `k8s-collector` scope (mirror of `vm-collector` from ADR-0015) is
   deferred to a future ADR that would amend all push routes
   uniformly — out of scope here.

3. **Store**: refactor
   `(*store.PG).UpsertNetworkPolicy(ctx, np) + ReplaceNetworkPolicyRules(ctx, id, rules)`
   into a single `UpsertNetworkPolicy(ctx, np, rules) (uuid.UUID, error)`
   wrapping both writes in one `pgx.Tx`. The former exported methods
   become unexported `*Tx` helpers.

4. **Interface**: collapse the `NetPolStore` interface in
   `internal/collector/netpol_collector.go` from three methods to two
   (Upsert takes rules; Sweep unchanged). Both `*store.PG` (in-process,
   via the new transactional method) and `apiclient.Store` (push, via
   the new HTTP endpoint) satisfy the new interface.

5. **Apiclient**: add `UpsertNetworkPolicy` (POST) and
   `SweepNetworkPoliciesByNamespace` (POST reconcile, reusing the
   existing `reconcileNamespaceScoped` helper) to
   `internal/collector/apiclient/client.go`. After this, the type
   assertion at `collector.go:346` succeeds in push mode and
   `ingestNetworkPolicies` stops being a no-op.

6. **Mount on three lists** (kept in sync per ADR-0016):
   - `cmd/longue-vue/main.go` — public listener `:8080`
   - `internal/api/ingest_mux.go` — `IngestRoutes` table +
     `dispatchByOperation` switch
   - `internal/ingestgw/allowlist.go` — `Routes` table

7. **Comment + doc cleanup**:
   - Replace `netpol_collector.go:18-27` comment block to reflect that
     both implementations now satisfy the interface.
   - One-line update to CLAUDE.md "Collectors" section noting netpol
     parity across pull and push.

## Consequences

### Positive

- POS-001: NetworkPolicy collection reaches functional parity between
  pull and push collectors. Air-gapped clusters per ADR-0009 are no
  longer second-class for netpol coverage.
- POS-002: Flow Matrix (ADR-0034/0036) sees the actual posture of
  push-collected clusters instead of an empty `internal` array.
- POS-003: Atomicity of policy+rules writes is now guaranteed by the
  store transaction in both paths — the previous in-process code did
  the upsert and rule replacement as two separate calls and any caller
  that copied that pattern could leave the store in a torn state.
- POS-004: No new auth scope, no new database table, no new
  middleware. The push surface follows the established 18-endpoint
  pattern. Operators wiring a new push collector get netpol coverage
  for free.

### Negative

- NEG-001: Two more routes on the public mux, the ingest mux, and the
  GW allowlist. Documented in §Amendments below; the lists are
  designed to grow.
- NEG-002: Additional write load on longue-vue from existing push
  collectors that had been silently skipping netpols. Order-of-magnitude
  ~5–50 POSTs per tick depending on cluster size — well within the
  existing per-resource push budget.
- NEG-003: The interface refactor (3 methods → 2) is a breaking change
  for anything outside `internal/collector/` that depends on
  `NetPolStore`. Grep confirms only `CollectNetworkPolicies` uses it,
  so the blast radius is one function.

### Neutral

- The K8s RBAC on `charts/longue-vue-collector/templates/clusterrole.yaml`
  added by ADR-0034 was already in place. No chart change needed; the
  push collector has had `list networkpolicies` permission since
  ADR-0034 — it just had nowhere to send the data.

## Amendments to prior ADRs

| ADR | Section | Amendment |
|---|---|---|
| ADR-0009 | §"API client store" (the HTTP-call mapping table) | Add two rows: `UpsertNetworkPolicy` → `POST /v1/network-policies`, and the sweep helper → `POST /v1/network-policies/reconcile` |
| ADR-0016 | §2 (allowlist table), §3 (ingest mux), and all prose mentioning "18 writes" / "18 routes" / "eighteen routes" | Route count 18 → 21. The two routes from this ADR plus the one SG sweep route from ADR-0035 (already present in `internal/ingestgw/allowlist.go:52` but never reflected in ADR-0016) bring the canonical count to 21. The "18" prose remains as historical context — the count grows whenever a new push surface ADR amends it |
| ADR-0034 | §4 ("Read-only API surface") | Reword to: "Read API (always exposed) + Write API restricted to the push collector path via the ingest mux (ADR-0038). The Read API remains the only surface for human consumers." |

These amendments are recorded here so future readers see one document
per decision rather than diffs scattered across the original ADRs —
the older ADRs stay frozen in their original wording.

## Alternatives considered

### Three granular endpoints (mirror the original 3-method interface)

Add `POST /v1/network-policies` for the policy, `PUT
/v1/network-policies/{id}/rules` for the rules, and a separate sweep.
**Rejected**: introduces a torn-write window (policy upserted, rules
not replaced if the GW or network breaks between calls). The atomic
endpoint eliminates the failure mode at the API contract level.

### Bulk per-namespace tick endpoint

Single `POST /v1/namespaces/{id}/network-policies/tick` carrying every
policy + sweep keep-list for a namespace in one request. **Rejected**:
diverges from the established per-resource endpoint pattern (ADR-0009)
for marginal request-count savings (~10 POSTs per tick at typical
cluster sizes). Worth revisiting only if push-mode netpol latency
becomes a measured bottleneck; same escape hatch as ADR-0009 §"Bulk
push (future optimisation)".

### Apiclient with in-memory pending state (3-method interface preserved)

Keep `NetPolStore` at three methods; the apiclient buffers the
upserted policy in memory and flushes on the subsequent
`ReplaceNetworkPolicyRules` call. **Rejected**: turns the apiclient
into a stateful component that depends on a specific call sequence,
generates synthetic UUIDs the server has not yet accepted, and breaks
silently if a future caller calls `Upsert` without a matching
`Replace`. The interface refactor is cheaper and safer.

## Implementation Notes

The spec at
`docs/superpowers/specs/2026-06-08-netpol-push-surface-design.md`
carries the full implementation outline (9 ordered steps), test plan
across 5 levels, and open questions for the plan phase. The plan is
TDD-friendly: each step lands a self-contained green build.

- IMP-001: Store transaction (§5 of the spec).
- IMP-002: OpenAPI + `make generate`.
- IMP-003: Handlers + handler unit tests.
- IMP-004: Public mux wiring (3 lines).
- IMP-005: Ingest mux + GW allowlist (kept synchronised, per ADR-0016).
- IMP-006: Apiclient methods + unit tests.
- IMP-007: Interface collapse + caller refactor + dead-code removal.
- IMP-008: Comment + CLAUDE.md cleanup.
- IMP-009: Integration test (push collector → ingest listener →
  Postgres → assertion).

## References

- ADR-0007 — Authentication and RBAC (write scope)
- ADR-0009 — Push collector for air-gapped clusters (amended)
- ADR-0015 — VM collector (cited for the `vm-collector` scope pattern
  considered then deferred)
- ADR-0016 — DMZ ingest gateway (amended)
- ADR-0034 — Flow matrix Phase 1: NetworkPolicy collection (amended)
- ADR-0036 — Flow matrix R2: reference + synthesis (the consumer that
  surfaces the bug)
- Spec: `docs/superpowers/specs/2026-06-08-netpol-push-surface-design.md`
