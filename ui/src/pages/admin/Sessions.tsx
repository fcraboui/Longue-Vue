import { useState } from 'react';
import * as api from '../../api';
import { Dash, Paginator, SectionTitle } from '../../components';
import { useEntityTable } from '../../components/column_filters';
import { SearchInput } from '../../components/SearchInput';
import { SortHeader } from '../../components/SortHeader';
import { useListControls, usePagedList } from '../../hooks';

// Admin Sessions page. Read-only table with a revoke action. The `id`
// column is the server-side public UUID, never the cookie value —
// admins can address a row without being able to impersonate the user
// by copying it into their own browser.

export default function SessionsPage() {
  const [nonce, setNonce] = useState(0);
  const reload = () => setNonce((n) => n + 1);
  const controls = useListControls();
  const list = usePagedList<api.Session>(
    (cursor, limit) => api.listSessions({ ...controls.params, cursor, limit }),
    [...controls.deps, nonce],
  );
  const tableRef = useEntityTable('admin.sessions');

  return (
    <>
      <SectionTitle count={list.items.length}>Active sessions</SectionTitle>
      <p className="muted" style={{ fontSize: '0.85rem', marginTop: 0 }}>
        Only non-expired sessions are listed. Revoking a session logs that
        user's tab out server-side — the next request the browser makes
        bounces back to the login page.
      </p>
      <div className="vm-filters">
        <SearchInput value={controls.nameInput} onChange={controls.setNameInput} label="Username" />
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
        <p className="muted">No active sessions.</p>
      ) : (
        <table className="entities" ref={tableRef}>
          <thead>
            <tr>
              <SortHeader
                label="User"
                sortKey="username"
                activeKey={controls.sort}
                asc={controls.order === 'asc'}
                onToggle={controls.toggleSort}
              />
              <SortHeader
                label="Created"
                sortKey="created_at"
                activeKey={controls.sort}
                asc={controls.order === 'asc'}
                onToggle={controls.toggleSort}
              />
              <SortHeader
                label="Last used"
                sortKey="last_used_at"
                activeKey={controls.sort}
                asc={controls.order === 'asc'}
                onToggle={controls.toggleSort}
              />
              <SortHeader
                label="Expires"
                sortKey="expires_at"
                activeKey={controls.sort}
                asc={controls.order === 'asc'}
                onToggle={controls.toggleSort}
              />
              <th>User agent</th>
              <th>Source IP</th>
              <th style={{ textAlign: 'right' }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {list.items.map((s) => (
              <SessionRow key={s.id} session={s} reload={reload} />
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}

function SessionRow({ session, reload }: { session: api.Session; reload: () => void }) {
  const [busy, setBusy] = useState(false);
  const revoke = async () => {
    if (!confirm(`Revoke session for ${session.username || session.user_id}?`)) return;
    setBusy(true);
    try {
      await api.revokeSession(session.id);
      reload();
    } catch (err) {
      alert(err instanceof api.ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <tr>
      <td>
        <strong>{session.username || session.user_id}</strong>
      </td>
      <td>{formatTs(session.created_at)}</td>
      <td>{formatTs(session.last_used_at)}</td>
      <td>{formatTs(session.expires_at)}</td>
      <td>
        {session.user_agent ? (
          <span className="muted" style={{ fontSize: '0.8rem' }}>
            {session.user_agent}
          </span>
        ) : (
          <Dash />
        )}
      </td>
      <td>{session.source_ip ? <code>{session.source_ip}</code> : <Dash />}</td>
      <td style={{ textAlign: 'right' }}>
        <button onClick={revoke} disabled={busy} className="danger">
          Revoke
        </button>
      </td>
    </tr>
  );
}

function formatTs(ts: string): string {
  try {
    return new Date(ts).toLocaleString();
  } catch {
    return ts;
  }
}
