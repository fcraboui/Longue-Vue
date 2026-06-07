# 37. Flow matrix -- R3: evidence outputs (drift audit, metrics, extracts)

Date: 2026-06-07

Status: Accepted (R3 of the cluster-flow-matrix redesign; completes ADR-0034, ADR-0035, ADR-0036)

## Context

R1 (ADR-0035) added cluster-perimeter security-group collection plus the
`flow_matrix_enabled` toggle. R2 (ADR-0036) added the operator-declared
reference matrix and the pure read-time synthesis (`internal/flowmatrix`)
that classifies each flow as `conforme` / `non_declare` / `manquant` /
`large_ouvert`.

R3 adds the SecNumCloud *evidence* layer on top of that synthesis: bulk
extracts (ch.8 audit evidence), Prometheus metrics, and drift audit
events. All three reuse the R2 read-time synthesis -- there is no new
compute path and no periodic comparison engine. The umbrella design is at
`docs/superpowers/specs/2026-06-06-cluster-flow-matrix-design.md`.

## Decision

R3 (evidence outputs) ships:

1. **`flow_drift_seen` throttle table.** Migration 00056 adds the only
   persisted derived state of the whole feature: throttle bookkeeping for
   drift emission. It is safe to truncate. `RecordFlowDriftSeen` is an
   atomic upsert that returns whether to emit (emit-once-per-(cluster,
   flow_key)-per-24h); `ListClustersWithFlowReferences` returns the
   clusters with >=1 reference row for the metrics pass.

2. **Drift `audit_events` at synthesis time.** `emitFlowDrift` writes a
   throttled `flow_drift` system audit row per `non_declare` flow (drift =
   an actual flow with no matching reference). `large_ouvert` (a hardening
   recommendation) and `manquant` (a coverage gap) are NOT alerted as
   drift. Emission is strictly best-effort: throttle-check and insert
   failures are logged and swallowed so a flaky audit table can never turn
   a successful synthesis read into a 5xx.

3. **Audited extracts.** `GET /v1/clusters/{id}/flow-matrix/extract?format=csv|json`
   and `.../extract.zip` (read scope, gated by `flow_matrix_enabled`,
   capped by `LONGUE_VUE_EXTRACT_MAX_ROWS` with `X-Longue-Vue-Truncated`,
   audit-logged via the `shouldAudit` allowlist -- SNC ch.8 evidence). The
   ZIP bundles the CSV plus a `_sources.json` index of the referenced rule
   ids. This reuses the existing extract plumbing (ADR-0019). Following
   the EOL/search-extract precedent, both endpoints are documented in
   openapi.yaml but excluded from codegen (hand-written handlers).

4. **Prometheus gauges.** Computed at scrape/refresh time from the
   read-time synthesis (`internal/metricsrefresh`), only for clusters with
   >=1 reference row and only when the toggle is on:
   `longue_vue_flow_states{cluster,layer,state}`,
   `longue_vue_flow_reference_rows{cluster}`, and
   `longue_vue_flow_dangling_references{cluster}`.

5. **Sample alerting rules.** A disabled-by-default `PrometheusRule`
   (`charts/longue-vue/templates/prometheusrule-flowmatrix.yaml`,
   `flowMatrix.prometheusRules.enabled=false`) ships
   `FlowMatrixUndeclaredOpenIncrease` and `FlowMatrixWideOpen` as
   operator-ready starting points.

## Consequences

- `longue_vue_flow_dangling_references` is currently **always 0** because
  dangling-reference detection in `loadFlowInputs` is deferred (the
  warnings slice is presently empty). The gauge + the ZIP `_sources.json`
  wiring are forward-compatible for when detection lands.
- Drift events fire only on synthesis reads -- the matrix is read-time,
  not a periodic engine -- so drift is recorded when an operator, the UI,
  an extract, or a scrape triggers a synthesis, not autonomously. Note the
  metrics refresh loop DOES trigger a synthesis per tick for
  clusters-with-references, so for adopted clusters drift is effectively
  detected once per refresh interval.
- R1/R2/R3 complete the cluster-flow-matrix; no further ADR is planned.
  The umbrella spec's original asset-centric P2/P3 framing is fully
  superseded.

## References

- Umbrella spec: `docs/superpowers/specs/2026-06-06-cluster-flow-matrix-design.md`
- ADR-0034: flow matrix P1 -- NetworkPolicy + provider-neutral SG inventory
- ADR-0035: flow matrix R1 -- cluster-perimeter SG collection + toggle
- ADR-0036: flow matrix R2 -- reference matrix + read-time synthesis
- ADR-0019: extract plumbing (cap + truncation header + audit allowlist)
