// Typed client for the endpoint-group admin endpoints
// (GET/POST/PATCH/DELETE /v1/admin/endpoint-groups). Endpoint groups are
// operator-curated named CIDR sets referenced by perimeter flow references
// (R2 Task 13). All endpoints are admin-scoped server-side.
//
// Uses the same raw-fetch + RFC 7807 problem+json parsing as
// api/flows.ts so 4xx validation errors (e.g. a duplicate name) surface the
// server's `detail` message via the shared ApiError.

import { ApiError } from '../api';

// EndpointGroup is one stored named CIDR set.
export interface EndpointGroup {
  id: string;
  name: string;
  notes?: string;
  cidrs: string[];
  created_by?: string;
  created_at: string;
  updated_at: string;
}

// EndpointGroupInput is the write shape (POST / PATCH body). It omits the
// server-assigned id and the created_*/updated_* audit columns.
export interface EndpointGroupInput {
  name: string;
  notes: string;
  cidrs: string[];
}

// parseProblem extracts an RFC 7807 problem+json `detail` (or `title`) from
// a non-ok response body, falling back to the HTTP status text. Mirrors the
// helper in api/flows.ts so 4xx validation errors surface the server message.
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

// listEndpointGroups returns every stored endpoint group.
export async function listEndpointGroups(): Promise<{ items: EndpointGroup[] }> {
  const res = await fetch('/v1/admin/endpoint-groups', {
    credentials: 'same-origin',
  });
  if (!res.ok) {
    throw new ApiError(res.status, await parseProblem(res));
  }
  return res.json() as Promise<{ items: EndpointGroup[] }>;
}

// createEndpointGroup adds one group. A 409 carries the server's validation
// detail (e.g. "endpoint group name already exists").
export async function createEndpointGroup(
  input: EndpointGroupInput,
): Promise<EndpointGroup> {
  const res = await fetch('/v1/admin/endpoint-groups', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  if (!res.ok) {
    throw new ApiError(res.status, await parseProblem(res));
  }
  return res.json() as Promise<EndpointGroup>;
}

// updateEndpointGroup replaces one group in place (full input body).
export async function updateEndpointGroup(
  id: string,
  input: EndpointGroupInput,
): Promise<EndpointGroup> {
  const res = await fetch(`/v1/admin/endpoint-groups/${id}`, {
    method: 'PATCH',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  if (!res.ok) {
    throw new ApiError(res.status, await parseProblem(res));
  }
  return res.json() as Promise<EndpointGroup>;
}

// deleteEndpointGroup removes one group (204 on success). Server-side FK
// behavior allows deleting a group still referenced by flow references.
export async function deleteEndpointGroup(id: string): Promise<void> {
  const res = await fetch(`/v1/admin/endpoint-groups/${id}`, {
    method: 'DELETE',
    credentials: 'same-origin',
  });
  if (!res.ok) {
    throw new ApiError(res.status, await parseProblem(res));
  }
}
