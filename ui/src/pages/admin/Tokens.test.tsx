import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactElement } from 'react';
import TokensPage from './Tokens';
import { renderWithRouter } from '../../test/render';
import { server } from '../../test/server';
import { MeProvider } from '../../me';
import { fixtureMe, fixtureToken, paged } from '../../test/fixtures';

function withAdmin(el: ReactElement) {
  return <MeProvider value={fixtureMe}>{el}</MeProvider>;
}

describe('TokensPage', () => {
  it('renders without crashing', async () => {
    renderWithRouter(withAdmin(<TokensPage />), { initialPath: '/admin/tokens' });
    // Wait for the list to resolve; "Machine tokens" is the SectionTitle from the page itself.
    await waitFor(() =>
      expect(screen.getByText(/machine tokens/i)).toBeInTheDocument(),
    );
  });

  it('renders the token list on ready', async () => {
    renderWithRouter(withAdmin(<TokensPage />), { initialPath: '/admin/tokens' });
    await waitFor(() =>
      expect(screen.getByText(fixtureToken.name)).toBeInTheDocument(),
    );
  });

  it('renders error state on 500', async () => {
    server.use(
      http.get('/v1/admin/tokens', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(withAdmin(<TokensPage />), { initialPath: '/admin/tokens' });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });

  it('wire-capture: click Name header sends sort=name&order=asc', async () => {
    const captured: string[] = [];
    server.use(
      http.get('/v1/admin/tokens', ({ request }) => {
        captured.push(new URL(request.url).search);
        return HttpResponse.json(paged([fixtureToken]));
      }),
    );
    renderWithRouter(withAdmin(<TokensPage />), { initialPath: '/admin/tokens' });
    await waitFor(() => expect(screen.getByText(fixtureToken.name)).toBeInTheDocument());

    // Click Name column header — should set sort=name&order=asc.
    const nameHeader = screen.getByRole('columnheader', { name: /^Name/ });
    expect(nameHeader.className).toContain('sortable');
    fireEvent.click(nameHeader);
    await waitFor(() => {
      const last = captured.at(-1) ?? '';
      expect(last).toContain('sort=name');
      expect(last).toContain('order=asc');
    });
  });

  it('bind param survives search-box typing (vm-collector preset stays active)', async () => {
    server.use(
      http.get('/v1/admin/tokens', () => HttpResponse.json(paged([fixtureToken]))),
      // MintForm loads cloud accounts when preset is vm-collector.
      http.get('/v1/admin/cloud-accounts', () => HttpResponse.json(paged([]))),
    );
    const user = userEvent.setup();
    const bindUuid = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa';
    renderWithRouter(withAdmin(<TokensPage />), {
      initialPath: `/admin/tokens?bind=${bindUuid}`,
    });
    // With bind=, MintForm opens immediately with vm-collector preset.
    await waitFor(() =>
      expect(screen.getByLabelText(/bound to cloud account/i)).toBeInTheDocument(),
    );

    // Type in the search box — triggers debounced URL update.
    await user.type(screen.getByPlaceholderText('Filter by name… (* = wildcard)'), 'ci');

    // After typing, the vm-collector preset form must still be visible —
    // proving the bind-driven form state survived the URL name-param write.
    await waitFor(() => {
      // Token list should now filter.
      expect(screen.getByPlaceholderText('Filter by name… (* = wildcard)')).toHaveValue('ci');
    });
    // The MintForm (vm-collector) must still be rendered.
    expect(screen.getByLabelText(/bound to cloud account/i)).toBeInTheDocument();
  });
});
