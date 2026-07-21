---
title: "ADR-0043: Kyverno policy inventory and Policies view"
status: "Proposed"
date: "2026-07-21"
authors: "Frederic CRABOUILLET"
tags: ["architecture", "decision", "collector", "ui", "kyverno", "policy"]
supersedes: ""
superseded_by: ""
---

# ADR-0043: Kyverno policy inventory and Policies view

## Status

**Proposed** | Accepted | Rejected | Superseded | Deprecated

- **Date:** 2026-07-21
- **Supersedes:** none
- **Superseded by:** none

## Context

Operators need visibility into Kyverno policy posture across all
catalogued clusters — which policies exist, where they apply, whether
they enforce or audit, and which resources pass or fail. Today,
longue-vue has no Kyverno awareness at all: the collector ignores
`ClusterPolicy`, `Policy`, `PolicyReport`, and `ClusterPolicyReport`
CRDs, and the UI has no page for them.

The SecNumCloud compliance evidence (SNC §8.3) increasingly requires
showing that admission-control and audit policies are consistently
deployed across environments. A dedicated Policies view lets the
operator compare policy posture per environment and per zone at a
glance.

**Key constraints:**

- Kyverno is the sole admission/audit policy engine in scope (ADR-0001
  ecosystem). Other engines (OPA Gatekeeper, Kubewarden) are out of
  scope for v1.
- The collector already walks every namespace per tick; adding two more
  list calls (`ClusterPolicy`, `PolicyReport` / `ClusterPolicyReport`)
  is marginal cost.
- The existing FK chain (`clusters` → `namespaces` → children) and
  reconcile pattern (ADR-0009, ADR-0033) apply directly: a policy is
  either cluster-scoped or namespace-scoped, and both live under a
  cluster.

## Decision

### 1. New database tables

Add two tables following the established pattern (cf. migration 00050
for `network_policies`):

**`cluster_policies`** — one row per collected Kyverno `ClusterPolicy`
or namespaced `Policy`.

| Column              | Type                                          | Notes                                                                                                                                                                                                                                                                                 |
| ------------------- | --------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `id`                | UUID PK                                       | `gen_random_uuid()`                                                                                                                                                                                                                                                                   |
| `cluster_id`        | UUID FK → `clusters(id) ON DELETE CASCADE`    | owning cluster                                                                                                                                                                                                                                                                        |
| `namespace_id`      | UUID FK → `namespaces(id) ON DELETE SET NULL` | NULL for ClusterPolicy (cluster-scoped)                                                                                                                                                                                                                                               |
| `name`              | TEXT                                          | policy name                                                                                                                                                                                                                                                                           |
| `resource_type`     | TEXT                                          | `ClusterPolicy` or `Policy`                                                                                                                                                                                                                                                           |
| `scope`             | TEXT                                          | `cluster` or `namespace` (denormalised from `resource_type` for sort/filter)                                                                                                                                                                                                          |
| `description`       | TEXT                                          | from `metadata.annotations["policies.kyverno.io/description"]`; fallback: first non-empty line of `spec.rules[0].validate.message`                                                                                                                                                    |
| `category`          | TEXT                                          | from `metadata.annotations["policies.kyverno.io/category"]`                                                                                                                                                                                                                           |
| `severity`          | TEXT                                          | from `metadata.annotations["policies.kyverno.io/severity"]`                                                                                                                                                                                                                           |
| `action`            | TEXT                                          | `Enforce` or `Audit` — from `spec.validationFailureAction` (string, or object `.action` per §5)                                                                                                                                                                                       |
| `failure_policy`    | TEXT                                          | `Fail` or `Ignore` — from `spec.webhookConfiguration.failurePolicy`                                                                                                                                                                                                                   |
| `background`        | BOOLEAN                                       | from `spec.background` (default true)                                                                                                                                                                                                                                                 |
| `rule_types`        | TEXT[]                                        | distinct rule types across `spec.rules[]`: subset of `{validate, mutate, generate, verifyImages}`                                                                                                                                                                                     |
| `rules_count`       | INTEGER                                       | `len(spec.rules)`                                                                                                                                                                                                                                                                     |
| `target_resources`  | TEXT[]                                        | distinct union of `spec.rules[].match.any[].resources.kinds[]` and `spec.rules[].match.all[].resources.kinds[]` — e.g. `{Pod, Deployment, Namespace}`                                                                                                                                 |
| `key_exclusions`    | TEXT[]                                        | distinct union of `spec.rules[].exclude.any[].resources.kinds[]`, `spec.rules[].exclude.all[].resources.kinds[]`, `spec.rules[].exclude.any[].resources.namespaces[]`, and `spec.rules[].exclude.all[].resources.namespaces[]` (capped at 10; prefixed: `kind:Pod`, `ns:kube-system`) |
| `ready`             | BOOLEAN                                       | from `status.conditions[?(@.type=="Ready")].status == "True"`                                                                                                                                                                                                                         |
| `annotations`       | JSONB                                         | full annotations for drill-down (subject, scored, minversion, …)                                                                                                                                                                                                                      |
| `spec_raw`          | JSONB                                         | full spec for forensic access (mirrors `network_policies.spec_raw`)                                                                                                                                                                                                                   |
| `reconcile_seen_at` | TIMESTAMPTZ                                   | standard reconcile timestamp                                                                                                                                                                                                                                                          |

