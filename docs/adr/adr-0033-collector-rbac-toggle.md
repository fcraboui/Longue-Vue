---
title: "ADR-0033: Collector chart RBAC profiles and namespace-suffixed cluster-scoped names"
status: "Accepted"
date: "2026-06-02"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "collector", "rbac", "helm", "chart"]
supersedes: ""
superseded_by: ""
---

# ADR-0033: Collector chart RBAC profiles and namespace-suffixed cluster-scoped names

## Status

Proposed | **Accepted** | Rejected | Superseded | Deprecated

- **Date:** 2026-06-02
- **Supersedes:** none
- **Superseded by:** none

## Context

The push-mode Kubernetes collector (ADR-0009) is packaged as the
`charts/longue-vue-collector` Helm chart. The collector binary
supports two authentication paths against the target Kubernetes API,
selected at runtime in `internal/collector/kube.go`:

- `rest.InClusterConfig()` when the pod is meant to scrape the cluster
  it runs in, using its mounted ServiceAccount token.
- An explicit `kubeconfig` file when the pod is meant to scrape a
  *different* target cluster, using credentials supplied in a Secret.

Both modes are first-class. The chart's `kubeconfig.mode` value
(`in-cluster | secret`) selects between them.

Until this ADR, the chart unconditionally rendered the cluster-scoped
`ClusterRole` and `ClusterRoleBinding` whenever `rbac.create` was
true, with a single global name derived from
`Release.Name-Chart.Name`. Two consequences fell out of that:

1. **Cluster-scoped object collision across releases.** Helm has no
   notion of namespace ownership for cluster-scoped objects. Two
   releases of the same chart in two different namespaces produced
   two writers of the same `ClusterRole` / `ClusterRoleBinding`.
   Whichever release was reconciled last won; the others silently lost
   their RBAC bindings until the next reconcile flipped ownership
   again. With a GitOps loop (Flux, Argo CD) reconciling each
   release on its own cadence, the binding could ping-pong between
   subjects every few minutes, presenting as intermittent `forbidden`
   on `list` calls.

2. **Unused permissions on cross-cluster installs.** A
   `kubeconfig.mode: secret` install does not use the in-pod
   ServiceAccount token at all — its pod runs (or should run) with
   `automountServiceAccountToken: false`. The cluster-scoped objects
   it provisioned existed only as side-effects: they granted nothing
   that the pod could actually use, while still participating in the
   collision pattern above.

The combination produced silent multi-day failure modes where a
host-cluster collector lost its RBAC because a cross-cluster install
elsewhere on the same host cluster was reconciled most recently. The
host collector's `list namespaces` requests started returning 403, no
`/v1/namespaces/reconcile` calls reached the backend, and the
soft-delete reconcile pattern (ADR-0021) had no input to act on —
orphan workloads from torn-down namespaces lingered indefinitely.

## Decision

Two narrow chart changes, no collector binary change.

### 1. Cluster-scoped RBAC becomes opt-in via `rbac.cluster`

The chart introduces `rbac.cluster` (boolean, default `false`).
ClusterRole and ClusterRoleBinding templates are gated on
`and .Values.rbac.create .Values.rbac.cluster`. The outer
`rbac.create` guard is retained.

The two profiles compose with the existing `kubeconfig.mode` value:

| Profile | `kubeconfig.mode` | `rbac.cluster` | What gets installed |
|---|---|---|---|
| host-cluster collector | `in-cluster` | `true` | ServiceAccount + ClusterRole + ClusterRoleBinding |
| cross-cluster collector | `secret` | `false` (default) | ServiceAccount only (no cluster-scoped objects) |

Default `false` means a stray `helm install` in a new namespace cannot
silently create global RBAC the operator did not ask for. The
host-cluster profile is one explicit override away.

### 2. Cluster-scoped object names embed `Release.Namespace`

The `longue-vue-collector.clusterRoleName` helper now returns
`{{ fullname }}-{{ Release.Namespace }}` by default (capped at 63
chars). The `clusterrolebinding.yaml` template uses the same helper
for both `metadata.name` and `roleRef.name` so the binding points at
the namespaced role.

Two releases that both opt into `rbac.cluster: true` in different
namespaces therefore produce distinct ClusterRole and
ClusterRoleBinding objects (`<fullname>-foo` vs `<fullname>-bar`).
Collision is impossible by construction.

An explicit `rbac.clusterRoleName` override still wins for operators
who need a fixed name across releases — at their own risk; the
comment in `values.yaml` calls out the collision pattern.

## Consequences

- **Breaking change for existing in-cluster-mode installs.** Pre-0.8.0
  releases relied on `rbac.create: true` alone to provision the
  cluster-scoped pair. On upgrade to 0.8.0 they must also set
  `rbac.cluster: true` to keep their permissions. Documented in the
  chart's release notes.
- **Breaking change for object names.** Even host-cluster installs
  that set `rbac.cluster: true` see a one-shot rename of the
  ClusterRole and ClusterRoleBinding from `<fullname>` to
  `<fullname>-<namespace>`. Helm drops the old objects and creates
  the new during the upgrade transaction; no manual cleanup needed.
- **Least privilege for cross-cluster installs.** Their pods now have
  zero host-cluster API capabilities by default, matching what those
  pods actually need.
- **No collector binary change.** The dual-mode auth path in
  `internal/collector/kube.go` was already correct; this ADR fixes
  the packaging that surrounded it.

## Alternatives considered

- **Always create cluster-scoped objects, only rename them.** The
  rename alone removes the collision, but cross-cluster installs
  still provision objects nobody uses. Least-privilege wins —
  toggle plus rename.
- **Default `rbac.cluster: true` for backward compatibility.**
  Rejected: the whole point is that the failure mode is silent. A
  default of `false` forces every host install to declare its intent
  exactly once, in exchange for permanently removing the footgun.
- **A collector-binary preflight using `SelfSubjectAccessReview`.**
  Worth doing as a separate layer of defense (fail-fast with a clear
  error when the running pod lacks the permissions it expects), but
  not required once the packaging contract is correct. Deferred.

## References

- ADR-0009 — push-mode collector
- ADR-0016 — DMZ ingest gateway
- ADR-0021 — time-travel snapshots and the soft-delete reconcile
  pattern that this ADR keeps fed
- `internal/collector/kube.go` — `loadKubeConfig` dual-mode auth
- `charts/longue-vue-collector/templates/_helpers.tpl` —
  `longue-vue-collector.clusterRoleName` helper
