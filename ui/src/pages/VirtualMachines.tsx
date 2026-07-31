import { useEffect, useMemo, useState } from 'react';
import { Link, useSearchParams } from 'react-router';
import * as api from '../api';
import { useResource, usePagedList, useListControls, useDebouncedValue } from '../hooks';
import { isAdmin, useMe } from '../me';
import { Dash, Paginator } from '../components';
import { useEntityTable } from '../components/column_filters';
import { SortHeader } from '../components/SortHeader';
import { SearchInput } from '../components/SearchInput';
import { VirtualMachineIcon } from '../icons';

// VirtualMachines is the top-level list page for ADR-0015 VMs. It mirrors
// the shape of the EOL Inventory dashboard: filterable summary cards
// above a sortable table. Power state is the load-bearing column —
// running / stopped / terminated / error each get a distinct pill.

type PowerStateGroup = 'running' | 'stopped' | 'terminated' | 'error' | 'other';

// classify groups raw provider power states into the 5 visual buckets we
// render in the summary cards / pill colour. "stopping" and "shutting_down"
// fold into 'stopped' (transient) so the card count stays meaningful.
function classify(state: string): PowerStateGroup {
  switch (state) {
    case 'running':
      return 'running';
    case 'stopped':
    case 'stopping':
    case 'pending':
      return 'stopped';
    case 'terminated':
    case 'shutting_down':
      return 'terminated';
    case 'error':
      return 'error';
    default:
      return 'other';
  }
}

// powerStateClass returns the CSS class for a coloured pill matching the
// state group. Tokens come from styles.css (--ok-* / --warn-* / --bad-*).
function powerStateClass(state: string): string {
  const g = classify(state);
  switch (g) {
    case 'running':
      return 'pill status-ok';
    case 'stopped':
      return 'pill status-warn';
    case 'error':
      return 'pill status-warn';
    case 'terminated':
      return 'pill status-bad';
    default:
      return 'pill';
  }
}

export function PowerStatePill({ state }: { state: string }) {
  return <span className={powerStateClass(state)}>{state}</span>;
}

function formatTs(ts?: string | null): string {
  if (!ts) return '';
  try {
    return new Date(ts).toLocaleString();
  } catch {
    return ts;
  }
}