UNIQUE on `(cluster_id, namespace_id, name)` — same composite key as
other namespaced entities; NULL `namespace_id` for cluster-scoped
policies. Because PostgreSQL treats NULL ≠ NULL in UNIQUE constraints
by default, two cluster-scoped policies with the same name in the same
cluster would **not** violate this constraint. To close that gap, two
partial unique indexes are used:

```sql
CREATE UNIQUE INDEX uq_cluster_policies_cluster
  ON cluster_policies (cluster_id, name)
  WHERE namespace_id IS NULL;
CREATE UNIQUE INDEX uq_cluster_policies_ns
  ON cluster_policies (cluster_id, namespace_id, name)
  WHERE namespace_id IS NOT NULL;
```

**`policy_reports`** — one row per collected `PolicyReport` or
`ClusterPolicyReport`, carrying the summary counts and the results
array.

| Column              | Type                                          | Notes                                                 |
| ------------------- | --------------------------------------------- | ----------------------------------------------------- |
| `id`                | UUID PK                                       | `gen_random_uuid()`                                   |
| `cluster_id`        | UUID FK → `clusters(id) ON DELETE CASCADE`    |                                                       |
| `namespace_id`      | UUID FK → `namespaces(id) ON DELETE SET NULL` | NULL for ClusterPolicyReport                          |
| `name`              | TEXT                                          | report object name                                    |
| `scope_kind`        | TEXT                                          | `Namespace`, `Pod`, etc. from `scope.kind` (nullable) |
| `scope_name`        | TEXT                                          | from `scope.name` (nullable)                          |
| `summary_pass`      | INTEGER                                       |                                                       |
| `summary_fail`      | INTEGER                                       |                                                       |
| `summary_warn`      | INTEGER                                       |                                                       |
| `summary_error`     | INTEGER                                       |                                                       |
| `summary_skip`      | INTEGER                                       |                                                       |
| `results_raw`       | JSONB                                         | full `results[]` array for drill-down                 |
| `reconcile_seen_at` | TIMESTAMPTZ                                   |                                                       |

UNIQUE on `(cluster_id, namespace_id, name)`.

### 2. Collector changes

Extend `internal/collector/` to list four Kyverno CRDs per tick:

```text
ClusterPolicy           → cluster_policies   (cluster-scoped, one list call per cluster)
Policy                  → cluster_policies   (namespaced, one list call per namespace)
PolicyReport            → policy_reports     (namespaced, one list call per namespace)
ClusterPolicyReport     → policy_reports     (cluster-scoped, one list call per cluster)
```

Collection is gated by a new setting `policies_enabled` (default
`false`), seeded from `LONGUE_VUE_POLICIES_ENABLED`. Rationale:
clusters without Kyverno should not pay the API-round-trip cost; the
setting also lets operators opt in gradually.

RBAC: add `clusterpolicies`, `policies`, `policyreports`,
`clusterpolicyreports` (all under `kyverno.io` and `wgpolicyk8s.io`
API groups) to both the in-process and push-collector ClusterRoles.

Reconcile follows the established pattern (ADR-0009):
`DeleteClusterPoliciesNotIn(cluster_id, seen_ids)` after each
successful tick; same for policy reports.

### 3. REST API

New list endpoint: `GET /v1/cluster-policies`

Query parameters follow ADR-0042 (uniform list contract):

