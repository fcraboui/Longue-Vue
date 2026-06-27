---
title: "ADR-0041: Container image freshness as a distinct signal from EOL"
status: "Accepted"
date: "2026-06-26"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "cmdb", "eol", "image-versions", "freshness", "dashboard"]
supersedes: ""
superseded_by: ""
---

# ADR-0041: Container image freshness as a distinct signal from EOL

## Status

Proposed | **Accepted** | Rejected | Superseded | Deprecated

- **Date:** 2026-06-26
- **Supersedes:** none (amends ADR-0032)
- **Superseded by:** none

## Context

ADR-0022 introduced the image-versions enricher, which measures the minor/major
version distance between a deployed container tag and the latest tag published in
the same registry. ADR-0032 then folded this registry-distance signal into the
global EOL dashboard under the `eol_status` vocabulary (`supported` /
`approaching_eol` / `eol`), reusing the same field name and colour palette as
the endoflife.date annotations on clusters, nodes, and VMs.

This conflation turned out to be semantically wrong and operationally harmful:

1. **Different data sources, different semantics.** endoflife.date tracks
   whether a product version is still receiving security patches from its
   upstream maintainer. The image-versions enricher tracks whether a newer tag
   exists in a registry — a purely relative, tag-distance measurement with no
   lifecycle-support semantics. A container image one minor version behind latest
   is not "approaching end of life"; it is simply not on the newest release.

2. **Inflated EOL counts.** Because workloads are far more numerous than clusters
   or VMs, adding workload rows to the EOL dashboard made the `eol` and
   `approaching_eol` counts spike on any fleet that was not on the absolute
   latest patch of every image. Operators reported confusion: the dashboard
   suggested their infrastructure was critically end-of-life when endoflife.date
   data showed otherwise.

3. **Vocabulary collision.** Using `eol_status` for a field that does not
   measure EOL creates documentation debt, makes API consumers write ambiguous
   filters, and makes the per-Application EOL card misleading when it folds
   image freshness rows alongside true EOL rows.

## Decision

### Freshness vocabulary

A new `freshness` field (replacing `eol_status` on `ContainerVersionInfo`)
carries registry-distance status using purpose-built values:

| Value | Condition |
|-------|-----------|
| `up_to_date` | 0 minor versions behind (patch differences only, or already on latest). |
| `outdated` | Exactly 1 minor version behind latest. |
| `far_behind` | 2 or more minor versions behind, or any major version gap. |
| `unknown` | Tag is not semver-parseable, registry is not in the allowlist, or enrichment has not run yet. |

The `eol_status` field is removed from `ContainerVersionInfo`. Existing API
consumers that read `eol_status` on workload/pod container entries must migrate
to `freshness`.

### Dedicated endpoint and UI page

A new read-only endpoint `GET /v1/container-freshness` aggregates freshness
signal across all workloads and pods, one row per distinct image repo (worst
tier wins on collision). Query parameters mirror the image-versions list:
`registry`, `image_repo` (substring), `freshness` (filter by value), `limit`,
`cursor`.

A **Container Freshness** page is added to the UI under the **Tools** sidebar
section (path `/container-freshness`). It shows summary cards
(Up to date / Outdated / Far behind / Unknown) and a sortable table with one
row per image repo, linking to the image-versions detail page.

### EOL dashboard and extract are pure endoflife.date

`eolagg.FlattenWorkloads` is removed. The EOL dashboard (`/ui/eol`), the EOL
extract (`/v1/eol/extract`), and the per-Application EOL card (`GET
/v1/applications/{id}/eol`) no longer include workload or image rows. The
`entity_type=workload` filter value on the EOL extract returns `400 Bad
Request`.

### Per-Application EOL card

The per-Application EOL card surfaces two kinds of member rows, distinguished
by a `signal` field:

- VM member rows sourced from `longue-vue.io/eol.*` endoflife annotations carry
  `"signal": "eol"`. Their `eol_status` drives the `statusRank` precedence used
  to compute the application's worst-case EOL posture.
- Workload image member rows carry `"signal": "freshness"` and a `freshness`
  value (`up_to_date` / `outdated` / `far_behind` / `unknown`). These rows are
  excluded from EOL `statusRank` precedence — they appear for visibility but do
  not inflate the application's EOL rating.

The OpenAPI enum for `signal` is `[eol, freshness]`.

## Consequences

- **Breaking rename:** `eol_status` → `freshness` on `ContainerVersionInfo`
  (workload/pod container entries). Any client reading `eol_status` on these
  objects will get `null` until migrated.
- The EOL dashboard and extract are now a clean endoflife.date surface.
  `longue_vue_eol_enrichments_total{resource="workload"}` will always be 0.
- `GET /v1/eol/extract?entity_type=workload` returns `400 Bad Request`.
- A new `GET /v1/container-freshness` endpoint and `/container-freshness` UI
  page replace the workload rows previously visible in the EOL dashboard.
- The per-Application EOL card gains a `signal` field on each row. Existing
  consumers that only read VM and cluster rows are unaffected.
- `eolagg.FlattenWorkloads` is deleted; its callers (dashboard + extract) are
  updated. Shared fixtures in `internal/eolagg` shrink accordingly.

## References

- ADR-0022 — Container image versions enrichment (V1)
- ADR-0029 — First-class Application entity + per-app EOL aggregation
- ADR-0032 — Kubernetes image freshness in the global EOL dashboard (amended by this ADR)
