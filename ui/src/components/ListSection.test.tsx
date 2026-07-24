import { describe, expect, it } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useLocalListControls, usePagedList } from '../hooks';
import type { PagedResponse } from '../api';
import { ListSection } from './ListSection';

type Item = { id: string; name: string; kind: string };

const itemsPage = (items: Item[], next?: string): PagedResponse<Item> => ({
  items,
  next_cursor: next ?? null,
});

// Harness owns the usePagedList hook (the production pattern: section
// components call the hook, ListSection renders the state).
function Harness({
  fetchPage,
}: {
  fetchPage: (cursor: string | undefined, limit: number) => Promise<PagedResponse<Item>>;
}) {
  const list = usePagedList<Item>(fetchPage, []);
  return (
    <ListSection
      title="Things"
      list={list}
      emptyMessage="No things yet."
      rowKey={(i) => i.id}
      columns={[
        { key: 'name', label: 'Name', link: (i) => `/things/${i.id}`, render: (i) => i.name },
        { key: 'kind', label: 'Kind', render: (i) => <span className="pill">{i.kind}</span> },
      ]}
    />
  );
}

function renderHarness(
  fetchPage: (cursor: string | undefined, limit: number) => Promise<PagedResponse<Item>>,
) {
  return render(
    <MemoryRouter>
      <Harness fetchPage={fetchPage} />
    </MemoryRouter>,
  );
}

describe('ListSection', () => {
  it('shows the loading state while the page is in flight', () => {
    renderHarness(() => new Promise(() => {}));
    expect(screen.getByText(/loading/i)).toBeInTheDocument();
  });

  it('renders rows with headers, name link and count once loaded', async () => {
    renderHarness(async () =>
      itemsPage([
        { id: 'a1', name: 'alpha', kind: 'Widget' },
        { id: 'b2', name: 'beta', kind: 'Gadget' },
      ]),
    );
    await waitFor(() => expect(screen.getByText('alpha')).toBeInTheDocument());
    // Title carries the count of the current page.
    expect(screen.getByRole('heading', { level: 3 })).toHaveTextContent('Things (2)');
    // Column headers from the config.
    expect(screen.getByRole('columnheader', { name: 'Name' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Kind' })).toBeInTheDocument();
    // `link` columns wrap the value in a bold link to the detail page.
    expect(screen.getByRole('link', { name: 'alpha' })).toHaveAttribute('href', '/things/a1');
    // Custom render cells pass through untouched.
    expect(screen.getByText('Gadget')).toBeInTheDocument();
  });

  it('renders the empty message when the page has no items', async () => {
    renderHarness(async () => itemsPage([]));
    await waitFor(() => expect(screen.getByText('No things yet.')).toBeInTheDocument());
    expect(screen.queryByRole('table')).toBeNull();
  });

  it('renders the error state when the fetch fails', async () => {
    renderHarness(async () => {
      throw new Error('boom');
    });
    await waitFor(() => expect(screen.getByText('boom')).toBeInTheDocument());
  });

  it('pages forward through the Paginator', async () => {
    renderHarness(async (cursor) =>
      cursor === 'next-1'
        ? itemsPage([{ id: 'c3', name: 'gamma', kind: 'Widget' }])
        : itemsPage([{ id: 'a1', name: 'alpha', kind: 'Widget' }], 'next-1'),
    );
    await waitFor(() => expect(screen.getByText('alpha')).toBeInTheDocument());
    await userEvent.click(screen.getByRole('button', { name: /next/i }));
    await waitFor(() => expect(screen.getByText('gamma')).toBeInTheDocument());
    expect(screen.queryByText('alpha')).toBeNull();
  });

  it('renders identically to before when controls are omitted', async () => {
    renderHarness(async () =>
      itemsPage([{ id: 'a1', name: 'alpha', kind: 'Widget' }]),
    );
    await waitFor(() => expect(screen.getByText('alpha')).toBeInTheDocument());
    // No search input rendered.
    expect(screen.queryByPlaceholderText(/filter by name/i)).toBeNull();
    // Headers are plain <th> elements (not sortable).
    const nameHeader = screen.getByRole('columnheader', { name: 'Name' });
    expect(nameHeader).not.toHaveClass('sortable');
    const kindHeader = screen.getByRole('columnheader', { name: 'Kind' });
    expect(kindHeader).not.toHaveClass('sortable');
  });

  it('renders search box and sortable headers when controls are provided', async () => {
    // Host component owns useLocalListControls and passes it to ListSection.
    function ControlledHarness() {
      const list = usePagedList<Item>(
        async () => itemsPage([{ id: 'a1', name: 'alpha', kind: 'Widget' }]),
        [],
      );
      const controls = useLocalListControls();
      return (
        <ListSection
          title="Controlled"
          list={list}
          emptyMessage="Nothing."
          rowKey={(i) => i.id}
          controls={controls}
          columns={[
            { key: 'name', label: 'Name', sortKey: 'name', render: (i) => i.name },
            { key: 'kind', label: 'Kind', render: (i) => i.kind },
          ]}
        />
      );
    }

    render(
      <MemoryRouter>
        <ControlledHarness />
      </MemoryRouter>,
    );

    await waitFor(() => expect(screen.getByText('alpha')).toBeInTheDocument());

    // Search input is rendered with the default placeholder.
    expect(screen.getByPlaceholderText(/filter by name/i)).toBeInTheDocument();

    // First column has sortKey → rendered as SortHeader with class 'sortable'.
    const nameHeader = screen.getByRole('columnheader', { name: 'Name' });
    expect(nameHeader).toHaveClass('sortable');

    // Second column has no sortKey → plain <th>, no 'sortable' class.
    const kindHeader = screen.getByRole('columnheader', { name: 'Kind' });
    expect(kindHeader).not.toHaveClass('sortable');

    // Clicking the sortable header toggles the ascending arrow.
    await userEvent.click(nameHeader);
    expect(screen.getByRole('columnheader', { name: /Name.*▲/ })).toBeInTheDocument();
  });
});