| Param            | Type   | Notes                                                                                                       |
| ---------------- | ------ | ----------------------------------------------------------------------------------------------------------- |
| `limit`          | int    | cursor page size                                                                                            |
| `cursor`         | string | tagged base64-JSON cursor                                                                                   |
| `name`           | string | substring / glob filter on policy name                                                                      |
| `sort`           | string | allowlist: `name`, `action`, `background`, `severity`, `rules_count`, `failure_policy`, `category`, `ready` |
| `order`          | string | `asc` (default) / `desc`                                                                                    |
| `cluster_id`     | UUID   | filter by cluster                                                                                           |
| `namespace_id`   | UUID   | filter by namespace                                                                                         |
| `resource_type`  | string | `ClusterPolicy` / `Policy`                                                                                  |
| `action`         | string | `Enforce` / `Audit`                                                                                         |
| `failure_policy` | string | `Fail` / `Ignore`                                                                                           |
| `severity`       | string | `critical` / `high` / `medium` / `low`                                                                      |
| `category`       | string | substring filter on category                                                                                |

Response: `{items: [...], next_cursor: "..." | null}` per ADR-0042.

A second endpoint `GET /v1/policy-reports` follows the same contract
with its own sort/filter allowlist, plus a `cluster_policy_name`
filter to find reports for a specific policy.

### 4. UI — Policies view

**Navigation:** insert a "Policies" entry in the sidebar between "Pods"
and "Services" in `ui/src/App.tsx`, using a new `PolicyIcon` SVG
component.

**Table columns** (all sortable per ADR-0042, reorderable via
drag-and-drop, resizable):

| Column                  | Source                                                                                                              | Link / rendering                                                                                                                   |
| ----------------------- | ------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| **Name**                | `cluster_policies.name`                                                                                             | Plain text (policy name)                                                                                                           |
| **Resource Type**       | `cluster_policies.resource_type`                                                                                    | Badge (`ClusterPolicy` / `Policy`)                                                                                                 |
| **Env**                 | `clusters.environment` (JOIN)                                                                                       | Plain text                                                                                                                         |
| **Cluster**             | `clusters.id` + `clusters.name` (JOIN)                                                                              | Hyperlink to `https://inventory.tooling.cloudgouv-eu-west-1.numspot.internal/ui/clusters/${cluster_id}`                            |
| **Zone**                | Derived from cluster name convention (`<platform>-<env>-std-<zone>`) or `clusters.annotations["zone"]` if available | Plain text                                                                                                                         |
| **Category / Severity** | `cluster_policies.category` + `cluster_policies.severity`                                                           | Colour-coded badge: critical=red, high=orange, medium=yellow, low=green, info=grey; category as prefix                             |
| **Rule Types**          | `cluster_policies.rule_types`                                                                                       | Pill badges: `validate`, `mutate`, `generate`, `verifyImages` — colour per type                                                    |
| **Action**              | `cluster_policies.action`                                                                                           | Colour-coded badge: red = `Enforce`, blue = `Audit`                                                                                |
| **Failure Policy**      | `cluster_policies.failure_policy`                                                                                   | Badge: red = `Fail` (request rejected on webhook error), grey = `Ignore`                                                           |
| **Target Resources**    | `cluster_policies.target_resources`                                                                                 | Comma-separated list with overflow tooltip (e.g. `Pod, Deployment, Namespace`)                                                     |
| **Key Exclusions**      | `cluster_policies.key_exclusions`                                                                                   | Comma-separated list with overflow tooltip; muted style (e.g. `ns:kube-system`); hover shows full list from `spec_raw` when capped |
| **Background Scan**     | `cluster_policies.background`                                                                                       | Boolean icon (checkmark / dash)                                                                                                    |
| **Ready**               | `cluster_policies.ready`                                                                                            | Status dot: green = `true`, red = `false`, grey = unknown                                                                          |
| **P99 Latency**         | Prometheus (§7)                                                                                                     | Numeric with ms unit; fetched at render time from configured Prometheus datasource                                                 |
| **Block Rate**          | Prometheus (§7)                                                                                                     | Requests/min blocked in Enforce mode; fetched at render time                                                                       |
| **Error Rate**          | Prometheus (§7)                                                                                                     | Errors/min during rule evaluation; fetched at render time                                                                          |
| **Non-Conformity**      | Prometheus + PolicyReport (§7)                                                                                      | Failing resources count from PolicyReport summary; fetched at render time                                                          |

**PolicyReport toggle:** a toggle button (show/hide) in the table
toolbar. When enabled, a nested row or expandable section under each
policy shows the associated `PolicyReport` / `ClusterPolicyReport`
entries with pass/fail/warn/error/skip counts and a link to the full
results. Default state: hidden (collapsed).

