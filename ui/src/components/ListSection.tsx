// ListSection factors the repeated "related assets" section pattern shared
// by the detail pages: a SectionTitle with count, a Paginator wired to a
// usePagedList state, the loading / error / empty tri-state, and an
// `entities` table rendered from a column config. The caller owns the
// usePagedList hook so it can also derive extra content from the current
// page (e.g. the nodes running a workload). Sections with bespoke layouts
// (group-by, impact analysis) keep their hand-rolled markup.
import { Link } from 'react-router-dom';
import { Empty, Paginator, SectionTitle } from '../components';
import type { ListControls, PagedListState } from '../hooks';
import { SearchInput } from './SearchInput';
import { SortHeader } from './SortHeader';

export interface ListColumn<T> {
  key: string;
  label: React.ReactNode;
  sortKey?: string;                  // NEW — ADR-0042 allowlist key
  render: (item: T) => React.ReactNode;
  // When set, the cell wraps the rendered value in a bold link — the
  // "name column links to the detail page" convention.
  link?: (item: T) => string;
}

export interface ListSectionProps<T> {
  title: React.ReactNode;
  list: PagedListState<T>;
  columns: ListColumn<T>[];
  emptyMessage: string;
  rowKey: (item: T) => string;
  controls?: ListControls;           // NEW — when set: SearchInput + sortable headers
  searchLabel?: string;              // NEW — SearchInput label override (default 'Name')
}

export function ListSection<T>({
  title,
  list,
  columns,
  emptyMessage,
  rowKey,
  controls,
  searchLabel = 'Name',
}: ListSectionProps<T>) {
  return (
    <>
      <SectionTitle count={list.items.length}>{title}</SectionTitle>
      {controls && (
        <div className="vm-filters">
          <SearchInput
            value={controls.nameInput}
            onChange={controls.setNameInput}
            label={searchLabel}
          />
        </div>
      )}
      <Paginator
        pageSize={list.pageSize}
        hasPrev={list.hasPrev}
        hasNext={list.hasNext}
        onPrev={list.prev}
        onNext={list.next}
        onPageSize={list.setPageSize}
      />
      {list.loading ? (
        <p className="loading">Loading…</p>
      ) : list.error ? (
        <p className="error">{list.error}</p>
      ) : list.items.length === 0 ? (
        <Empty message={emptyMessage} />
      ) : (
        <table className="entities">
          <thead>
            <tr>
              {columns.map((c) =>
                controls && c.sortKey ? (
                  <SortHeader
                    key={c.key}
                    label={c.label}
                    sortKey={c.sortKey}
                    activeKey={controls.sort}
                    asc={controls.order === 'asc'}
                    onToggle={controls.toggleSort}
                  />
                ) : (
                  <th key={c.key}>{c.label}</th>
                ),
              )}
            </tr>
          </thead>
          <tbody>
            {list.items.map((item) => (
              <tr key={rowKey(item)}>
                {columns.map((c) => (
                  <td key={c.key}>
                    {c.link ? (
                      <Link to={c.link(item)}>
                        <strong>{c.render(item)}</strong>
                      </Link>
                    ) : (
                      c.render(item)
                    )}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}
