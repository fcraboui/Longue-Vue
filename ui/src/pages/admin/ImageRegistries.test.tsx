import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import ImageRegistriesPage from './ImageRegistries';
import { renderWithRouter } from '../../test/render';
import { server } from '../../test/server';
import { fixtureImageRegistry, fixtureImageRegistryMirror, paged } from '../../test/fixtures';

describe('ImageRegistriesPage', () => {
  it('renders the registry list', async () => {
    renderWithRouter(<ImageRegistriesPage />, { initialPath: '/admin/image-registries' });
    await waitFor(() =>
      expect(screen.getByText(fixtureImageRegistry.hostname)).toBeInTheDocument(),
    );
    expect(screen.getByText('Source')).toBeInTheDocument();
    expect(screen.getByText('Enabled')).toBeInTheDocument();
  });

  it('renders the empty state', async () => {
    server.use(
      http.get('/v1/admin/image-versions/registries', () => HttpResponse.json(paged([]))),
    );
    renderWithRouter(<ImageRegistriesPage />, { initialPath: '/admin/image-registries' });
    await waitFor(() =>
      expect(screen.getByText(/No registries configured yet/i)).toBeInTheDocument(),
    );
  });

  it('clicking Type header reorders rows', async () => {
    server.use(
      http.get('/v1/admin/image-versions/registries', () =>
        HttpResponse.json(paged([fixtureImageRegistryMirror, fixtureImageRegistry])),
      ),
    );
    const user = userEvent.setup();
    renderWithRouter(<ImageRegistriesPage />, { initialPath: '/admin/image-registries' });
    // Initial sort by hostname asc: mirror.example.com before registry.example.com.
    await waitFor(() => {
      const rows = screen.getAllByRole('row').slice(1);
      expect(rows[0].textContent).toMatch(/mirror\.example\.com/);
      expect(rows[1].textContent).toMatch(/registry\.example\.com/);
    });
    // Click Type → sort by type asc: Mirror before Source.
    await user.click(screen.getByRole('columnheader', { name: /Type/ }));
    await waitFor(() => {
      const rows = screen.getAllByRole('row').slice(1);
      expect(rows[0].textContent).toMatch(/Mirror/);
      expect(rows[1].textContent).toMatch(/Source/);
    });
    // Click again → desc: Source before Mirror.
    await user.click(screen.getByRole('columnheader', { name: /Type/ }));
    await waitFor(() => {
      const rows = screen.getAllByRole('row').slice(1);
      expect(rows[0].textContent).toMatch(/Source/);
      expect(rows[1].textContent).toMatch(/Mirror/);
    });
  });
});