export default function VirtualMachines() {
  const me = useMe();
  const showAccountFilter = isAdmin(me);
  const tableRef = useEntityTable('vms.list');

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

  // Read all filters from the URL:
  const cloudAccountId = searchParams.get('cloud_account_id') ?? '';
  const region = searchParams.get('region') ?? '';
  const powerState = searchParams.get('power_state') ?? '';
  const includeTerminated = searchParams.get('include_terminated') === 'true';
  const application = searchParams.get('application') ?? '';
  const applicationVersion = searchParams.get('application_version') ?? '';

  // Image: debounced free-text like name, but page-local (not part of
  // useListControls — it is a VM-specific filter).
  const [imageInput, setImageInput] = useState(searchParams.get('image') ?? '');
  const debouncedImage = useDebouncedValue(imageInput.trim(), 300);
  useEffect(() => {
    if (debouncedImage !== (searchParams.get('image') ?? '')) setParam('image', debouncedImage);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [debouncedImage]);

  // Role filter is applied client-side (after splitting comma-joined
  // role strings) so that a filter on `dns` matches a VM tagged
  // `ansible_group=dns,primary_dns`. Region / cloud_account / power_state
  // remain server-side filters.
  const [role, setRole] = useState<string>('');
  const [groupFilter, setGroupFilter] = useState<PowerStateGroup | null>(null);

  const filter: api.VirtualMachineListFilter = {
    cloud_account_id: cloudAccountId || undefined,
    region: region || undefined,
    power_state: powerState || undefined,
    include_terminated: includeTerminated,
    image: debouncedImage || undefined,
    application: application || undefined,
    application_version: application && applicationVersion ? applicationVersion : undefined,
  };

  // Cloud account dropdown is admin-only — viewers / editors can't list
  // accounts.
  const accountsState = useResource(
    () => (showAccountFilter ? api.listCloudAccounts() : Promise.resolve(null)),
    [showAccountFilter],
  );

  // Distinct applications list backs the application filter dropdown.
  // Cheap (DISTINCT product over a JSONB array) and refreshed on each
  // mount; the request fans out in parallel with listVirtualMachines.
  const appsState = useResource(() => api.listDistinctVMApplications(), []);

  const vmsList = usePagedList<api.VirtualMachine>(
    (cursor, limit) => api.listVirtualMachines({ ...filter, ...controls.params, cursor, limit }),
    [
      cloudAccountId, region, powerState, includeTerminated,
      debouncedImage, application, applicationVersion,
      ...controls.deps,
    ],
  );

  const splitRoles = (raw: string | null | undefined): string[] =>
    (raw ?? '')
      .split(',')
      .map((r) => r.trim())
      .filter((r) => r !== '');

  const accountsById = useMemo(() => {
    if (accountsState.status !== 'ready' || !accountsState.data) return new Map();
    return new Map(accountsState.data.items.map((a) => [a.id, a]));
  }, [accountsState]);

  const accountList = accountsState.status === 'ready' ? accountsState.data?.items ?? [] : [];

  return (
    <>
      <h2>
        <VirtualMachineIcon size={20} /> Virtual Machines
      </h2>
      <p className="muted" style={{ marginBottom: '1rem' }}>
        Non-Kubernetes platform VMs catalogued by longue-vue-vm-collector (ADR-0015).
      </p>

      {(() => {
        const all = vmsList.items;
        // Summary card counts run over the current page's items.
        const counts: Record<PowerStateGroup, number> = {
          running: 0,
          stopped: 0,
          terminated: 0,
          error: 0,
          other: 0,
        };
        for (const vm of all) counts[classify(vm.power_state)] += 1;
        const pendingAccounts = accountList.filter((a) => a.status === 'pending_credentials').length;

        // Apply client-side filters on top of the server-side ones.
        // Role: split each VM's role string on `,` and match the
        // selected role against any of the splits.
        // Group: classify the power_state into running / stopped /
        // terminated buckets for summary-card click.
        const visible = all.filter((vm) => {
          if (role && !splitRoles(vm.role).includes(role)) return false;
          if (groupFilter && classify(vm.power_state) !== groupFilter) return false;
          return true;
        });
        const sorted = visible;

        // Region dropdown options: union of regions from cloud_accounts
        // (admins only) and the currently-loaded VMs, plus the
        // currently-selected region (so the user can always clear it
        // even after a filter narrows the visible regions).
        const regionSet = new Set<string>();
        for (const a of accountList) if (a.region) regionSet.add(a.region);
        for (const vm of all) if (vm.region) regionSet.add(vm.region);
        if (region) regionSet.add(region);
        const regions = Array.from(regionSet).sort();

        // Role dropdown options: distinct individual roles seen on
        // ingested VMs (comma-joined strings split into separate
        // entries), plus the currently-selected role.
        const roleSet = new Set<string>();
        for (const vm of all) for (const r of splitRoles(vm.role)) roleSet.add(r);
        if (role) roleSet.add(role);
        const roles = Array.from(roleSet).sort();

        return (
          <>
            {showAccountFilter && pendingAccounts > 0 && (
                <div className="vm-banner">
                  <strong>{pendingAccounts}</strong> cloud account
                  {pendingAccounts === 1 ? '' : 's'} pending credentials.{' '}
                  <Link to="/admin/cloud-accounts?status=pending_credentials">View</Link>
                </div>
              )}

              <div className="eol-summary">
                <SummaryCard
                  label="Running"
                  count={counts.running}
                  active={groupFilter === 'running'}
                  cls="eol-ok"
                  onClick={() =>
                    setGroupFilter(groupFilter === 'running' ? null : 'running')
                  }
                />
                <SummaryCard
                  label="Stopped"
                  count={counts.stopped + counts.error}
                  active={groupFilter === 'stopped' || groupFilter === 'error'}
                  cls="eol-warn"
                  onClick={() =>
                    setGroupFilter(groupFilter === 'stopped' ? null : 'stopped')
                  }
                />
                <SummaryCard
                  label="Terminated"
                  count={counts.terminated}
                  active={groupFilter === 'terminated'}
                  cls="eol-bad"
                  onClick={() =>
                    setGroupFilter(groupFilter === 'terminated' ? null : 'terminated')
                  }
                />
                <SummaryCard
                  label="Total"
                  count={all.length}
                  active={groupFilter === null}
                  cls=""
                  onClick={() => setGroupFilter(null)}
                />
              </div>

              <div className="vm-filters">
                {showAccountFilter && (
                  <label>
                    <span>Cloud account</span>
                    <select
                      value={cloudAccountId}
                      onChange={(e) => setParam('cloud_account_id', e.target.value)}
                    >
                      <option value="">All accounts</option>
                      {accountList.map((a) => (
                        <option key={a.id} value={a.id}>
                          {a.name} ({a.provider}/{a.region})
                        </option>
                      ))}
                    </select>
                  </label>
                )}
                <label>
                  <span>Region</span>
                  <select
                    value={region}
                    onChange={(e) => setParam('region', e.target.value)}
                    disabled={regions.length === 0}
                  >
                    <option value="">All regions</option>
                    {regions.map((r) => (
                      <option key={r} value={r}>
                        {r}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  <span>Role</span>
                  <select
                    value={role}
                    onChange={(e) => setRole(e.target.value)}
                    disabled={roles.length === 0}
                  >
                    <option value="">All roles</option>
                    {roles.map((r) => (
                      <option key={r} value={r}>
                        {r}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  <span>Power state</span>
                  <select value={powerState} onChange={(e) => setParam('power_state', e.target.value)}>
                    <option value="">Any</option>
                    <option value="running">running</option>
                    <option value="stopped">stopped</option>
                    <option value="stopping">stopping</option>
                    <option value="pending">pending</option>
                    <option value="terminated">terminated</option>
                    <option value="error">error</option>
                    <option value="unknown">unknown</option>
                  </select>
                </label>
                <label>
                  <span>Application</span>
                  <select
                    value={application}
                    onChange={(e) => {
                      setParam('application', e.target.value);
                      // Reset version when the product changes — the
                      // option list belongs to the previous product.
                      setParam('application_version', '');
                    }}
                    disabled={
                      appsState.status !== 'ready' ||
                      appsState.data.products.length === 0
                    }
                  >
                    <option value="">Any</option>
                    {appsState.status === 'ready' &&
                      appsState.data.products.map((p) => (
                        <option key={p.product} value={p.product}>
                          {p.product}
                        </option>
                      ))}
                  </select>
                </label>
                <label>
                  <span>App version</span>
                  <select
                    value={applicationVersion}
                    onChange={(e) => setParam('application_version', e.target.value)}
                    disabled={!application || appsState.status !== 'ready'}
                  >
                    <option value="">Any</option>
                    {appsState.status === 'ready' &&
                      application &&
                      (appsState.data.products.find((p) => p.product === application)?.versions ?? []).map(
                        (v) => (
                          <option key={v} value={v}>
                            {v}
                          </option>
                        ),
                      )}
                  </select>
                </label>
                <label className="vm-filter-checkbox">
                  <input
                    type="checkbox"
                    checked={includeTerminated}
                    onChange={(e) => setParam('include_terminated', e.target.checked ? 'true' : '')}
                  />
                  <span>Include terminated</span>
                </label>
              </div>

              <div className="vm-search">
                <SearchInput
                  value={controls.nameInput}
                  onChange={controls.setNameInput}
                />
                <label>
                  <span>Image</span>
                  <input
                    type="search"
                    value={imageInput}
                    placeholder="ami-… or name fragment"
                    onChange={(e) => setImageInput(e.target.value)}
                  />
                </label>
              </div>

              <Paginator
                pageSize={vmsList.pageSize}
                hasPrev={vmsList.hasPrev}
                hasNext={vmsList.hasNext}
                onPrev={vmsList.prev}
                onNext={vmsList.next}
                onPageSize={vmsList.setPageSize}
              />
              {vmsList.loading ? (
                <p className="loading">Loading…</p>
              ) : vmsList.error ? (
                <div className="error">Failed to load: {vmsList.error}</div>
              ) : sorted.length === 0 ? (
                <p className="muted empty">No virtual machines match these filters.</p>
              ) : (
                <div className="table-wrap">
                  <table className="entities" ref={tableRef}>
                  <thead>
                    <tr>
                      <SortHeader
                        label="Name"
                        sortKey="name"
                        activeKey={controls.sort}
                        asc={controls.order === 'asc'}
                        onToggle={controls.toggleSort}
                      />
                      <SortHeader
                        label="Role"
                        sortKey="role"
                        activeKey={controls.sort}
                        asc={controls.order === 'asc'}
                        onToggle={controls.toggleSort}
                      />
                      <th>Cloud account</th>
                      <SortHeader
                        label="Region"
                        sortKey="region"
                        activeKey={controls.sort}
                        asc={controls.order === 'asc'}
                        onToggle={controls.toggleSort}
                      />
                      <SortHeader
                        label="Zone"
                        sortKey="zone"
                        activeKey={controls.sort}
                        asc={controls.order === 'asc'}
                        onToggle={controls.toggleSort}
                      />
                      <SortHeader
                        label="Instance type"
                        sortKey="instance_type"
                        activeKey={controls.sort}
                        asc={controls.order === 'asc'}
                        onToggle={controls.toggleSort}
                      />
                      <SortHeader
                        label="Image"
                        sortKey="image_name"
                        activeKey={controls.sort}
                        asc={controls.order === 'asc'}
                        onToggle={controls.toggleSort}
                      />
                      <th>Image ID</th>
                      <th>Private IP</th>
                      <SortHeader
                        label="Power state"
                        sortKey="power_state"
                        activeKey={controls.sort}
                        asc={controls.order === 'asc'}
                        onToggle={controls.toggleSort}
                      />
                      <SortHeader
                        label="Last seen"
                        sortKey="last_seen_at"
                        activeKey={controls.sort}
                        asc={controls.order === 'asc'}
                        onToggle={controls.toggleSort}
                      />
                    </tr>
                  </thead>
                  <tbody>
                    {sorted.map((vm) => {
                      const acct = accountsById.get(vm.cloud_account_id);
                      const isTerminated = !!vm.terminated_at;
                      return (
                        <tr key={vm.id} className={isTerminated ? 'vm-row-terminated' : ''}>
                          <td>
                            <Link to={`/virtual-machines/${vm.id}`}>
                              <strong>{vm.display_name || vm.name}</strong>
                            </Link>
                            {vm.display_name && (
                              <div className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
                                {vm.name}
                              </div>
                            )}
                          </td>
                          <td>
                            {splitRoles(vm.role).length > 0 ? (
                              <span style={{ display: 'inline-flex', gap: '0.25rem', flexWrap: 'wrap' }}>
                                {splitRoles(vm.role).map((r) => (
                                  <span key={r} className="pill">
                                    {r}
                                  </span>
                                ))}
                              </span>
                            ) : (
                              <Dash />
                            )}
                          </td>
                          <td>
                            {acct ? (
                              <span>
                                {acct.name}{' '}
                                <span className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
                                  {acct.provider}
                                </span>
                              </span>
                            ) : (
                              <span className="muted">{vm.cloud_account_id.slice(0, 8)}…</span>
                            )}
                          </td>
                          <td>{vm.region ? <code>{vm.region}</code> : <Dash />}</td>
                          <td>{vm.zone ? <code>{vm.zone}</code> : <Dash />}</td>
                          <td>{vm.instance_type ? <code>{vm.instance_type}</code> : <Dash />}</td>
                          <td>{vm.image_name ? <span>{vm.image_name}</span> : <Dash />}</td>
                          <td>{vm.image_id ? <code>{vm.image_id}</code> : <Dash />}</td>
                          <td>
                            {vm.private_ip ? (
                              <span>
                                <code>{vm.private_ip}</code>
                                {vm.public_ip && (
                                  <div
                                    className="muted"
                                    style={{ fontSize: 'var(--fs-sm)', marginTop: '0.1rem' }}
                                  >
                                    <code>{vm.public_ip}</code> public
                                  </div>
                                )}
                              </span>
                            ) : (
                              <Dash />
                            )}
                          </td>
                          <td>
                            <PowerStatePill state={vm.power_state} />
                          </td>
                          <td className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
                            {formatTs(vm.last_seen_at) || <Dash />}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
                </div>
              )}
            </>
          );
        })()}
    </>
  );
}

function SummaryCard({
  label,
  count,
  active,
  cls,
  onClick,
}: {
  label: string;
  count: number;
  active: boolean;
  cls: string;
  onClick: () => void;
}) {
  return (
    <div
      className={`eol-summary-card ${cls}${active ? ' eol-active' : ''}`}
      onClick={onClick}
    >
      <span className="eol-summary-count">{count}</span>
      <span className="eol-summary-label">{label}</span>
    </div>
  );
}