**Pagination:** cursor-based, following `EntityListPage<T>` (same as
every other list view). Column sort, column reorder, and column resize
use the existing `SortHeader` + `column_filters` infrastructure.

### 5. How to retrieve Kyverno policies (collector logic)

The collector retrieves Kyverno policies using the Kubernetes API via
client-go dynamic or typed clients. The Kyverno CRDs are:

- **`ClusterPolicy`** (`kyverno.io/v1`, cluster-scoped, short: `cpol`)
- **`Policy`** (`kyverno.io/v1`, namespaced, short: `pol`)
- **`ClusterPolicyReport`** (`wgpolicyk8s.io/v1alpha2`, cluster-scoped, short: `cpolr`)
- **`PolicyReport`** (`wgpolicyk8s.io/v1alpha2`, namespaced, short: `polr`)

Per-tick retrieval:

```text
1. If policies_enabled is false → skip entirely.

2. Cluster-scoped (one call per cluster):
   - GET /apis/kyverno.io/v1/clusterpolicies
     → map each item to a cluster_policies row (namespace_id = NULL, scope = "cluster")

   - GET /apis/wgpolicyk8s.io/v1alpha2/clusterpolicyreports
     → map each item to a policy_reports row (namespace_id = NULL)

3. Namespace-scoped (one call per namespace, inside the existing namespace loop):
   - GET /apis/kyverno.io/v1/namespaces/{ns}/policies
     → map each item to a cluster_policies row (namespace_id set, scope = "namespace")

   - GET /apis/wgpolicyk8s.io/v1alpha2/namespaces/{ns}/policyreports
     → map each item to a policy_reports row (namespace_id set)

4. Upsert each row; reconcile after the successful list.
```

Field extraction from a `ClusterPolicy` / `Policy` object:

| CMDB column        | K8s field path                                                                                                                                                                                                                                                                                                        |
| ------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `name`             | `.metadata.name`                                                                                                                                                                                                                                                                                                      |
| `resource_type`    | `.kind` (`ClusterPolicy` / `Policy`)                                                                                                                                                                                                                                                                                  |
| `scope`            | `"cluster"` if kind=ClusterPolicy, `"namespace"` if kind=Policy                                                                                                                                                                                                                                                       |
| `description`      | `.metadata.annotations["policies.kyverno.io/description"]`; fallback: first non-empty line of `.spec.rules[0].validate.message`                                                                                                                                                                                       |
| `category`         | `.metadata.annotations["policies.kyverno.io/category"]`                                                                                                                                                                                                                                                               |
| `severity`         | `.metadata.annotations["policies.kyverno.io/severity"]`                                                                                                                                                                                                                                                               |
| `action`           | `.spec.validationFailureAction` — if string, use directly (`"Enforce"` or `"Audit"`); if object (Kyverno ≥1.10), read `.action` sub-field                                                                                                                                                                             |
| `failure_policy`   | `.spec.webhookConfiguration.failurePolicy` (string: `"Fail"` or `"Ignore"`; fallback: deprecated `.spec.failurePolicy`)                                                                                                                                                                                               |
| `background`       | `.spec.background` (boolean, default true when absent)                                                                                                                                                                                                                                                                |
| `rule_types`       | distinct set of rule types present in `.spec.rules[]`: keys `validate`, `mutate`, `generate`, `verifyImages` — stored as TEXT[]                                                                                                                                                                                       |
| `rules_count`      | `len(.spec.rules)`                                                                                                                                                                                                                                                                                                    |
| `target_resources` | distinct union of `.spec.rules[].match.any[].resources.kinds[]` and `.spec.rules[].match.all[].resources.kinds[]`                                                                                                                                                                                                     |
| `key_exclusions`   | distinct union of `spec.rules[].exclude.any[].resources.kinds[]` (prefixed `kind:`), `spec.rules[].exclude.all[].resources.kinds[]` (prefixed `kind:`), `spec.rules[].exclude.any[].resources.namespaces[]` (prefixed `ns:`), and `spec.rules[].exclude.all[].resources.namespaces[]` (prefixed `ns:`) — capped at 10 |
| `ready`            | `true` if `status.conditions[type=="Ready"].status == "True"`; `false` otherwise; `NULL` if status is absent                                                                                                                                                                                                          |
| `annotations`      | `.metadata.annotations` (full map as JSONB)                                                                                                                                                                                                                                                                           |
| `spec_raw`         | `.spec` (full spec as JSONB)                                                                                                                                                                                                                                                                                          |

Field extraction from a `PolicyReport` / `ClusterPolicyReport`:

