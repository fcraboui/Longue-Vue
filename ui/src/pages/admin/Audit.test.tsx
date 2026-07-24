import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor, fireEvent } from '@testing-library/react';
import type { ReactElement } from 'react';
import AuditPage from './Audit';
import { renderWithRouter } from '../../test/render';
import { server } from '../../test/server';
import { MeProvider } from '../../me';
import { fixtureMe, fixtureAuditEvent, paged } from '../../test/fixtures';

function withAdmin(el: ReactElement) {
  return <MeProvider value={fixtureMe}>{el}</MeProvider>;
}

describe('AuditPage', () => {
  it('renders without crashing', () => {
    renderWithRouter(withAdmin(<AuditPage />), { initialPath: '/admin/audit' });
    expect(screen.getByText(/audit log/i)).toBeInTheDocument();
  });

  it('renders the audit event list on ready', async () => {
    renderWithRouter(withAdmin(<AuditPage />), { initialPath: '/admin/audit' });
    await waitFor(() =>
      expect(screen.getByText(fixtureAuditEvent.action)).toBeInTheDocument(),
    );
  });

  it('renders error state on 500', async () => {
    server.use(
      http.get('/v1/admin/audit', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(withAdmin(<AuditPage />), { initialPath: '/admin/audit' });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });

  it('wire-capture: click Action header sends sort=action&order=asc', async () => {
    const captured: string[] = [];
    server.use(
      http.get('/v1/admin/audit', ({ request }) => {
        captured.push(new URL(request.url).search);
        return HttpResponse.json(paged([fixtureAuditEvent]));
      }),
    );
    renderWithRouter(withAdmin(<AuditPage />), { initialPath: '/admin/audit' });
    await waitFor(() => expect(screen.getByText(fixtureAuditEvent.action)).toBeInTheDocument());

    // Click Action column header — should set sort=action&order=asc.
    const actionHeader = screen.getByRole('columnheader', { name: /^Action/ });
    expect(actionHeader.className).toContain('sortable');
    fireEvent.click(actionHeader);
    await waitFor(() => {
      const last = captured.at(-1) ?? '';
      expect(last).toContain('sort=action');
      expect(last).toContain('order=asc');
    });
  });

  it('pagination: Next button enabled when next_cursor returned; click sends cursor', async () => {
    const captured: string[] = [];
    server.use(
      http.get('/v1/admin/audit', ({ request }) => {
        const url = new URL(request.url);
        captured.push(url.search);
        const cursor = url.searchParams.get('cursor');
        // First page returns a next_cursor; subsequent pages don't.
        if (!cursor) {
          return HttpResponse.json({ items: [fixtureAuditEvent], next_cursor: 'c2' });
        }
        return HttpResponse.json({ items: [fixtureAuditEvent], next_cursor: null });
      }),
    );
    renderWithRouter(withAdmin(<AuditPage />), { initialPath: '/admin/audit' });
    await waitFor(() => expect(screen.getByText(fixtureAuditEvent.action)).toBeInTheDocument());

    // Next button should be enabled since next_cursor was returned.
    const nextBtn = screen.getByRole('button', { name: /next/i });
    expect(nextBtn).not.toBeDisabled();

    // Click Next — the second request should carry cursor=c2.
    fireEvent.click(nextBtn);
    await waitFor(() => {
      const last = captured.at(-1) ?? '';
      expect(last).toContain('cursor=c2');
    });
  });
});
