import { afterEach, describe, expect, it, vi } from 'vitest';
import { http, HttpResponse } from 'msw';
import { server } from '../test/server';
import { ApiError } from '../api';
import {
  createFlowReference,
  deleteFlowReference,
  exportFlowReferencesURL,
  importFlowReferencesYAML,
  listFlowReferences,
  updateFlowReference,
  type FlowReference,
  type FlowReferenceInput,
} from './flows';

afterEach(() => vi.restoreAllMocks());

const sampleInput: FlowReferenceInput = {
  layer: 'perimeter',
  direction: 'ingress',
  src_kind: 'endpoint_group',
  src_ref: 'internet',
  dst_kind: 'workload',
  dst_ref: 'api',
  protocol: 'tcp',
  from_port: 443,
  to_port: 443,
  justification: 'public API',
};

const sampleRef: FlowReference = {
  ...sampleInput,
  id: 'ref-1',
  cluster_id: 'c-1',
  created_by: 'alice',
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
};

describe('flow reference client', () => {
  it('listFlowReferences parses {items}', async () => {
    server.use(
      http.get('/v1/clusters/:id/flow-references', () =>
        HttpResponse.json({ items: [sampleRef] }),
      ),
    );
    const resp = await listFlowReferences('c-1');
    expect(resp.items).toHaveLength(1);
    expect(resp.items[0].id).toBe('ref-1');
  });

  it('createFlowReference POSTs JSON and returns the row', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    server.use(
      http.post('/v1/clusters/:id/flow-references', () =>
        HttpResponse.json(sampleRef, { status: 201 }),
      ),
    );
    const created = await createFlowReference('c-1', sampleInput);
    expect(created.id).toBe('ref-1');
    const init = fetchSpy.mock.calls[0][1] as RequestInit;
    expect(init.method).toBe('POST');
    expect((init.headers as Record<string, string>)['Content-Type']).toBe(
      'application/json',
    );
    expect(init.credentials).toBe('same-origin');
  });

  it('createFlowReference surfaces 409 validation detail as ApiError', async () => {
    server.use(
      http.post('/v1/clusters/:id/flow-references', () =>
        HttpResponse.json(
          { title: 'Conflict', detail: 'internal flows must not use endpoint_group' },
          { status: 409 },
        ),
      ),
    );
    await expect(createFlowReference('c-1', sampleInput)).rejects.toMatchObject({
      name: 'ApiError',
      status: 409,
      message: 'internal flows must not use endpoint_group',
    });
  });

  it('updateFlowReference PATCHes by reference id', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    server.use(
      http.patch('/v1/flow-references/:id', () => HttpResponse.json(sampleRef)),
    );
    await updateFlowReference('ref-1', sampleInput);
    expect(fetchSpy.mock.calls[0][0]).toBe('/v1/flow-references/ref-1');
    const init = fetchSpy.mock.calls[0][1] as RequestInit;
    expect(init.method).toBe('PATCH');
  });

  it('deleteFlowReference resolves on 204', async () => {
    server.use(
      http.delete('/v1/flow-references/:id', () => new HttpResponse(null, { status: 204 })),
    );
    await expect(deleteFlowReference('ref-1')).resolves.toBeUndefined();
  });

  it('deleteFlowReference throws ApiError on failure', async () => {
    server.use(
      http.delete('/v1/flow-references/:id', () =>
        HttpResponse.json({ detail: 'forbidden' }, { status: 403 }),
      ),
    );
    await expect(deleteFlowReference('ref-1')).rejects.toBeInstanceOf(ApiError);
  });

  it('importFlowReferencesYAML sends text/yaml and parses the replaced set', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    server.use(
      http.post('/v1/clusters/:id/flow-references/import', () =>
        HttpResponse.json({ items: [sampleRef] }),
      ),
    );
    const resp = await importFlowReferencesYAML('c-1', 'references: []');
    expect(resp.items).toHaveLength(1);
    const init = fetchSpy.mock.calls[0][1] as RequestInit;
    expect(init.method).toBe('POST');
    expect((init.headers as Record<string, string>)['Content-Type']).toBe('text/yaml');
    expect(init.body).toBe('references: []');
  });

  it('importFlowReferencesYAML surfaces 4xx row detail', async () => {
    server.use(
      http.post('/v1/clusters/:id/flow-references/import', () =>
        HttpResponse.json({ detail: 'row 2: justification required' }, { status: 400 }),
      ),
    );
    await expect(importFlowReferencesYAML('c-1', 'bad')).rejects.toMatchObject({
      status: 400,
      message: 'row 2: justification required',
    });
  });

  it('exportFlowReferencesURL builds the export path', () => {
    expect(exportFlowReferencesURL('c-1')).toBe('/v1/clusters/c-1/flow-references/export');
  });
});
