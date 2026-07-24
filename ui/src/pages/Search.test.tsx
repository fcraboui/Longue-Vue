import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { http, HttpResponse } from 'msw';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import * as api from '../api';
import ImageSearch from './Search';
import { renderWithRouter } from '../test/render';
import { server } from '../test/server';

describe('ImageSearch', () => {
  it('renders the search input', () => {
    renderWithRouter(<ImageSearch />, { initialPath: '/search/image' });
    expect(screen.getByRole('textbox')).toBeInTheDocument();
  });

  it('renders the empty state when no query is set', () => {
    renderWithRouter(<ImageSearch />, { initialPath: '/search/image' });
    expect(screen.getByText(/enter an image to search/i)).toBeInTheDocument();
  });

  it('renders results when a query is present', async () => {
    renderWithRouter(<ImageSearch />, { initialPath: '/search/image?q=nginx' });
    await waitFor(() => {
      // The callout summarising match counts always renders after the fetches settle.
      expect(screen.getByText(/matches for/i)).toBeInTheDocument();
    });
  });

  it('shows an error state when fetch calls fail', async () => {
    server.use(
      http.get('/v1/workloads', () => new HttpResponse(null, { status: 500 })),
      http.get('/v1/pods', () => new HttpResponse(null, { status: 500 })),
      http.get('/v1/virtual-machines', () => new HttpResponse(null, { status: 500 })),
      http.get('/v1/namespaces', () => new HttpResponse(null, { status: 500 })),
      http.get('/v1/clusters', () => new HttpResponse(null, { status: 500 })),
      http.get('/v1/admin/cloud-accounts', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(<ImageSearch />, { initialPath: '/search/image?q=nginx' });
    await waitFor(() => {
      expect(screen.getByText(/error|failed/i)).toBeInTheDocument();
    });
  });
});

describe('Search page incomplete list banners', () => {
  it('shows the workloads banner when /v1/workloads returns a next_cursor, others absent', async () => {
    server.use(
      http.get('/v1/workloads', () =>
        HttpResponse.json({ items: [], next_cursor: 'more' }),
      ),
      http.get('/v1/pods', () => HttpResponse.json({ items: [], next_cursor: null })),
      http.get('/v1/virtual-machines', () => HttpResponse.json({ items: [], next_cursor: null })),
    );
    renderWithRouter(<ImageSearch />, { initialPath: '/search/image?q=nginx' });
    await waitFor(() => expect(screen.getByText(/matches for/i)).toBeInTheDocument());
    // Workloads banner should be visible — banner div has class "banner-warn".
    const workloadsBanners = screen.getAllByText(/first page of/i);
    expect(workloadsBanners.length).toBe(1);
    expect(workloadsBanners[0].textContent).toMatch(/matching workloads/i);
    // Pods and VMs banners should be absent (no second/third "first page of" element).
    const allBannerTexts = screen.getAllByText(/first page of/i).map((el) => el.textContent ?? '');
    expect(allBannerTexts.some((t) => /matching pods/i.test(t))).toBe(false);
    expect(allBannerTexts.some((t) => /matching virtual machines/i.test(t))).toBe(false);
  });
});

describe('Search page extract buttons', () => {
  beforeEach(() => {
    // Mock the api extract functions so downloadExtract (which needs DOM APIs
    // not available in jsdom) is never reached.
    vi.spyOn(api, 'extractSearch').mockResolvedValue({ truncated: false, filename: null });
    vi.spyOn(api, 'extractSearchZip').mockResolvedValue({ truncated: false, filename: null });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('shows per-table extract buttons and the all-zip button', async () => {
    renderWithRouter(<ImageSearch />, { initialPath: '/search/image?q=nginx' });
    await waitFor(() => expect(screen.getByText(/matches for/i)).toBeInTheDocument());
    // Three per-table Extract ▾ dropdowns + one "Extract all (.zip)" button.
    const buttons = screen.getAllByRole('button', { name: /extract/i });
    expect(buttons.length).toBeGreaterThanOrEqual(4);
  });

  it('clicking CSV on workloads invokes api.extractSearch with kind=workloads', async () => {
    const spy = vi.spyOn(api, 'extractSearch').mockResolvedValue({ truncated: false, filename: null });
    renderWithRouter(<ImageSearch />, { initialPath: '/search/image?q=nginx' });
    await waitFor(() => expect(screen.getByText(/matches for/i)).toBeInTheDocument());
    // The Extract dropdown buttons (aria-haspopup="menu") are after the zip button.
    // Index 0 is "Extract all (.zip)", index 1 is the Workloads Extract dropdown.
    const extractButtons = screen.getAllByRole('button', { name: /extract/i });
    fireEvent.click(extractButtons[1]);
    fireEvent.click(screen.getByRole('menuitem', { name: /csv/i }));
    await waitFor(() => expect(spy).toHaveBeenCalledWith('nginx', 'workloads', 'csv'));
    spy.mockRestore();
  });
});
