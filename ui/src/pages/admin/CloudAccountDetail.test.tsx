import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import type { ReactElement } from 'react';
import CloudAccountDetailPage from './CloudAccountDetail';
import { renderWithRouter } from '../../test/render';
import { server } from '../../test/server';
import { MeProvider } from '../../me';
import { fixtureMe, fixtureCloudAccount, fixtureVirtualMachine, paged } from '../../test/fixtures';

function withAdmin(el: ReactElement) {
  return <MeProvider value={fixtureMe}>{el}</MeProvider>;
}

describe('CloudAccountDetailPage', () => {
  it('renders without crashing', () => {
    renderWithRouter(withAdmin(<CloudAccountDetailPage />), {
      initialPath: `/admin/cloud-accounts/${fixtureCloudAccount.id}`,
      routePath: '/admin/cloud-accounts/:id',
    });
    expect(screen.getByText(/loading/i)).toBeInTheDocument();
  });

  it('renders the account detail on ready', async () => {
    renderWithRouter(withAdmin(<CloudAccountDetailPage />), {
      initialPath: `/admin/cloud-accounts/${fixtureCloudAccount.id}`,
      routePath: '/admin/cloud-accounts/:id',
    });
    await waitFor(() =>
      expect(
        screen.getByRole('heading', { level: 2, name: new RegExp(fixtureCloudAccount.name, 'i') }),
      ).toBeInTheDocument(),
    );
  });

  it('renders error state on 500', async () => {
    server.use(
      http.get('/v1/admin/cloud-accounts/:id', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(withAdmin(<CloudAccountDetailPage />), {
      initialPath: `/admin/cloud-accounts/${fixtureCloudAccount.id}`,
      routePath: '/admin/cloud-accounts/:id',
    });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });

  it('VM list paginates across cursor pages (no silent 50-row cap)', async () => {
    const vm1 = { ...fixtureVirtualMachine, id: 'vm-1', name: 'vm-alpha', display_name: null };
    const vm2 = { ...fixtureVirtualMachine, id: 'vm-2', name: 'vm-beta', display_name: null };

    server.use(
      http.get('/v1/virtual-machines', ({ request }) => {
        const url = new URL(request.url);
        const cursor = url.searchParams.get('cursor');
        if (cursor === 'c2') {
          return HttpResponse.json(paged([vm2]));
        }
        return HttpResponse.json({ items: [vm1], next_cursor: 'c2' });
      }),
    );

    render(
      <MemoryRouter initialEntries={[`/admin/cloud-accounts/${fixtureCloudAccount.id}`]}>
        <MeProvider value={fixtureMe}>
          <Routes>
            <Route path="/admin/cloud-accounts/:id" element={<CloudAccountDetailPage />} />
          </Routes>
        </MeProvider>
      </MemoryRouter>,
    );

    // First page shows vm-alpha, Next button is enabled.
    await waitFor(() => expect(screen.getByText('vm-alpha')).toBeInTheDocument());
    const nextBtn = screen.getByRole('button', { name: /next/i });
    expect(nextBtn).not.toBeDisabled();

    // Click Next → second page carries cursor=c2.
    await userEvent.click(nextBtn);
    await waitFor(() => expect(screen.getByText('vm-beta')).toBeInTheDocument());
    expect(screen.queryByText('vm-alpha')).toBeNull();
  });
});
