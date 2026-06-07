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

// getClusterFlowMatrix fetches the read-time synthesis for one cluster.
// A 409 (feature disabled) is translated into a FlowMatrixDisabledError;
// every other non-2xx surfaces as the shared ApiError.
export async function getClusterFlowMatrix(clusterId: string): Promise<Synthesis> {
  const res = await fetch(`/v1/clusters/${clusterId}/flow-matrix`, {
    credentials: 'same-origin',
  });
  if (!res.ok) {
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
    if (res.status === 409) {
      throw new FlowMatrixDisabledError(detail);
    }
    throw new ApiError(res.status, detail);
  }
  return res.json() as Promise<Synthesis>;
}
