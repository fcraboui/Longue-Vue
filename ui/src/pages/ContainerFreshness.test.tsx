import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import ContainerFreshness from './ContainerFreshness';
import { renderWithRouter } from '../test/render';
import { server } from '../test/server';

const mockResponse = {
  items: [
    {
      image: 'nginx:1.24',
      latest_tag: '1.25',
      freshness: 'outdated',
      container_name: 'web',
      workload_name: 'frontend',
      namespace_name: 'default',
      cluster_name: 'prod',
    },
  ],
  summary: { total: 1, up_to_date: 0, outdated: 1, far_behind: 0, unknown: 0 },
  next_cursor: null,
};

const mockResponseWithCursor = {
  items: [
    {
      image: 'redis:7.0',
      latest_tag: '7.2',
      freshness: 'far_behind',
      container_name: 'cache',
      workload_name: 'backend',
      namespace_name: 'default',
      cluster_name: 'prod',
    },
  ],
  summary: { total: 2, up_to_date: 0, outdated: 0, far_behind: 1, unknown: 0 },
  next_cursor: 'cursor-abc',
};

const mockPage2 = {
  items: [
    {
      image: 'postgres:14',
      latest_tag: '15',
      freshness: 'outdated',
      container_name: 'db',
      workload_name: 'database',
      namespace_name: 'default',
      cluster_name: 'prod',
    },
  ],
  summary: { total: 2, up_to_date: 0, outdated: 1, far_behind: 1, unknown: 0 },
  next_cursor: null,
};

describe('ContainerFreshness', () => {
  it('renders heading', () => {
    renderWithRouter(<ContainerFreshness />, { initialPath: '/container-freshness' });
    expect(screen.getByRole('heading', { name: /container freshness/i })).toBeInTheDocument();
  });

  it('shows freshness rows', async () => {
    server.use(
      http.get('/v1/container-freshness', () => HttpResponse.json(mockResponse)),
    );
    renderWithRouter(<ContainerFreshness />, { initialPath: '/container-freshness' });
    await waitFor(() =>
      expect(screen.getAllByText('Outdated').length).toBeGreaterThan(0),
    );
    expect(screen.getByText('nginx:1.24')).toBeInTheDocument();
    expect(screen.getByText('frontend')).toBeInTheDocument();
  });

  it('shows Load more button when next_cursor is present', async () => {
    server.use(
      http.get('/v1/container-freshness', () => HttpResponse.json(mockResponseWithCursor)),
    );
    renderWithRouter(<ContainerFreshness />, { initialPath: '/container-freshness' });
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /load more/i })).toBeInTheDocument(),
    );
  });

  it('clicking Load more appends next page rows', async () => {
    let callCount = 0;
    server.use(
      http.get('/v1/container-freshness', () => {
        callCount += 1;
        return HttpResponse.json(callCount === 1 ? mockResponseWithCursor : mockPage2);
      }),
    );
    renderWithRouter(<ContainerFreshness />, { initialPath: '/container-freshness' });
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /load more/i })).toBeInTheDocument(),
    );
    await userEvent.click(screen.getByRole('button', { name: /load more/i }));
    await waitFor(() =>
      expect(screen.getByText('postgres:14')).toBeInTheDocument(),
    );
    // First page row still visible
    expect(screen.getByText('redis:7.0')).toBeInTheDocument();
    // Load more button gone (next_cursor is null on page 2)
    expect(screen.queryByRole('button', { name: /load more/i })).not.toBeInTheDocument();
  });

  it('shows Far behind badge label for far_behind freshness', async () => {
    server.use(
      http.get('/v1/container-freshness', () => HttpResponse.json(mockResponseWithCursor)),
    );
    renderWithRouter(<ContainerFreshness />, { initialPath: '/container-freshness' });
    await waitFor(() =>
      expect(screen.getAllByText('Far behind').length).toBeGreaterThan(0),
    );
  });
});
