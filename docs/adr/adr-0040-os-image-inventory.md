---
title: "ADR-0040: OS image inventory in the CMDB"
status: "Accepted"
date: "2026-06-20"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "vm-collector", "nodes", "inventory"]
supersedes: ""
superseded_by: ""
---

# ADR-0040: OS image inventory in the CMDB

## Status

Proposed | **Accepted** | Rejected | Superseded | Deprecated

- **Date:** 2026-06-20
- **Supersedes:** none
- **Superseded by:** none

## Context

The CMDB lacked an inventory of OS images (OMI) actually in service. Cloud
VMs carry `image_name`, but cluster nodes do not: the OMI name does not come
back via the Kubernetes API (`status.nodeInfo.osImage` is a generic OS
string), and the VMs that back nodes are deliberately excluded from
`virtual_machines` (ADR-0015) — and, in steady state, dropped client-side by
the collector's kube-node pre-filter, so their image never reaches the
server.

## Decision

1. Add nullable `image_id`/`image_name` to `nodes`.
2. The VM collector — which already resolves every listed VM's `image_name`
   before the pre-filter — sends a per-tick batch of `{provider_vm_id,
   image_id, image_name}` for the dropped kube-node VMs to a new
   vm-collector ingest endpoint `POST /v1/ingest/cloud-accounts/{id}/node-images`,
   which backfills `nodes` by `provider_id` substring match (idempotent).
   This mirrors the existing per-VM SG-attachment side channel; it keeps the
   pre-filter optimisation and does not overload the VM upsert path.
3. Expose `GET /v1/os-images`: the deduplicated set of `image_name` across
   non-terminated VMs ∪ active nodes, with distinct image ids and per-source
   counts.

The endpoint is **generic and vendor-neutral**. External consumers
(vulnerability scanners, compliance exports, dashboards) read it and act on
their own side; the CMDB has no knowledge of any specific consumer.

## Consequences

- Dedup key is `image_name` (image ids are per-region in Outscale).
- A deleted node drops its image with the row (cascade); no reaper.
- An older collector against a new server leaves node images NULL (degraded
  but safe); a new collector against an older server gets a 404 on the
  node-images push, treated as a non-fatal no-op.
- Untagged node VMs that slip past the pre-filter still hit the server-side
  409 dedup unchanged; backfilling them there is possible future work.
