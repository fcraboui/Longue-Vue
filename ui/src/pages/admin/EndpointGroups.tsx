import { FormEvent, useState } from 'react';
import { useResource } from '../../hooks';
import { AsyncView, SectionTitle } from '../../components';
import { ApiError } from '../../api';
import {
  listEndpointGroups,
  createEndpointGroup,
  updateEndpointGroup,
  deleteEndpointGroup,
  type EndpointGroup,
} from '../../api/endpointGroups';

// EndpointGroupsPage — admin tab for managing endpoint groups (named CIDR
// sets) referenced by perimeter flow references (R2 Task 13). Mirrors the
// ImageRegistries admin tab: a collapsible create form above an entities
// table with per-row inline edit + delete. Admin-scoped server-side.

type Reload = () => void;

// parseCidrs splits a comma/newline-separated textarea value into a trimmed
// CIDR list, dropping empties.
function parseCidrs(raw: string): string[] {
  return raw
    .split(/[\n,]+/)
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
}

export default function EndpointGroupsPage() {
  const [nonce, setNonce] = useState(0);
  const reload: Reload = () => setNonce((n) => n + 1);
  const state = useResource(() => listEndpointGroups(), [nonce]);

  return (
    <AsyncView state={state}>
      {(resp) => (
        <>
          <CreateForm reload={reload} />
          <SectionTitle count={resp.items.length}>Endpoint groups</SectionTitle>
          <p className="muted" style={{ marginTop: 0, fontSize: 'var(--fs-base)' }}>
            Named CIDR sets reusable as the source or destination of perimeter
            flow references (e.g. <code>internet</code> = <code>0.0.0.0/0</code>,
            or an office VPN range).
          </p>
          {resp.items.length === 0 ? (
            <p className="muted">No endpoint groups defined yet.</p>
          ) : (
            <table className="entities">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>CIDRs</th>
                  <th>Notes</th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {[...resp.items]
                  .sort((a, b) => a.name.localeCompare(b.name))
                  .map((g) => (
                    <GroupRow key={g.id} group={g} reload={reload} />
                  ))}
              </tbody>
            </table>
          )}
        </>
      )}
    </AsyncView>
  );
}

function CreateForm({ reload }: { reload: Reload }) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [notes, setNotes] = useState('');
  const [cidrs, setCidrs] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const reset = () => {
    setName('');
    setNotes('');
    setCidrs('');
    setError(null);
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    if (!name.trim()) {
      setError('name is required');
      return;
    }
    setBusy(true);
    try {
      await createEndpointGroup({
        name: name.trim(),
        notes: notes.trim(),
        cidrs: parseCidrs(cidrs),
      });
      reset();
      setOpen(false);
      reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  if (!open) {
    return (
      <div className="admin-actions">
        <button className="primary" onClick={() => setOpen(true)}>
          + Add endpoint group
        </button>
      </div>
    );
  }

  return (
    <form className="admin-form" onSubmit={submit}>
      <h3>Add endpoint group</h3>
      <div className="admin-form-row">
        <div>
          <label>Name</label>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="internet"
            autoFocus
          />
        </div>
        <div style={{ flex: 1 }}>
          <label>Notes (optional)</label>
          <input
            value={notes}
            onChange={(e) => setNotes(e.target.value)}
            placeholder="e.g. public internet"
          />
        </div>
      </div>
      <div className="admin-form-row">
        <div style={{ flex: 1 }}>
          <label>CIDRs</label>
          <textarea
            value={cidrs}
            onChange={(e) => setCidrs(e.target.value)}
            placeholder="0.0.0.0/0&#10;10.0.0.0/8"
            rows={3}
          />
          <small className="muted">One CIDR per line, or comma-separated.</small>
        </div>
      </div>
      <div className="admin-form-actions">
        <button type="submit" className="primary" disabled={busy}>
          {busy ? 'Adding…' : 'Add endpoint group'}
        </button>
        <button
          type="button"
          onClick={() => {
            reset();
            setOpen(false);
          }}
          disabled={busy}
        >
          Cancel
        </button>
      </div>
      {error && <div className="error">{error}</div>}
    </form>
  );
}

function GroupRow({ group, reload }: { group: EndpointGroup; reload: Reload }) {
  const [editing, setEditing] = useState(false);
  const [notes, setNotes] = useState(group.notes ?? '');
  const [cidrs, setCidrs] = useState(group.cidrs.join('\n'));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const onDelete = async () => {
    if (!confirm(`Delete endpoint group "${group.name}"?`)) return;
    setBusy(true);
    try {
      await deleteEndpointGroup(group.id);
      reload();
    } catch (err) {
      alert(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const onSave = async () => {
    setError(null);
    setBusy(true);
    try {
      await updateEndpointGroup(group.id, {
        name: group.name,
        notes: notes.trim(),
        cidrs: parseCidrs(cidrs),
      });
      setEditing(false);
      reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const startEdit = () => {
    setNotes(group.notes ?? '');
    setCidrs(group.cidrs.join('\n'));
    setError(null);
    setEditing(true);
  };

  if (editing) {
    return (
      <tr>
        <td>
          <code>{group.name}</code>
        </td>
        <td>
          <textarea
            value={cidrs}
            onChange={(e) => setCidrs(e.target.value)}
            rows={3}
            style={{ width: '100%' }}
          />
        </td>
        <td>
          <input
            value={notes}
            onChange={(e) => setNotes(e.target.value)}
            style={{ width: '100%' }}
          />
        </td>
        <td style={{ textAlign: 'right' }}>
          <button onClick={onSave} disabled={busy} className="primary">
            {busy ? 'Saving…' : 'Save'}
          </button>{' '}
          <button onClick={() => setEditing(false)} disabled={busy}>
            Cancel
          </button>
          {error && <div className="error">{error}</div>}
        </td>
      </tr>
    );
  }

  return (
    <tr>
      <td>
        <code>{group.name}</code>
      </td>
      <td>
        {group.cidrs.length === 0 ? (
          <span className="muted">—</span>
        ) : (
          <span className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
            {group.cidrs.length} CIDR(s): <code>{group.cidrs.join(', ')}</code>
          </span>
        )}
      </td>
      <td className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
        {group.notes ?? ''}
      </td>
      <td style={{ textAlign: 'right' }}>
        <button onClick={startEdit} disabled={busy}>
          Edit
        </button>{' '}
        <button onClick={onDelete} disabled={busy} className="danger">
          Delete
        </button>
      </td>
    </tr>
  );
}
