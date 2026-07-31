import { useEffect, useMemo, useState } from 'react';
import { Link, useSearchParams } from 'react-router';
import * as api from '../api';
import { usePagedList, useListControls, useDebouncedValue } from '../hooks';
import { Dash, Paginator } from '../components';
import { SortHeader } from '../components/SortHeader';
import { SearchInput } from '../components/SearchInput';

// Applications is the top-level list page for the ADR-0029 first-class
// Application entity. It mirrors the VirtualMachines page shape: a
// toolbar of filters above a table. The default view groups rows by
// application_block_name (with an "Unblocked" group last); a toggle
// flips to a flat alphabetical sort. Standard Paginator replaces "Load more".

// dictMax returns the maximum DICT axis value across the four axes, or
// null when none are recorded. The list badge renders "DICT max: N".
export function dictMax(a: api.Application): number | null {
  const axes = [
    a.sec_disponibilite,
    a.sec_integrite,
    a.sec_confidentialite,
    a.sec_tracabilite,
  ].filter((v): v is number => typeof v === 'number');
  if (axes.length === 0) return null;
  return Math.max(...axes);
}

// memberSummary renders "5 workloads / 2 VMs / 3 VM-apps".
function memberSummary(c: api.ApplicationMemberCounts): string {
  return `${c.workloads} workloads · ${c.virtual_machines} VMs · ${c.vm_applications} VM-apps`;
}

// Sentinel key for apps with no block. Distinct from any real (lowercased)
// block name; the group ordering below always sorts it last regardless of
// the literal value.
const UNBLOCKED = '__unblocked__';