| CMDB column     | K8s field path                   |
| --------------- | -------------------------------- |
| `name`          | `.metadata.name`                 |
| `scope_kind`    | `.scope.kind` (nullable)         |
| `scope_name`    | `.scope.name` (nullable)         |
| `summary_pass`  | `.summary.pass`                  |
| `summary_fail`  | `.summary.fail`                  |
| `summary_warn`  | `.summary.warn`                  |
| `summary_error` | `.summary.error`                 |
| `summary_skip`  | `.summary.skip`                  |
| `results_raw`   | `.results` (full array as JSONB) |

### 6. Zone derivation

The "Zone" column is not stored as a first-class field on policies. It
is derived at read time from the cluster's identity:

- If `clusters.annotations` contains a `"zone"` key, use that value.
- Otherwise, parse the cluster name using the convention
  `<platform>-<env>-std-<zone>` (e.g., `zex-prod-std-main` → zone
  `main`, `zex-prod-std-dmz` → zone `dmz`). The zone is the
  fourth hyphen-delimited segment when the name matches the pattern
  `*-std-*`. Clusters that do not match fall back to an empty zone.

This avoids storing redundant data while covering 100% of the current
naming convention across all KUBECONFIG contexts.

### 7. Prometheus-derived policy metrics

Four columns in the Policies table are derived from Kyverno's
Prometheus metrics, not from the K8s API or the CMDB store. These are
**fetched at render time** by the UI from the Prometheus datasource
configured in longue-vue settings. They are not stored in PostgreSQL
and are therefore not sortable server-side (ADR-0042 §4: computed
columns are not sortable).

A new setting `policy_prometheus_url` (default: empty = disabled)
enables the Prometheus integration. When empty, the four metric
columns render as "—".

| Metric column      | Prometheus metric                                   | Type      | PromQL                                                                                                                                                                                                                                                          |
| ------------------ | --------------------------------------------------- | --------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **P99 Latency**    | `kyverno_policy_execution_duration_seconds`         | Histogram | `histogram_quantile(0.99, sum(rate(kyverno_policy_execution_duration_seconds_bucket{policy_name="<name>"}[5m])) by (le, rule_name))`                                                                                                                            |
| **Block Rate**     | `kyverno_policy_results`                            | Counter   | `sum(rate(kyverno_policy_results{rule_result="FAIL", policy_validation_mode="enforce", rule_execution_cause="admission_request", policy_name="<name>"}[5m]))`                                                                                                   |
| **Error Rate**     | `kyverno_policy_results`                            | Counter   | `max(0, sum(rate(kyverno_policy_results{rule_result="FAIL", policy_name="<name>", rule_execution_cause="admission_request"}[5m])) - sum(rate(kyverno_policy_results{rule_result="PASS", policy_name="<name>", rule_execution_cause="admission_request"}[5m])))` |
| **Non-Conformity** | PolicyReport `summary.fail` (CMDB `policy_reports`) | DB read   | `SELECT SUM(summary_fail) FROM policy_reports WHERE cluster_id = $1 AND (name = $2 OR name LIKE $3)` where `$3` = `%-<policy_name>`                                                                                                                             |

**Key design decisions:**

- **Why not store metrics in PostgreSQL?** Prometheus counters are
  cumulative and high-cardinality; storing them in the CMDB would
  duplicate time-series data and require a polling enricher. The
  render-time fetch keeps the CMDB a CMDB and lets Prometheus handle
  rate/histogram math natively.
- **Why is non-conformity from PolicyReport, not Prometheus?** The
  PolicyReport `summary.fail` is a point-in-time count of non-compliant
  resources — exactly what the operator needs. The Prometheus counter
  `kyverno_policy_results{rule_result="FAIL"}` is cumulative and
  requires a window function to approximate; the PolicyReport is the
  authoritative source for current compliance state.
- **Batch fetch strategy:** the UI sends a single Prometheus
  `/api/v1/query` request per page load with a vector selector
  aggregating all visible policies (not one request per row). The
  response is indexed by `policy_name` label and merged into the table
  rows client-side.
- **Cluster correlation:** Kyverno metrics are emitted by the Kyverno
  controller Pod. The Prometheus datasource is typically cluster-local
  (Thanos/Mimir federated). When `policy_prometheus_url` points to a
  centralised Thanos, the `cluster` label (or equivalent) is used to
  correlate metrics with CMDB clusters. If no cluster label exists,
  the metrics are shown without per-cluster breakdown (aggregate only).
