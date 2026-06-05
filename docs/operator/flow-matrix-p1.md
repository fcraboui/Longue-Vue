# Flow Matrix -- Phase 1 operator runbook

P1 ships the **inventory** layer: every collected K8s NetworkPolicy + cloud
SG is now visible via API and on the workload + VM detail pages. No matrix,
no intent, no drift yet (those land in P2 / P3).

## What changed for operators

1. **Workload detail -> "Network rules" tab**: every NetworkPolicy selecting
   the workload, with rules grouped by direction.
2. **VM detail -> "Network rules" tab**: every attached security group, with
   rules.
3. **Day-one wide-open banner**: a workload with NO matching NetworkPolicy
   shows a yellow banner. K8s default-allow applies; this will surface as
   `wide_open_no_netpol` once P2 ships.

## What deployers must check

- The K8s collector now lists `networking.k8s.io/networkpolicies`. The
  ClusterRole in both `charts/longue-vue` and `charts/longue-vue-collector`
  was updated. Helm upgrade picks this up automatically.
- The Outscale vm-collector now emits canonical SG payloads. Pre-deploy
  VM rows (no `schema_version`) will show a "stale" banner on the
  Network rules tab until the next collector tick.
- AWS / OVH / Scaleway provider stubs exist as `panic("not wired")`.
  They cannot be configured at runtime yet; per-provider wiring lands in
  later phases.

## Endpoints introduced

- `GET /v1/network-policies?cluster_id=&namespace_id=` -- cursor paginated
- `GET /v1/network-policies/{id}` -- with embedded rules
- `GET /v1/security-groups?cloud_account_id=` -- cursor paginated
- `GET /v1/security-groups/{id}` -- with embedded rules
- `GET /v1/workloads/{id}/network-rules` -- derived
- `GET /v1/virtual-machines/{id}/network-rules` -- derived

All endpoints require `read` scope. No endpoint mutates state.

## Smoke-test after rollout

1. `kubectl apply -f` a NetworkPolicy in any namespace -> `GET /v1/network-policies?cluster_id=...` lists it within one collector interval.
2. Open a workload detail page selected by the NetPol -> "Network rules" tab shows it.
3. Open a workload detail page with no matching NetPol -> yellow banner appears.
4. Open a VM detail page -> SGs render with proper protocol/port/peer formatting.
