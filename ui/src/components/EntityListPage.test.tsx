import { describe, expect, it } from 'vitest';
import { screen, fireEvent, waitFor } from '@testing-library/react';
import { renderWithRouter } from '../test/render';
import { EntityListPage } from './EntityListPage';

type Row = { id: string; name: string };

function makeFetch(calls: Array<{ params: unknown; cursor?: string; limit: number }>) {
  return async (
    params: { name?: string; sort?: string; order?: string },
    cursor: string | undefined,
    limit: number,
  ) => {
    calls.push({ params, cursor, limit });
    return {
      items: [
        { id: '1', name: 'alpha' },
        { id: '2', name: 'beta' },
      ] as Row[],
      next_cursor: null,
    };
  };
}

function renderPage(calls: Array<{ params: unknown; cursor?: string; limit: number }>) {
  return renderWithRouter(
    <EntityListPage<Row>
      title="Widgets"
      icon={null}
      storageKey="test.widgets"
      emptyMessage="none"
      fetchPage={makeFetch(calls)}
      rowKey={(r) => r.id}
      columns={[
        { key: 'name', label: 'Name', sortKey: 'name', render: (r) => r.name },
        { key: 'computed', label: 'Computed', render: () => 'x' },
      ]}
    />,
  );
}

describe('EntityListPage', () => {
  it('renders rows, sortable and plain headers', async () => {
    const calls: Array<{ params: unknown; cursor?: string; limit: number }> = [];
    renderPage(calls);
    await screen.findByText('alpha');
    const headers = screen.getAllByRole('columnheader');
    expect(headers[0].className).toContain('sortable');
    expect(headers[1].className).not.toContain('sortable');
  });

  it('clicking a sortable header refetches with sort params', async () => {
    const calls: Array<{ params: { sort?: string; order?: string }; cursor?: string; limit: number }> = [];
    renderPage(calls);
    await screen.findByText('alpha');
    fireEvent.click(screen.getByRole('columnheader', { name: /^Name/ }));
    await waitFor(() =>
      expect(calls.at(-1)?.params).toMatchObject({ sort: 'name', order: 'asc' }),
    );
  });

  it('typing in the search box refetches with name after debounce', async () => {
    const calls: Array<{ params: { name?: string }; cursor?: string; limit: number }> = [];
    renderPage(calls);
    await screen.findByText('alpha');
    fireEvent.change(screen.getByPlaceholderText(/Filter by name/), { target: { value: 'alp' } });
    await waitFor(() => expect(calls.at(-1)?.params).toMatchObject({ name: 'alp' }), {
      timeout: 2000,
    });
  });
});
