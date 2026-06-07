// Typed client for the cluster flow-matrix synthesis endpoint
// (GET /v1/clusters/{id}/flow-matrix). Mirrors the server-side
// flowmatrix.Synthesis shape in internal/flowmatrix/types.go. The
// synthesis is computed read-time and gated behind flow_matrix_enabled:
// the endpoint returns a 409 problem+json when the feature is off, which
// getClusterFlowMatrix surfaces as a typed FlowMatrixDisabledError so the
// Flows page can show a "feature disabled" message instead of a raw error.

import { ApiError } from '../api';

// FlowState is the classification of one synthesized flow. Matches the
// flowmatrix.State constants on the server.
export type FlowState = 'conforme' | 'non_declare' | 'manquant' | 'large_ouvert';

// PortRange is an inclusive port span. `from` undefined means "any port".
export interface PortRange {
  protocol: string; // tcp|udp|icmp|any
  from?: number;
  to?: number;
}

// Endpoint identifies one side of a flow.
export interface Endpoint {
  kind: string; // workload|service|namespace|endpoint_group|cidr
  name: string;
  cidr?: string;
}

// FlowSource records a contributing rule for a synthesized flow.
export interface FlowSource {
  kind: string; // sg_rule|netpol_rule|reference
  id: string;
  summary: string;
}

// Flow is one synthesized cell of the matrix.
export interface Flow {
  layer: string;
  direction: string;
  src: Endpoint;
  dst: Endpoint;
  ports: PortRange[];
  state: FlowState;
  reference_id?: string | null;
  sources: FlowSource[];
}

// Warning flags a non-fatal synthesis issue (e.g. a dangling reference ref).
export interface Warning {
  kind: string;
  reference_id?: string;
  ref?: string;
}

// Synthesis is the full per-cluster flow-matrix result.
export interface Synthesis {
  cluster_id: string;
  perimeter: Flow[];
  internal: Flow[];
  warnings: Warning[];
}

// FlowMatrixDisabledError is thrown when the endpoint returns 409 because
// flow_matrix_enabled is off. The Flows page catches it and renders a
// dedicated "feature disabled" panel rather than a generic error.
export class FlowMatrixDisabledError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'FlowMatrixDisabledError';
  }
}

// parseProblem extracts an RFC 7807 problem+json `detail` (or `title`) from
// a non-ok response body, falling back to the HTTP status text. Shared by
// every flows helper so 4xx validation errors surface the server message.
async function parseProblem(res: Response): Promise<string> {
  let detail = res.statusText;
  try {
    const body = await res.json();
    if (body && typeof body.detail === 'string') {
      detail = body.detail;
    } else if (body && typeof body.title === 'string') {
      detail = body.title;
    }
  } catch {
    // Non-JSON body — keep statusText.
  }
  return detail;
}

// getClusterFlowMatrix fetches the read-time synthesis for one cluster.
// A 409 (feature disabled) is translated into a FlowMatrixDisabledError;
// every other non-2xx surfaces as the shared ApiError.
export async function getClusterFlowMatrix(clusterId: string): Promise<Synthesis> {
  const res = await fetch(`/v1/clusters/${clusterId}/flow-matrix`, {
    credentials: 'same-origin',
  });
  if (!res.ok) {
    const detail = await parseProblem(res);
    if (res.status === 409) {
      throw new FlowMatrixDisabledError(detail);
    }
    throw new ApiError(res.status, detail);
  }
  return res.json() as Promise<Synthesis>;
}

// --- Reference matrix (operator-declared) -------------------------------
//
// The reference matrix is the operator's source of truth for "what flows
// are allowed". The synthesis above compares the discovered posture against
// these references. CRUD here is editor-scoped server-side.

export type FlowLayer = 'perimeter' | 'internal';
export type FlowDirection = 'ingress' | 'egress';
export type FlowEndpointKind = 'workload' | 'service' | 'namespace' | 'endpoint_group';
export type FlowProtocol = 'tcp' | 'udp' | 'icmp' | 'any';

// FlowReferenceInput is the write shape (POST / PATCH body). It omits the
// server-assigned id/cluster_id and the created_*/updated_* audit columns.
export interface FlowReferenceInput {
  layer: FlowLayer;
  direction: FlowDirection;
  src_kind: FlowEndpointKind;
  src_ref: string;
  dst_kind: FlowEndpointKind;
  dst_ref: string;
  protocol: FlowProtocol;
  from_port?: number | null;
  to_port?: number | null;
  justification: string;
}

// FlowReference is one stored reference row.
export interface FlowReference extends FlowReferenceInput {
  id: string;
  cluster_id: string;
  created_by?: string | null;
  created_at: string;
  updated_at: string;
}

// listFlowReferences returns every stored reference for one cluster.
export async function listFlowReferences(
  clusterId: string,
): Promise<{ items: FlowReference[] }> {
  const res = await fetch(`/v1/clusters/${clusterId}/flow-references`, {
    credentials: 'same-origin',
  });
  if (!res.ok) {
    throw new ApiError(res.status, await parseProblem(res));
  }
  return res.json() as Promise<{ items: FlowReference[] }>;
}

// createFlowReference adds one reference. A 409 carries the server's
// validation detail (e.g. "internal flows must not use endpoint_group").
export async function createFlowReference(
  clusterId: string,
  input: FlowReferenceInput,
): Promise<FlowReference> {
  const res = await fetch(`/v1/clusters/${clusterId}/flow-references`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  if (!res.ok) {
    throw new ApiError(res.status, await parseProblem(res));
  }
  return res.json() as Promise<FlowReference>;
}

// updateFlowReference replaces one reference in place (full input body).
export async function updateFlowReference(
  id: string,
  input: FlowReferenceInput,
): Promise<FlowReference> {
  const res = await fetch(`/v1/flow-references/${id}`, {
    method: 'PATCH',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  if (!res.ok) {
    throw new ApiError(res.status, await parseProblem(res));
  }
  return res.json() as Promise<FlowReference>;
}

// deleteFlowReference removes one reference (204 on success).
export async function deleteFlowReference(id: string): Promise<void> {
  const res = await fetch(`/v1/flow-references/${id}`, {
    method: 'DELETE',
    credentials: 'same-origin',
  });
  if (!res.ok) {
    throw new ApiError(res.status, await parseProblem(res));
  }
}

// importFlowReferencesYAML replaces every reference for a cluster with the
// set parsed from a raw YAML document (sent as text/yaml). Destructive:
// callers must confirm before invoking. Validation errors (4xx) surface
// the server's per-row detail via ApiError so the editor can show
// "row N: ..." messages.
export async function importFlowReferencesYAML(
  clusterId: string,
  yaml: string,
): Promise<{ items: FlowReference[] }> {
  const res = await fetch(`/v1/clusters/${clusterId}/flow-references/import`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'text/yaml' },
    body: yaml,
  });
  if (!res.ok) {
    throw new ApiError(res.status, await parseProblem(res));
  }
  return res.json() as Promise<{ items: FlowReference[] }>;
}

// exportFlowReferencesURL returns the GET path that streams the cluster's
// references as a text/yaml document. Used as an <a href download> target
// so the browser fetches it with the operator's session cookie.
export function exportFlowReferencesURL(clusterId: string): string {
  return `/v1/clusters/${clusterId}/flow-references/export`;
}
