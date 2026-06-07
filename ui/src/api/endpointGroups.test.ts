import { afterEach, describe, expect, it, vi } from 'vitest';
import { http, HttpResponse } from 'msw';
import { server } from '../test/server';
import { ApiError } from '../api';
import {
  listEndpointGroups,
  createEndpointGroup,
  updateEndpointGroup,
  deleteEndpointGroup,
  type EndpointGroup,
  type EndpointGroupInput,
} from './endpointGroups';

afterEach(() => vi.restoreAllMocks());

const sampleInput: EndpointGroupInput = {
  name: 'internet',
  notes: 'public internet',
  cidrs: ['0.0.0.0/0'],
};

const sampleGroup: EndpointGroup = {
  ...sampleInput,
  id: 'eg-1',
  created_by: 'alice',
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
};

describe('endpoint group client', () => {
  it('listEndpointGroups parses {items}', async () => {
    server.use(
      http.get('/v1/admin/endpoint-groups', () =>
        HttpResponse.json({ items: [sampleGroup] }),
      ),
    );
    const resp = await listEndpointGroups();
    expect(resp.items).toHaveLength(1);
    expect(resp.items[0].id).toBe('eg-1');
  });

  it('createEndpointGroup POSTs JSON and returns the row', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    server.use(
      http.post('/v1/admin/endpoint-groups', () =>
        HttpResponse.json(sampleGroup, { status: 201 }),
      ),
    );
    const created = await createEndpointGroup(sampleInput);
    expect(created.id).toBe('eg-1');
    const init = fetchSpy.mock.calls[0][1] as RequestInit;
    expect(init.method).toBe('POST');
    expect((init.headers as Record<string, string>)['Content-Type']).toBe(
      'application/json',
    );
    expect(init.credentials).toBe('same-origin');
  });

  it('createEndpointGroup surfaces 409 validation detail as ApiError', async () => {
    server.use(
      http.post('/v1/admin/endpoint-groups', () =>
        HttpResponse.json(
          { title: 'Conflict', detail: 'endpoint group name already exists' },
          { status: 409 },
        ),
      ),
    );
    await expect(createEndpointGroup(sampleInput)).rejects.toMatchObject({
      name: 'ApiError',
      status: 409,
      message: 'endpoint group name already exists',
    });
  });

  it('updateEndpointGroup PATCHes by id', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    server.use(
      http.patch('/v1/admin/endpoint-groups/:id', () =>
        HttpResponse.json(sampleGroup),
      ),
    );
    await updateEndpointGroup('eg-1', sampleInput);
    expect(fetchSpy.mock.calls[0][0]).toBe('/v1/admin/endpoint-groups/eg-1');
    const init = fetchSpy.mock.calls[0][1] as RequestInit;
    expect(init.method).toBe('PATCH');
  });

  it('deleteEndpointGroup resolves on 204', async () => {
    server.use(
      http.delete('/v1/admin/endpoint-groups/:id', () =>
        new HttpResponse(null, { status: 204 }),
      ),
    );
    await expect(deleteEndpointGroup('eg-1')).resolves.toBeUndefined();
  });

  it('deleteEndpointGroup throws ApiError on failure', async () => {
    server.use(
      http.delete('/v1/admin/endpoint-groups/:id', () =>
        HttpResponse.json({ detail: 'forbidden' }, { status: 403 }),
      ),
    );
    await expect(deleteEndpointGroup('eg-1')).rejects.toBeInstanceOf(ApiError);
  });
});
