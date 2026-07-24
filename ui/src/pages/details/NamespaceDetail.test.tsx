import { describe, expect, it, vi } from 'vitest';
import { http, HttpResponse } from 'msw';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { NamespaceDetail } from './NamespaceDetail';
import { server } from '../../test/server';
import { fixtureNamespace, paged, fixtureWorkload } from '../../test/fixtures';

function renderDetail() {
  return render(
    <MemoryRouter initialEntries={[`/namespaces/${fixtureNamespace.id}`]}>
      <Routes>
        <Route path="/namespaces/:id" element={<NamespaceDetail />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('NamespaceDetail', () => {
  it('renders without crashing', () => {
    renderDetail();
    expect(screen.getAllByText(/loading/i).length).toBeGreaterThan(0);
  });

  it('typed search in Workloads section sends name= scoped to namespace_id=', async () => {
    const captured = vi.fn();
    server.use(
      http.get('/v1/workloads', ({ request }) => {
        const url = new URL(request.url);
        captured({
          name: url.searchParams.get('name'),
          namespace_id: url.searchParams.get('namespace_id'),
        });
        return HttpResponse.json(paged([fixtureWorkload]));
      }),
    );

    const user = userEvent.setup();
    renderDetail();

    // Wait for namespace detail to load.
    await waitFor(() =>
      expect(screen.getByRole('heading', { level: 2 })).toBeInTheDocument(),
    );

    // Find the search input inside the Workloads section (first search box).
    const inputs = await screen.findAllByPlaceholderText(/filter by name/i);
    const workloadsInput = inputs[0];

    await user.type(workloadsInput, 'api');

    // After the debounce the request should carry both name= and namespace_id=.
    await waitFor(() => {
      const calls = captured.mock.calls.map((c) => c[0]);
      const scoped = calls.find((c) => c.name === 'api');
      expect(scoped).toBeDefined();
      expect(scoped.namespace_id).toBe(fixtureNamespace.id);
    }, { timeout: 1000 });
  });
});
