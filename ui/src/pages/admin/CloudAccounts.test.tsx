import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor, fireEvent } from '@testing-library/react';
import type { ReactElement } from 'react';
import CloudAccountsPage from './CloudAccounts';
import { renderWithRouter } from '../../test/render';
import { server } from '../../test/server';
import { MeProvider } from '../../me';
import { fixtureMe, fixtureCloudAccount, paged } from '../../test/fixtures';

function withAdmin(el: ReactElement) {
  return <MeProvider value={fixtureMe}>{el}</MeProvider>;
}

describe('CloudAccountsPage', () => {
  it('renders without crashing', async () => {
    renderWithRouter(withAdmin(<CloudAccountsPage />), {
      initialPath: '/admin/cloud-accounts',
    });
    // Wait for the list to resolve; "Cloud accounts" is the SectionTitle from the page itself.
    await waitFor(() =>
      expect(screen.getByRole('heading', { name: /^cloud accounts/i })).toBeInTheDocument(),
    );
  });

  it('renders the cloud account list on ready', async () => {
    renderWithRouter(withAdmin(<CloudAccountsPage />), {
      initialPath: '/admin/cloud-accounts',
    });
    await waitFor(() =>
      expect(screen.getByText(fixtureCloudAccount.name)).toBeInTheDocument(),
    );
  });

  it('renders error state on 500', async () => {
    server.use(
      http.get('/v1/admin/cloud-accounts', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(withAdmin(<CloudAccountsPage />), {
      initialPath: '/admin/cloud-accounts',
    });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });

  it('wire-capture: click Provider header sends sort=provider&order=asc', async () => {
    const captured: string[] = [];
    server.use(
      http.get('/v1/admin/cloud-accounts', ({ request }) => {
        captured.push(new URL(request.url).search);
        return HttpResponse.json(paged([fixtureCloudAccount]));
      }),
    );
    renderWithRouter(withAdmin(<CloudAccountsPage />), {
      initialPath: '/admin/cloud-accounts',
    });
    await waitFor(() => expect(screen.getByText(fixtureCloudAccount.name)).toBeInTheDocument());

    // Click Provider column header — should set sort=provider&order=asc.
    const providerHeader = screen.getByRole('columnheader', { name: /^Provider/ });
    expect(providerHeader.className).toContain('sortable');
    fireEvent.click(providerHeader);
    await waitFor(() => {
      const last = captured.at(-1) ?? '';
      expect(last).toContain('sort=provider');
      expect(last).toContain('order=asc');
    });
  });

  it('status filter narrows the rendered rows on the current page', async () => {
    server.use(
      http.get('/v1/admin/cloud-accounts', () =>
        HttpResponse.json(
          paged([
            fixtureCloudAccount,
            {
              ...fixtureCloudAccount,
              id: 'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb',
              name: 'staging-eu',
              status: 'pending_credentials',
            },
          ]),
        ),
      ),
    );
    renderWithRouter(withAdmin(<CloudAccountsPage />), {
      initialPath: '/admin/cloud-accounts?status=pending_credentials',
    });
    // Only the pending account should appear; the active one should not.
    await waitFor(() =>
      expect(screen.getByText('staging-eu')).toBeInTheDocument(),
    );
    expect(screen.queryByText(fixtureCloudAccount.name)).not.toBeInTheDocument();
  });
});