export default function Applications() {
  const controls = useListControls();

  const [searchParams, setSearchParams] = useSearchParams();
  const setParam = (key: string, value: string) => {
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (value) next.set(key, value);
        else next.delete(key);
        return next;
      },
      { replace: true },
    );
  };

  // Read filter values from URL:
  const block = searchParams.get('application_block_name') ?? '';
  const criticality = searchParams.get('criticality') ?? '';
  const hasDictParam = searchParams.get('has_dict') ?? '';
  const dictMinParam = searchParams.get('dict_min') ?? '';

  const hasDict: 'any' | 'yes' | 'no' =
    hasDictParam === 'yes' ? 'yes' : hasDictParam === 'no' ? 'no' : 'any';
  const dictMin = dictMinParam ? Math.max(0, Math.min(4, Number(dictMinParam))) : 0;

  // Debounced text inputs for block and criticality (VM-page pattern).
  const [blockInput, setBlockInput] = useState(block);
  const [criticalityInput, setCriticalityInput] = useState(criticality);

  const debouncedBlock = useDebouncedValue(blockInput.trim(), 300);
  const debouncedCriticality = useDebouncedValue(criticalityInput.trim(), 300);

  // Debounced input → URL (guarded to avoid loops).
  useEffect(() => {
    if (debouncedBlock !== block) setParam('application_block_name', debouncedBlock);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [debouncedBlock]);

  useEffect(() => {
    if (debouncedCriticality !== criticality) setParam('criticality', debouncedCriticality);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [debouncedCriticality]);

  // External URL change (back/forward, block-pill link) → input.
  useEffect(() => {
    if (block !== debouncedBlock) setBlockInput(block);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [block]);

  useEffect(() => {
    if (criticality !== debouncedCriticality) setCriticalityInput(criticality);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [criticality]);

  // Display preference — not a filter, stays local.
  const [grouped, setGrouped] = useState(true);

  const filter: api.ApplicationListFilter = {
    application_block_name: block || undefined,
    criticality: criticality || undefined,
    has_dict: hasDict === 'any' ? undefined : hasDict === 'yes',
    dict_min: dictMin > 0 ? dictMin : undefined,
  };

  const list = usePagedList<api.Application>(
    (cursor, limit) =>
      api.listApplications({ ...filter, ...controls.params, cursor, limit }),
    [block, criticality, hasDict, dictMin, ...controls.deps],
  );

  return (
    <>
      <h2>Applications</h2>
      <p className="muted" style={{ marginBottom: '1rem' }}>
        Business systems spanning Kubernetes workloads + cloud VMs (ADR-0029).
      </p>

      <div className="vm-filters">
        <SearchInput value={controls.nameInput} onChange={controls.setNameInput} />
        <label>
          <span>Application block</span>
          <input
            type="search"
            value={blockInput}
            placeholder="block name"
            onChange={(e) => setBlockInput(e.target.value)}
          />
        </label>
        <label>
          <span>Criticality</span>
          <input
            type="search"
            value={criticalityInput}
            placeholder="critical / high / ..."
            onChange={(e) => setCriticalityInput(e.target.value)}
          />
        </label>
        <label>
          <span>Has DICT</span>
          <select
            value={hasDict}
            onChange={(e) => setParam('has_dict', e.target.value === 'any' ? '' : e.target.value)}
          >
            <option value="any">Any</option>
            <option value="yes">Yes</option>
            <option value="no">No</option>
          </select>
        </label>
        <label>
          <span>DICT min</span>
          <select
            value={dictMin}
            onChange={(e) =>
              setParam('dict_min', e.target.value === '0' ? '' : e.target.value)
            }
          >
            {[0, 1, 2, 3, 4].map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </label>
        <label className="vm-filter-checkbox">
          <button type="button" onClick={() => setGrouped((g) => !g)}>
            {grouped ? 'Flat sort' : 'Group by block'}
          </button>
        </label>
      </div>

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
        <div className="error">Failed to load: {list.error}</div>
      ) : list.items.length === 0 ? (
        <p className="muted empty">
          No applications yet — create one from a workload or VM detail page.
        </p>
      ) : grouped ? (
        <GroupedTable
          apps={list.items}
          sort={controls.sort}
          asc={controls.order === 'asc'}
          onToggle={controls.toggleSort}
        />
      ) : (
        <FlatTable
          apps={list.items}
          sort={controls.sort}
          asc={controls.order === 'asc'}
          onToggle={controls.toggleSort}
        />
      )}
    </>
  );
}

function GroupedTable({
  apps,
  sort,
  asc,
  onToggle,
}: {
  apps: api.Application[];
  sort: string;
  asc: boolean;
  onToggle: (k: string) => void;
}) {
  // Group case-insensitively on the block name; unblocked apps land in a
  // dedicated bucket keyed by UNBLOCKED so it can sort last.
  const groups = useMemo(() => {
    const m = new Map<string, { label: string; apps: api.Application[] }>();
    for (const a of apps) {
      const raw = a.application_block_name?.trim();
      const key = raw ? raw.toLowerCase() : UNBLOCKED;
      const label = raw || 'Unblocked';
      if (!m.has(key)) m.set(key, { label, apps: [] });
      m.get(key)!.apps.push(a);
    }
    return Array.from(m.entries())
      .sort(([ka], [kb]) => {
        if (ka === UNBLOCKED) return 1;
        if (kb === UNBLOCKED) return -1;
        return ka.localeCompare(kb);
      })
      .map(([, v]) => v);
  }, [apps]);

  return (
    <>
      {groups.map((g) => (
        <details key={g.label} open className="vm-subsection">
          <summary>
            {g.label} <span className="muted">({g.apps.length})</span>
          </summary>
          <FlatTable apps={g.apps} sort={sort} asc={asc} onToggle={onToggle} />
        </details>
      ))}
    </>
  );
}

function FlatTable({
  apps,
  sort,
  asc,
  onToggle,
}: {
  apps: api.Application[];
  sort: string;
  asc: boolean;
  onToggle: (k: string) => void;
}) {
  return (
    <div className="table-wrap">
      <table className="entities">
        <thead>
          <tr>
            <SortHeader
              label="Name"
              sortKey="name"
              activeKey={sort}
              asc={asc}
              onToggle={onToggle}
            />
            <th>Block</th>
            <SortHeader
              label="Owner"
              sortKey="owner"
              activeKey={sort}
              asc={asc}
              onToggle={onToggle}
            />
            <SortHeader
              label="Criticality"
              sortKey="criticality"
              activeKey={sort}
              asc={asc}
              onToggle={onToggle}
            />
            <th>DICT</th>
            <th>Members</th>
            <th>Runbook</th>
          </tr>
        </thead>
        <tbody>
          {apps.map((a) => {
            const dm = dictMax(a);
            return (
              <tr key={a.id}>
                <td>
                  <Link to={`/applications/${a.id}`}>
                    <strong>{a.display_name || a.name}</strong>
                  </Link>
                  {a.display_name && a.display_name !== a.name && (
                    <div className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
                      {a.name}
                    </div>
                  )}
                </td>
                <td>
                  {a.application_block_name ? (
                    <Link
                      to={`/applications?application_block_name=${encodeURIComponent(a.application_block_name)}`}
                      className="pill"
                    >
                      {a.application_block_name}
                    </Link>
                  ) : (
                    <Dash />
                  )}
                </td>
                <td>{a.owner || <Dash />}</td>
                <td>{a.criticality ? <span className="pill">{a.criticality}</span> : <Dash />}</td>
                <td>
                  {dm === null ? (
                    <span className="muted">no DICT</span>
                  ) : (
                    <span className={`pill heat-${dm}`}>DICT max: {dm}</span>
                  )}
                </td>
                <td className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
                  {memberSummary(a.member_counts)}
                </td>
                <td>
                  {a.runbook_url ? (
                    <a
                      href={a.runbook_url}
                      target="_blank"
                      rel="noreferrer"
                      title={a.runbook_url}
                      aria-label="Open runbook"
                    >
                      &#128279;
                    </a>
                  ) : (
                    <Dash />
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
