import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor, fireEvent } from '@testing-library/react';
import VirtualMachines from './VirtualMachines';
import { renderWithRouter } from '../test/render';
import { server } from '../test/server';
import { fixtureVirtualMachine } from '../test/fixtures';

describe('VirtualMachines list', () => {
  it('renders without crashing', () => {
    renderWithRouter(<VirtualMachines />, { initialPath: '/virtual-machines' });
    expect(screen.getByRole('heading', { name: /virtual machines/i })).toBeInTheDocument();
  });

  it('renders the VM name once data arrives', async () => {
    renderWithRouter(<VirtualMachines />, { initialPath: '/virtual-machines' });
    await waitFor(() =>
      expect(screen.getByText(fixtureVirtualMachine.name)).toBeInTheDocument(),
    );
  });

  it('renders error state on 500', async () => {
    server.use(
      http.get('/v1/virtual-machines', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(<VirtualMachines />, { initialPath: '/virtual-machines' });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });

  it('Name column header is sortable and clicking it sends sort=name&order=asc', async () => {
    const captured: string[] = [];
    server.use(
      http.get('/v1/virtual-machines', ({ request }) => {
        captured.push(new URL(request.url).search);
        return HttpResponse.json({ items: [fixtureVirtualMachine], next_cursor: null });
      }),
    );
    renderWithRouter(<VirtualMachines />, { initialPath: '/virtual-machines' });
    await waitFor(() =>
      expect(screen.getByText(fixtureVirtualMachine.name)).toBeInTheDocument(),
    );
    const nameHeader = screen.getByRole('columnheader', { name: /^Name/ });
    expect(nameHeader.className).toContain('sortable');
    fireEvent.click(nameHeader);
    await waitFor(() => {
      const last = captured.at(-1) ?? '';
      expect(last).toContain('sort=name');
      expect(last).toContain('order=asc');
    });
  });

  it('typing in the name box sends name param after debounce', async () => {
    const captured: string[] = [];
    server.use(
      http.get('/v1/virtual-machines', ({ request }) => {
        captured.push(new URL(request.url).search);
        return HttpResponse.json({ items: [fixtureVirtualMachine], next_cursor: null });
      }),
    );
    renderWithRouter(<VirtualMachines />, { initialPath: '/virtual-machines' });
    await waitFor(() =>
      expect(screen.getByText(fixtureVirtualMachine.name)).toBeInTheDocument(),
    );
    const nameInput = screen.getByRole('searchbox', { name: /^Name/i });
    fireEvent.change(nameInput, { target: { value: 'vpn' } });
    await waitFor(
      () => {
        const last = captured.at(-1) ?? '';
        expect(last).toContain('name=vpn');
      },
      { timeout: 1000 },
    );
  });
});
