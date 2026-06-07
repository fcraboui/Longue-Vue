# 36. Flow matrix -- R2: operator reference matrix + read-time synthesis

Date: 2026-06-07

Status: Accepted (R2 of the cluster-flow-matrix redesign; see ADR-0034, ADR-0035, and ADR-0037 for the rest)

## Context

ADR-0035 (R1) added cluster-perimeter security-group collection plus the
`flow_matrix_enabled` toggle, so the store now holds both halves of a
cluster's discovered posture: perimeter SG rules (node-VM attachments)
and internal K8s NetworkPolicy rules (ADR-0034). What it lacked was the
*declared* side of the SecNumCloud "matrice de flux" and the conformance
evidence that compares declared against actual.

R2 adds the operator-declared reference matrix and the read-time
comparison/synthesis tool. A periodic comparison engine with a derived
`flow_cells` table was considered and rejected: the comparison is cheap,
must always reflect the latest collector tick, and has no cross-request
state, so it is computed at read time per cluster, mirroring
`internal/eolagg` (ADR-0022). The umbrella design is at
`docs/superpowers/specs/2026-06-06-cluster-flow-matrix-design.md`.

## Decision

R2 (reference matrix + synthesis) ships:

1. **Two new tables.** Migration 00054 adds `endpoint_groups` +
   `endpoint_group_cidrs` -- named external CIDR zones, admin-curated.
   Migration 00055 adds `cluster_flow_references`: one expected flow per
   row, fine-grained over workload / service / namespace / endpoint-group
   endpoints, with a required `justification` so each declared flow
   carries its own auditor evidence.

2. **`internal/flowmatrix/` -- a pure, read-time comparator** with no
   I/O and no derived-state table: `Synthesize(Inputs) Synthesis`. A CIDR
   is resolved to an endpoint group by most-specific containment using
   `net/netip`. Each synthesized flow is classified into one of four
   states: `conforme` (actual rule covered by a reference row),
   `non_declare` (actual flow with no matching reference -- drift),
   `manquant` (reference declared, no actual rule implements it), and
   `large_ouvert` (K8s default-allow OR a `0.0.0.0/0` SG peer --
   hardening).

3. **Actual-flow inputs.** Perimeter actual flows come from R1's
   `PerimeterSecurityGroupsForCluster` and the SG rules' CIDR peers;
   internal actual flows come from the K8s NetworkPolicies of ADR-0034.
   NetworkPolicy selector-to-workload resolution is intentionally coarse
   in R2 (summarized by name; precise selector resolution deferred).

4. **API.** `GET /v1/clusters/{id}/flow-matrix` (read scope, gated by
   `flow_matrix_enabled` -> 409 when off). Flow-reference CRUD plus YAML
   import (replace-all, OQ-1) / export (editor scope). Admin
   endpoint-group CRUD (admin scope, hand-written, not in OpenAPI -- like
   `/v1/admin/settings`). The flow-matrix and flow-reference paths are
   documented in OpenAPI but excluded from codegen (hand-written
   handlers, following the ADR-0029 applications precedent).

5. **UI.** A top-level "Flows" tool in the sidebar (between Search and
   EOL) with a cluster picker, perimeter + internal synthesis panels
   (state pills), state/direction filters, a reference-matrix editor
   (create / delete + YAML import/export), and an admin endpoint-groups
   page. Gating follows the EOL precedent: the nav item is always shown
   (nav is not flag-gated, and `/v1/admin/settings` is admin-only), and
   the page renders a "feature disabled" banner when the backend returns
   409.

## Consequences

- Read-time compute keeps the feature simple and always fresh: no engine,
  no `flow_cells` table, no per-tick cap. Work is bounded per cluster.
- The P1 per-asset "Network rules" tabs (ADR-0034) remain reachable via
  workload / VM detail pages. A synthesis-to-P1 deep link is deferred:
  the synthesis carries asset names, not ids.
- Evidence outputs -- extracts, drift metrics, audit events -- land in R3
  (planned ADR-0037).
- Migrations are renumbered relative to the umbrella spec because the
  toggle landed in R1 as 00052; R2 therefore starts at 00054.

## References

- Umbrella spec: `docs/superpowers/specs/2026-06-06-cluster-flow-matrix-design.md`
- ADR-0034: flow matrix P1 -- NetworkPolicy + provider-neutral SG inventory
- ADR-0035: flow matrix R1 -- cluster-perimeter SG collection + toggle
- ADR-0022: image-versions enricher / `internal/eolagg` read-time pattern
- ADR-0029: applications -- codegen-excluded hand-written endpoints
- ADR-0037 (planned): R3 -- evidence outputs (drift audit_events, Prometheus, extracts)
