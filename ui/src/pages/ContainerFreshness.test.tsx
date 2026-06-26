import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor } from '@testing-library/react';
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
});
