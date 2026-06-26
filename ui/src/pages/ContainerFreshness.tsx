import { useState } from 'react';
import * as api from '../api';
import { useResource, useDebouncedValue } from '../hooks';
import { AsyncView } from '../components';
import { ExtractButton } from '../components/ExtractButton';

type FreshFilter = api.Freshness | 'all';

function badgeClass(f: api.Freshness): string {
  switch (f) {
    case 'far_behind': return 'pill status-bad';
    case 'outdated': return 'pill status-warn';
    case 'up_to_date': return 'pill status-ok';
    default: return 'pill';
  }
}

function badgeLabel(f: api.Freshness): string {
  switch (f) {
    case 'far_behind': return 'Far behind';
    case 'outdated': return 'Outdated';
    case 'up_to_date': return 'Up to date';
    default: return 'Unknown';
  }
}

export default function ContainerFreshness() {
  const [imageInput, setImageInput] = useState('');
  const [fresh, setFresh] = useState<FreshFilter>('all');
  const debounced = useDebouncedValue(imageInput, 300);
  const state = useResource(
    () => api.listContainerFreshness({
      image: debounced || undefined,
      freshness: fresh === 'all' ? undefined : fresh,
    }),
    [debounced, fresh],
  );
  return (
    <div className="page">
      <div className="page-header">
        <h1>Container Freshness</h1>
      </div>
      <p className="muted" style={{ marginBottom: '1rem' }}>
        How far deployed container images are behind the latest available registry tag.
      </p>
      <AsyncView state={state}>
        {(resp) => {
          const summaryKeys: Array<{ key: FreshFilter; label: string; colorClass: string }> = [
            { key: 'all', label: 'All', colorClass: '' },
            { key: 'up_to_date', label: 'Up to date', colorClass: 'eol-ok' },
            { key: 'outdated', label: 'Outdated', colorClass: 'eol-warn' },
            { key: 'far_behind', label: 'Far behind', colorClass: 'eol-bad' },
            { key: 'unknown', label: 'Unknown', colorClass: '' },
          ];
          return (
            <>
              <div className="eol-summary" style={{ marginBottom: '1rem' }}>
                {summaryKeys.map(({ key, label, colorClass }) => {
                  const count = key === 'all' ? resp.summary.total : resp.summary[key as keyof typeof resp.summary];
                  return (
                    <div
                      key={key}
                      className={`eol-summary-card ${colorClass}${fresh === key ? ' eol-active' : ''}`}
                      onClick={() => setFresh(fresh === key ? 'all' : key)}
                      role="button"
                      tabIndex={0}
                      onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') setFresh(fresh === key ? 'all' : key); }}
                    >
                      <span className="eol-summary-count">{count}</span>
                      <span className="eol-summary-label">{label}</span>
                    </div>
                  );
                })}
              </div>
              <div style={{ display: 'flex', gap: '0.5rem', marginBottom: '0.75rem', alignItems: 'center' }}>
                <input
                  type="search"
                  placeholder="Filter by image…"
                  value={imageInput}
                  onChange={(e) => setImageInput(e.target.value)}
                />
                <ExtractButton
                  label="Extract"
                  onExtract={(format) => api.extractContainerFreshness(format)}
                />
              </div>
              {resp.items.length === 0 ? (
                <p className="muted empty">No container freshness data available.</p>
              ) : (
                <table className="entities">
                  <thead>
                    <tr>
                      <th>Freshness</th>
                      <th>Image</th>
                      <th>Latest tag</th>
                      <th>Container</th>
                      <th>Workload</th>
                      <th>Namespace</th>
                      <th>Cluster</th>
                    </tr>
                  </thead>
                  <tbody>
                    {resp.items.map((r, i) => (
                      <tr key={`${r.workload_name}-${r.container_name}-${i}`}>
                        <td><span className={badgeClass(r.freshness)}>{badgeLabel(r.freshness)}</span></td>
                        <td><code>{r.image}</code></td>
                        <td><code>{r.latest_tag || '—'}</code></td>
                        <td>{r.container_name}</td>
                        <td>{r.workload_name}</td>
                        <td>{r.namespace_name}</td>
                        <td>{r.cluster_name}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
              {resp.next_cursor && (
                <p className="muted" style={{ marginTop: '0.75rem' }}>
                  More results available — refine filters to narrow the page.
                </p>
              )}
            </>
          );
        }}
      </AsyncView>
    </div>
  );
}