- **Error Rate approximation:** `kyverno_policy_results` only exposes
  `rule_result="PASS"` and `rule_result="FAIL"` (uppercase). There is
  no `rule_result="error"` value. The Error Rate column therefore
  approximates error rate as `max(0, FAIL_rate − PASS_rate)` for
  admission requests, which captures policies that fail without a
  corresponding pass (webhook errors, timeouts). The `max(0, …)` clamp
  prevents negative values when PASS rate exceeds FAIL rate (the
  common case for healthy policies). If this proves insufficient, a
  follow-up can use `kyverno_controller_reconcile_total` or log-based
  metrics instead.
- **`verifyImages` in Prometheus:** The `rule_type` label in
  `kyverno_policy_results` only supports `"validate"`, `"mutate"`,
  `"generate"`. Verify-images rules are counted under `"validate"` in
  Prometheus. The DB `rule_types` column stores `verifyImages`
  separately (from the K8s spec), but Prometheus metrics for such
  policies will show only `"validate"`.

**Kyverno `kyverno_policy_results` label reference:**

| Label                        | Values                                                                                                           |
| ---------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| `policy_name`                | policy name                                                                                                      |
| `policy_namespace`           | namespace (`"-"` for ClusterPolicy)                                                                              |
| `policy_validation_mode`     | `"enforce"`, `"audit"`                                                                                           |
| `policy_type`                | `"cluster"`, `"namespaced"`                                                                                      |
| `policy_background_mode`     | `"true"`, `"false"`                                                                                              |
| `rule_name`                  | rule name within the policy                                                                                      |
| `rule_result`                | `"PASS"`, `"FAIL"` (uppercase; per Kyverno v1.x — verify against deployed version)                               |
| `rule_type`                  | `"validate"`, `"mutate"`, `"generate"` (note: `verifyImages` rules are counted under `"validate"` in Prometheus) |
| `rule_execution_cause`       | `"admission_request"`, `"background_scan"`                                                                       |
| `resource_kind`              | e.g. `"Pod"`, `"Deployment"`                                                                                     |
| `resource_namespace`         | namespace of the audited resource                                                                                |
| `resource_request_operation` | `"create"`, `"update"`, `"delete"`                                                                               |

## Consequences

### Positive

- **POS-001**: Operators gain a single-pane view of Kyverno policy
  posture across all environments and zones — no more per-cluster
  `kubectl get cpol` runs.
- **POS-002**: The PolicyReport toggle surfaces compliance results
  (pass/fail counts) alongside policy definitions without cluttering the
  default view.
- **POS-004**: The cluster link (to `/ui/clusters/<uuid>`) keeps the
  existing navigation pattern consistent with other list views.
- **POS-005**: Gating collection behind `policies_enabled` avoids API
  round-trip cost on clusters without Kyverno and allows incremental
  rollout.
- **POS-006**: Reusing the ADR-0042 contract (cursor pagination, sort,
  name filter) means zero UI-level pagination/sort rewrite — the
  `EntityListPage<T>` component is reused as-is.
- **POS-007**: Storing `spec_raw` and `results_raw` as JSONB mirrors the
  `network_policies.spec_raw` pattern and supports future drill-down
  pages without schema changes.
- **POS-008**: Prometheus-derived columns (p99 latency, block rate,
  error rate) give operators runtime observability alongside the
  static policy definition — combining inventory and telemetry in one
  view without duplicating time-series data in PostgreSQL.
- **POS-009**: `failure_policy` surfaces the webhook failure mode
  (`Fail` vs `Ignore`), which is critical for understanding blast
  radius: a policy with `Fail` blocks the entire admission request on
  webhook error, while `Ignore` silently passes it.
- **POS-010**: `rule_types` lets operators filter policies by what they
  do (validate, mutate, generate, verifyImages) — essential for
  understanding the admission-control surface at a glance.
- **POS-011**: `target_resources` and `key_exclusions` provide immediate
  visibility into what a policy selects and what it exempts, without
  needing to open the full spec.

### Negative

- **NEG-001**: Two additional list calls per namespace per tick
  (`Policy` + `PolicyReport`) increase collector API surface. On
  clusters with many namespaces, this is non-trivial. Mitigated by the
  `policies_enabled` gate and by the existing per-namespace loop that
  already makes calls for pods, workloads, and services.
- **NEG-002**: Zone derivation from cluster name is convention-dependent.
  If a cluster's name does not follow `<platform>-<env>-std-<zone>`, the
  zone column is empty. Mitigated by the `annotations["zone"]`
  fallback.
