import { useState } from 'react';
import * as api from '../api';
import { useResource, useDebouncedValue } from '../hooks';
import { AsyncView, Dash } from '../components';

// OSImages is the read-only inventory of OS images (OMI) in service across
// cloud VMs and Kubernetes cluster nodes, served by GET /v1/os-images
// (ADR-0040). The endpoint returns the full set unpaginated; this page
// sorts and filters client-side.

type SortKey = 'image_name' | 'vm_count' | 'node_count' | 'total';
type SortDir = 'asc' | 'desc';

function total(img: api.OSImage): number {
  return img.vm_count + img.node_count;
}

function formatTs(ts?: string | null): string {
  if (!ts) return '';
  try {
    return new Date(ts).toLocaleString();
  } catch {
    return ts;
  }
}

export default function OSImages() {
  const [searchInput, setSearchInput] = useState('');
  const [sortKey, setSortKey] = useState<SortKey>('image_name');
  const [sortDir, setSortDir] = useState<SortDir>('asc');
  const debouncedSearch = useDebouncedValue(searchInput, 300);

  // Fetch once on mount; search + sort are applied client-side below.
  const listState = useResource(() => api.listOSImages(), []);

  const toggleSort = (key: SortKey) => {
    if (key === sortKey) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'));
    } else {
      setSortKey(key);
      setSortDir(key === 'image_name' ? 'asc' : 'desc');
    }
  };

  const arrow = (key: SortKey) =>
    sortKey === key ? (sortDir === 'asc' ? ' ▲' : ' ▼') : '';

  return (
    <>
      <h2>OS Images</h2>
      <p className="muted" style={{ marginBottom: '1rem' }}>
        Images OS en service — VMs et nodes de clusters (ADR-0040).
      </p>

      <div className="vm-search" style={{ marginBottom: '0.75rem' }}>
        <label>
          <span>Image</span>
          <input
            type="search"
            value={searchInput}
            placeholder="e.g. master-PLATFORM-k8s"
            onChange={(e) => setSearchInput(e.target.value)}
          />
        </label>
      </div>

      <AsyncView state={listState}>
        {(data) => {
          const all = data.images;
          const totalVMs = all.reduce((acc, i) => acc + i.vm_count, 0);
          const totalNodes = all.reduce((acc, i) => acc + i.node_count, 0);

          const q = debouncedSearch.trim().toLowerCase();
          const filtered = q
            ? all.filter((i) => i.image_name.toLowerCase().includes(q))
            : all;
          const sorted = [...filtered].sort((a, b) => {
            let cmp = 0;
            if (sortKey === 'image_name') cmp = a.image_name.localeCompare(b.image_name);
            else if (sortKey === 'vm_count') cmp = a.vm_count - b.vm_count;
            else if (sortKey === 'node_count') cmp = a.node_count - b.node_count;
            else cmp = total(a) - total(b);
            return sortDir === 'asc' ? cmp : -cmp;
          });

          return (
            <>
              <div className="eol-summary" style={{ marginBottom: '1rem' }}>
                <div className="eol-summary-card">
                  <span className="eol-summary-count">{all.length}</span>
                  <span className="eol-summary-label">Images</span>
                </div>
                <div className="eol-summary-card">
                  <span className="eol-summary-count">{totalVMs}</span>
                  <span className="eol-summary-label">VMs</span>
                </div>
                <div className="eol-summary-card">
                  <span className="eol-summary-count">{totalNodes}</span>
                  <span className="eol-summary-label">Nodes</span>
                </div>
              </div>

              {sorted.length === 0 ? (
                <p className="muted empty">Aucune image OS en service.</p>
              ) : (
                <table className="entities">
                  <thead>
                    <tr>
                      <th className="sortable" onClick={() => toggleSort('image_name')}>
                        Image{arrow('image_name')}
                      </th>
                      <th className="sortable" onClick={() => toggleSort('vm_count')}>
                        VMs{arrow('vm_count')}
                      </th>
                      <th className="sortable" onClick={() => toggleSort('node_count')}>
                        Nodes{arrow('node_count')}
                      </th>
                      <th className="sortable" onClick={() => toggleSort('total')}>
                        Total{arrow('total')}
                      </th>
                      <th>Image IDs</th>
                    </tr>
                  </thead>
                  <tbody>
                    {sorted.map((img) => (
                      <tr key={img.image_name}>
                        <td>
                          <strong>{img.image_name}</strong>
                        </td>
                        <td>{img.vm_count}</td>
                        <td>{img.node_count}</td>
                        <td>{total(img)}</td>
                        <td>
                          {img.image_ids.length ? (
                            img.image_ids.map((id) => (
                              <span key={id} className="pill">
                                {id}
                              </span>
                            ))
                          ) : (
                            <Dash />
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}

              <p
                className="muted"
                style={{ marginTop: '0.75rem', fontSize: 'var(--fs-sm)' }}
              >
                Généré à {formatTs(data.generated_at) || '—'}
              </p>
            </>
          );
        }}
      </AsyncView>
    </>
  );
}
