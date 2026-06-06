# 35. Flow matrix -- R1: cluster-perimeter security-group collection

Date: 2026-06-06

Status: Accepted (R1 of the cluster-flow-matrix redesign; see ADR-0036 and ADR-0037 for follow-ups)

## Context

ADR-0034 (P1 inventory) gave longue-vue a canonical model of K8s
NetworkPolicies and cloud security groups, but it captured SGs only for
assets that live in `virtual_machines`. The VMs that back a cluster's
Kubernetes nodes are deliberately *not* inventoried there: the VM
collector dedups them against `nodes.provider_id` (ADR-0015), so they
never become `virtual_machines` rows. Their security groups -- the SGs
that actually protect the cluster's nodes -- were therefore never stored,
and the per-VM SG sweep purged any that briefly appeared.

SecNumCloud ss.10/ss.11 require the cluster *perimeter* in the matrice de
flux: the flux périphériques are exactly the SGs attached to the cluster's
node VMs. This ADR records R1 of the cluster-flow-matrix redesign whose
umbrella design is at
`docs/superpowers/specs/2026-06-06-cluster-flow-matrix-design.md`.

## Decision

R1 (perimeter SG collection) ships:

1. **`flow_matrix_enabled` settings toggle** (migration 00052),
   env-seeded from `LONGUE_VUE_FLOW_MATRIX_ENABLED`, default off (the
   EOL/MCP toggle pattern). It gates the enriched persistence
   server-side.

2. **`vm_security_group_attachments` table** (migration 00053) keyed
   `(cloud_account_id, provider_vm_id, provider_sg_id)`. It is
   independent of `virtual_machines` so node-VM SG links can be recorded
   without inventorying node VMs as VMs.

3. **Account-wide SG enumeration in the vm-collector.** The collector now
   calls `Provider.GetSecurityGroups` once per tick (account-wide) and
   emits a `(provider_vm_id -> provider_sg_id)` attachment for *every*
   listed VM -- node or not -- built before the VM pre-filter, so the
   perimeter is captured even for node VMs that are filtered out of the
   inventory. Both the account-wide SGs and the attachments ride on the
   enriched SG-sweep body. On `GetSecurityGroups` failure the collector
   folds the attachment SG ids into the sweep seen-set, so the server's
   delete-unseen sweep cannot purge node perimeter SGs that tick.

4. **Gated server-side persistence.** The sweep endpoint persists the
   account-wide SGs + attachments only when `flow_matrix_enabled` is on.
   The collector always sends the enriched body; the server alone gates,
   so no DB-flag coupling leaks into the push collector. Per-VM and
   account-wide SG persistence share one helper (`upsertCanonicalSG`).

5. **`PerimeterSecurityGroupsForCluster` store query** maps a cluster to
   its perimeter SGs by joining `nodes` to `vm_security_group_attachments`
   on `nodes.provider_id LIKE %provider_vm_id%` (the same substring match
   the VM dedup already trusts, ADR-0015) and then to `security_groups`.

The SG-sweep route path
(`POST /v1/ingest/cloud-accounts/{id}/security-groups/sweep`) is
unchanged from ADR-0034 -- only its request body grew -- so the ingest
gateway allowlist needed no new entry.

## Consequences

- Node-only SGs now survive reconcile: the sweep is account-wide rather
  than per-VM-seen, and the failure-fold protects the perimeter on a
  transient enumeration error.
- One new table + one new toggle. The toggle was pulled from the spec's
  R2 slot into R1 because R1's persistence gate depends on it; the R1
  migrations are therefore renumbered (00052 toggle, 00053 attachments)
  and R2 will start at 00054.
- AWS / OVH / Scaleway provider implementations remain skeleton stubs
  (ADR-0034); only Outscale + the fake provider enumerate SGs.
- The synthesis/comparison UI and the read-time comparator land in R2
  (planned ADR-0036); the SNC evidence outputs land in R3 (planned
  ADR-0037).

## References

- Umbrella spec: `docs/superpowers/specs/2026-06-06-cluster-flow-matrix-design.md`
- ADR-0034: flow matrix P1 -- NetworkPolicy + provider-neutral SG inventory
- ADR-0015: cloud VM collector + node `provider_id` dedup
- ADR-0036 (planned): R2 -- comparison engine + synthesis UI
- ADR-0037 (planned): R3 -- evidence outputs (drift audit_events, Prometheus, extracts)