- **NEG-003**: `ClusterPolicy` and `Policy` share the same table
  (`cluster_policies`), distinguished by `resource_type` / `scope`. A
  cluster-scoped `ClusterPolicy` has `namespace_id = NULL`; a
  namespace-scoped `Policy` has `namespace_id` set to its namespace UUID.
  Therefore a `ClusterPolicy` named `foo` and a `Policy` named `foo` in
  namespace `bar` have different `(cluster_id, namespace_id, name)` tuples
  and coexist without collision. For duplicate cluster-scoped names
  (same name, same cluster, both with `namespace_id = NULL`), the
  standard UNIQUE constraint would not catch the violation because
  PostgreSQL treats NULL ≠ NULL. The two partial unique indexes
  (`uq_cluster_policies_cluster` and `uq_cluster_policies_ns`) close
  this gap and enforce uniqueness correctly for both scopes.
- **NEG-004**: The `description` field relies on a Kyverno annotation
  convention (`policies.kyverno.io/description`). Policies that omit
  this annotation fall back to the first non-empty line of
  `spec.rules[0].validate.message`, which is a best-effort heuristic
  and may produce unexpected text for policies whose first rule is not
  a validation rule.
- **NEG-005**: Prometheus-derived columns (p99 latency, block rate,
  error rate) are fetched at render time and are not sortable
  server-side. When Prometheus is unavailable, these columns render as
  "—". The render-time fetch also adds latency to the table load
  (one Prometheus query per page). Mitigated by the batch fetch
  strategy (single vector query) and by the graceful "—" fallback.
- **NEG-006**: `failure_policy` uses the deprecated
  `spec.failurePolicy` as a fallback when
  `spec.webhookConfiguration.failurePolicy` is absent (Kyverno < 1.11).
  If neither field is present, the column renders as empty. In
  practice, Kyverno always sets a default (`Fail`), but the edge case
  is documented.
- **NEG-007**: `key_exclusions` is capped at 10 entries to bound the
  array size. Policies with more than 10 exclusion rules will show a
  truncated list with a "+N more" indicator. The full exclusion list is
  available in `spec_raw` and rendered in a hover tooltip on the
  "Key Exclusions" cell — so operators see the cap in the cell and the
  complete list on mouse-over without opening a detail page.

## Alternatives Considered

### Separate tables for ClusterPolicy and Policy

- **ALT-001**: **Description**: Create `cluster_policies` for
  `ClusterPolicy` only and `namespace_policies` for `Policy`, mirroring
  how clusters and namespaces are separate tables.
- **ALT-002**: **Rejection Reason**: The two Kyverno CRDs share the
  exact same spec schema. A single table with a `resource_type`
  discriminator is simpler to query, paginate, and display in one
  unified table view. The UI needs a single list endpoint, not two.

### Collect PolicyReport results as individual rows

- **ALT-003**: **Description**: Instead of storing `results_raw` as
  JSONB, normalise each `PolicyReportResult` into its own row in a
  `policy_report_results` table with columns for `policy`, `rule`,
  `result`, `severity`, `resource_kind`, `resource_name`,
  `resource_namespace`.
- **ALT-004**: **Rejection Reason**: A single ClusterPolicyReport can
  contain thousands of results (one per audited resource per rule).
  Normalising into individual rows would explode the row count and
  complicate reconciliation. JSONB storage lets the UI render summary
  counts cheaply and lazy-load individual results on demand. If
  per-result querying becomes necessary, a SQL view over
  `jsonb_array_elements(results_raw)` can be added later.

### Real-time Kyverno API proxy instead of collection

- **ALT-005**: **Description**: Instead of collecting and storing
  policies in PostgreSQL, proxy live Kyverno API calls from the UI
  through longue-vue to each cluster's Kubernetes API.
- **ALT-006**: **Rejection Reason**: Violates the CMDB's core design
  principle (ADR-0001): longue-vue is a cache/snapshot, not a proxy.
  It must work when clusters are unreachable (air-gapped, VPN-down).
  The collector model preserves historical data and supports the
  `reconcile_seen_at` staleness signal.

### MCP-only exposure (no UI)

- **ALT-007**: **Description**: Expose `list_cluster_policies` and
  `get_cluster_policy` MCP tools only, without a UI page.
- **ALT-008**: **Rejection Reason**: The ADR-0006 rationale (UI for
  audit and curated metadata) applies directly: operators triage policy
  posture visually, not via API calls. The MCP tools will be added as
  a follow-up, but the UI is the primary interface.

