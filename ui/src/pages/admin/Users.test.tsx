import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactElement } from 'react';
import UsersPage from './Users';
import { renderWithRouter } from '../../test/render';
import { server } from '../../test/server';
import { MeProvider } from '../../me';
import { fixtureMe, fixtureUser, paged } from '../../test/fixtures';

function withAdmin(el: ReactElement) {
  return <MeProvider value={fixtureMe}>{el}</MeProvider>;
}

describe('UsersPage', () => {
  it('renders without crashing', async () => {
    renderWithRouter(withAdmin(<UsersPage />), { initialPath: '/admin/users' });
    // Wait for the list to resolve; "Users" SectionTitle (h3) is from the page itself.
    await waitFor(() =>
      expect(screen.getByRole('heading', { level: 3, name: /^users/i })).toBeInTheDocument(),
    );
  });

  it('renders the user list on ready', async () => {
    renderWithRouter(withAdmin(<UsersPage />), { initialPath: '/admin/users' });
    await waitFor(() =>
      expect(screen.getByText(fixtureUser.username)).toBeInTheDocument(),
    );
  });

  it('renders error state on 500', async () => {
    server.use(
      http.get('/v1/admin/users', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(withAdmin(<UsersPage />), { initialPath: '/admin/users' });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });

  it('shows Locked status and Unlock button when locked_at is set', async () => {
    server.use(
      http.get('/v1/admin/users', () =>
        HttpResponse.json(paged([
          {
            ...fixtureUser,
            id: 'cccccccc-cccc-cccc-cccc-cccccccccccc',
            username: 'lockedone',
            role: 'editor',
            failed_login_count: 6,
            locked_at: '2026-05-09T10:00:00Z',
          },
        ])),
      ),
    );
    renderWithRouter(withAdmin(<UsersPage />), { initialPath: '/admin/users' });
    await waitFor(() => expect(screen.getByText('lockedone')).toBeInTheDocument());
    expect(screen.getByText('Locked')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /unlock/i })).toBeInTheDocument();
  });

  it('wire-capture: click Username header sends sort=username&order=asc, type in search sends name=ali', async () => {
    const captured: string[] = [];
    server.use(
      http.get('/v1/admin/users', ({ request }) => {
        captured.push(new URL(request.url).search);
        return HttpResponse.json(paged([fixtureUser]));
      }),
    );
    const user = userEvent.setup();
    renderWithRouter(withAdmin(<UsersPage />), { initialPath: '/admin/users' });
    await waitFor(() => expect(screen.getByText(fixtureUser.username)).toBeInTheDocument());

    // Click Username column header — should set sort=username&order=asc.
    const usernameHeader = screen.getByRole('columnheader', { name: /^Username/ });
    expect(usernameHeader.className).toContain('sortable');
    fireEvent.click(usernameHeader);
    await waitFor(() => {
      const last = captured.at(-1) ?? '';
      expect(last).toContain('sort=username');
      expect(last).toContain('order=asc');
    });

    // Type in the search box — should debounce and send name=ali.
    await user.type(screen.getByPlaceholderText('Filter by name… (* = wildcard)'), 'ali');
    await waitFor(() => {
      const last = captured.at(-1) ?? '';
      expect(last).toContain('name=ali');
    });
  });
});
