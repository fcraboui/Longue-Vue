# 34. Flow matrix -- Phase 1 (Inventory): NetworkPolicy collection + provider-neutral SG model

Date: 2026-06-05

Status: Accepted (Phase 1 of three; see ADR-0035 and ADR-0036 for follow-ups)

## Context

Longue-Vue inventories Kubernetes workloads, cloud VMs, and the
applications that group them, but had no model of network reachability
posture between those assets. K8s NetworkPolicies were not collected at
all; cloud security groups were stored as opaque provider JSONB on
`virtual_machines.security_groups` (Outscale-only). SecNumCloud ss.10/ss.11
require a documented "matrice de flux" with proof that the actual posture
matches it.

This ADR records Phase 1 of the three-phase rollout described in the
umbrella design at `docs/superpowers/specs/2026-06-05-flow-matrix-design.md`.

## Decision

Phase 1 (Inventory) ships:

1. **K8s NetworkPolicy collection** as a new resource handler inside the
   existing `internal/collector/` Go package -- same `client-go` transport,
   same per-cluster/per-namespace tick, same reconcile contract (only run
   after a successful list). Persists to `network_policies` +
   `network_policy_rules` (migration 00050).

2. **Provider-neutral canonical SG model** in
   `internal/vmcollector/provider/sgmodel.go` (types: `SecurityGroup`,
   `SecurityGroupRule`, `SGPeer`, `VMSecurityGroupsPayload`;
   `SGSchemaVersion = 1`). Each provider impl normalizes native SG format
   into these types before sending. Outscale is wired now; AWS / OVH /
   Scaleway exist as `panic("not wired")` skeletons with native-shape
   fixtures committed alongside the canonical contract.

3. **Server-side canonical persistence** into `security_groups` +
   `security_group_rules` (migration 00051), deduped per
   `(cloud_account_id, provider_sg_id)`. The VM ingest handler upserts
   per-VM after the existing VM upsert; an account-level sweep endpoint
   `POST /v1/ingest/cloud-accounts/{id}/security-groups/sweep` removes
   SGs not seen this tick. The vm-collector calls it at end-of-tick.

4. **Read-only API surface**: six P1 endpoints (`GET /v1/network-policies`
   list + single-get with embedded rules; `GET /v1/security-groups` same
   pair; `GET /v1/workloads/{id}/network-rules` and
   `GET /v1/virtual-machines/{id}/network-rules` derived per-asset).

5. **UI "Network rules" tab** on workload + VM detail pages.

6. **Helm RBAC update**: `networkpolicies` added to the
   `networking.k8s.io` group rule in both
   `charts/longue-vue/templates/clusterrole.yaml` and
   `charts/longue-vue-collector/templates/clusterrole.yaml`. Verb stays
   `list` only.

## Consequences

- `virtual_machines.security_groups` JSONB now carries `schema_version: 1`;
  pre-existing rows render as `stale: true` on the API and tab until the
  next collector tick overwrites.
- Six new tables in the database (4 for collection: `network_policies`,
  `network_policy_rules`, `security_groups`, `security_group_rules`; the
  other two land in P2).
- The `Provider` interface signature changes: `GetSecurityGroups` now
  returns `[]SecurityGroup` (was opaque JSON). The Outscale impl and the
  fake provider follow; the wire payload in the vm-collector apiclient
  wraps the result as `VMSecurityGroupsPayload`.
- AWS / OVH / Scaleway provider stubs panic at runtime -- they exist only
  to keep the canonical contract honest at compile time and to ship the
  native-shape fixtures for later real implementations.

## References

- Umbrella spec: `docs/superpowers/specs/2026-06-05-flow-matrix-design.md`
- P1 implementation plan: `docs/superpowers/plans/2026-06-05-flow-matrix-p1-inventory.md`
- ADR-0035 (planned): P2 -- comparison engine + per-Application intent
- ADR-0036 (planned): P3 -- evidence outputs (drift audit_events, Prometheus, extracts)