## Implementation Notes

- **IMP-001**: Migration `00059_create_cluster_policies.sql` creates
  `cluster_policies` and `policy_reports` tables with indexes on
  `(cluster_id)`, `(namespace_id)`, and two partial unique indexes
  (`uq_cluster_policies_cluster` for NULL `namespace_id`,
  `uq_cluster_policies_ns` for non-NULL `namespace_id`) to correctly
  enforce uniqueness with NULL semantics. GIN index on `rule_types` and
  `target_resources` (array containment queries for filter).
- **IMP-002**: Add `policies_enabled` boolean to the `settings` table
  (migration `00060`). Seed from `LONGUE_VUE_POLICIES_ENABLED`.
- **IMP-003**: Add `policy_prometheus_url` text column to the `settings`
  table (same migration). Default: empty (disabled).
- **IMP-004**: Collector: add `ingestClusterPolicies` and
  `ingestPolicyReports` methods in `internal/collector/`. Gate behind
  `policies_enabled`. Add the four Kyverno CRDs to both ClusterRole
  templates. Extract `failure_policy`, `category`, `rule_types`,
  `target_resources`, `key_exclusions`, and `ready` during the K8s
  list→row mapping.
- **IMP-005**: Store: `internal/store/pg_cluster_policies.go` —
  `UpsertClusterPolicy`, `DeleteClusterPoliciesNotIn`,
  `ListClusterPolicies` (with ADR-0042 sort/filter/pagination).
  `internal/store/pg_policy_reports.go` — same pattern for reports.
  Add filter support for `rule_types` (array overlap `&&`) and
  `target_resources` (array overlap `&&`).
- **IMP-006**: OpenAPI: add `/v1/cluster-policies` and
  `/v1/policy-reports` endpoints to `api/openapi/openapi.yaml` with the
  shared params (Limit, Cursor, NameFilter, SortKey, SortOrder) plus
  entity-specific filters including `failure_policy`, `category`,
  `rule_types`, `target_resources`. Regenerate `ui/src/api.ts`.
- **IMP-007**: API handlers: `internal/api/cluster_policy_*.go` and
  `internal/api/policy_report_*.go` — hand-written handlers per the
  established pattern (cf. `internal/api/application_*`).
- **IMP-008**: Prometheus proxy: add a lightweight server-side proxy
  endpoint `GET /v1/cluster-policies/metrics` that forwards the
  batched PromQL query to the configured `policy_prometheus_url` and
  returns the result indexed by policy name. The UI calls this endpoint
  once per page load instead of querying Prometheus directly (avoids
  exposing Prometheus auth to the browser).
- **IMP-009**: UI: add `PolicyIcon` to `ui/src/icons.tsx`. Add
  `<NavLink>` between Pods and Services in `ui/src/App.tsx`. Add
  `Policies()` function in `ui/src/pages/Lists.tsx` with column
  definitions matching the table spec above. Implement the
  PolicyReport toggle as a toolbar button that expands/collapses a
  nested section per policy row. Prometheus-derived columns are
  rendered as async cells that populate after the metrics proxy
  responds; they display a spinner while loading and "—" on error or
  when Prometheus is not configured.
- **IMP-010**: Zone derivation: add a `clusterZone(cluster)` helper
  (shared between API response rendering and UI) that parses
  `annotations["zone"]` first, then falls back to the
  `<platform>-<env>-std-<zone>` name convention.
- **IMP-011**: MCP follow-up: add `list_cluster_policies`,
  `get_cluster_policy`, `list_policy_reports` tools to
  `internal/mcp/` — separate PR after the UI lands.

## References

- **REF-001**: ADR-0001 — CMDB for SNC using Kubernetes
- **REF-002**: ADR-0005 — Multi-cluster collector topology
- **REF-003**: ADR-0009 — Push collector for air-gapped clusters
- **REF-004**: ADR-0042 — Uniform list search & sort contract
- **REF-005**: ADR-0033 — Collector chart RBAC profiles (RBAC additions for Kyverno CRDs)
- **REF-006**: Kyverno CRD API reference — https://kyverno.io/docs/reference/crd/
- **REF-007**: PolicyReport CRD (wgpolicyk8s.io) — https://github.com/kubernetes-sigs/wg-policy-protector/tree/master/crd
- **REF-009**: Kyverno Prometheus metrics — https://kyverno.io/docs/monitoring/
- **REF-010**: `kyverno_policy_results` metric source — https://github.com/kyverno/kyverno/blob/main/pkg/metrics/metrics.go
