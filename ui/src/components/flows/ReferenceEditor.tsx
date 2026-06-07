import { FormEvent, useRef, useState } from 'react';
import { ApiError } from '../../api';
import { useResource } from '../../hooks';
import { AsyncView, Empty } from '../../components';
import { canEdit, useMe } from '../../me';
import {
  createFlowReference,
  deleteFlowReference,
  exportFlowReferencesURL,
  importFlowReferencesYAML,
  listFlowReferences,
  type FlowDirection,
  type FlowEndpointKind,
  type FlowLayer,
  type FlowProtocol,
  type FlowReference,
  type FlowReferenceInput,
} from '../../api/flows';

// ReferenceEditor is the operator-facing CRUD surface for the
// reference matrix (R2 Task 12). The matrix is the declared source of
// truth the synthesis panels above compare the discovered posture
// against, so every mutation calls `onChange` to re-fetch the synthesis
// and keep the classification pills live.
//
// Reads are visible to everyone; writes are editor-scoped server-side, so
// the add form / delete / import affordances are gated on `canEdit` for UX
// (the API enforces scope regardless).

const LAYERS: FlowLayer[] = ['perimeter', 'internal'];
const DIRECTIONS: FlowDirection[] = ['ingress', 'egress'];
const KINDS: FlowEndpointKind[] = ['workload', 'service', 'namespace', 'endpoint_group'];
const PROTOCOLS: FlowProtocol[] = ['tcp', 'udp', 'icmp', 'any'];

// formatPorts renders the port span on a reference row.
function formatPorts(ref: FlowReference): string {
  const proto = ref.protocol || 'any';
  if (ref.from_port === undefined || ref.from_port === null) {
    return `${proto}/any`;
  }
  if (ref.to_port !== undefined && ref.to_port !== null && ref.to_port !== ref.from_port) {
    return `${proto}/${ref.from_port}-${ref.to_port}`;
  }
  return `${proto}/${ref.from_port}`;
}

export function ReferenceEditor({
  clusterId,
  onChange,
}: {
  clusterId: string;
  onChange?: () => void;
}) {
  const me = useMe();
  const editable = canEdit(me);
  // refreshKey bumps to force a re-list after any successful mutation.
  const [refreshKey, setRefreshKey] = useState(0);
  const state = useResource(
    () => listFlowReferences(clusterId),
    [clusterId, refreshKey],
  );

  const refetch = () => {
    setRefreshKey((k) => k + 1);
    onChange?.();
  };

  return (
    <section style={{ marginTop: '2rem' }}>
      <h3 className="section-title">Reference matrix</h3>
      <p className="muted" style={{ marginTop: 0 }}>
        The operator-declared set of allowed flows. The synthesis above
        classifies the discovered posture against these references.
      </p>

      <AsyncView state={state}>
        {(resp) => (
          <ReferenceEditorBody
            clusterId={clusterId}
            editable={editable}
            references={resp.items}
            onRefetch={refetch}
          />
        )}
      </AsyncView>
    </section>
  );
}

function ReferenceEditorBody({
  clusterId,
  editable,
  references,
  onRefetch,
}: {
  clusterId: string;
  editable: boolean;
  references: FlowReference[];
  onRefetch: () => void;
}) {
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [rowError, setRowError] = useState<string | null>(null);

  const onDelete = async (id: string) => {
    setRowError(null);
    setDeletingId(id);
    try {
      await deleteFlowReference(id);
      onRefetch();
    } catch (err) {
      setRowError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setDeletingId(null);
    }
  };

  return (
    <>
      <div
        style={{
          display: 'flex',
          gap: '1rem',
          alignItems: 'center',
          marginBottom: '0.75rem',
          flexWrap: 'wrap',
        }}
      >
        {editable && (
          <ImportControl clusterId={clusterId} onRefetch={onRefetch} />
        )}
        <a
          className="link-btn"
          href={exportFlowReferencesURL(clusterId)}
          download={`flow-references-${clusterId}.yaml`}
        >
          Export YAML
        </a>
      </div>

      {rowError && <div className="error">{rowError}</div>}

      {references.length === 0 ? (
        <Empty message="No references declared yet." />
      ) : (
        <table className="entities">
          <thead>
            <tr>
              <th>Layer</th>
              <th>Direction</th>
              <th>Source</th>
              <th>Destination</th>
              <th>Ports</th>
              <th>Justification</th>
              {editable && <th aria-label="actions" />}
            </tr>
          </thead>
          <tbody>
            {references.map((ref) => (
              <tr key={ref.id}>
                <td>{ref.layer}</td>
                <td>{ref.direction}</td>
                <td>
                  <code>
                    {ref.src_kind}:{ref.src_ref}
                  </code>
                </td>
                <td>
                  <code>
                    {ref.dst_kind}:{ref.dst_ref}
                  </code>
                </td>
                <td>
                  <code>{formatPorts(ref)}</code>
                </td>
                <td>{ref.justification}</td>
                {editable && (
                  <td>
                    <button
                      type="button"
                      className="link-btn"
                      disabled={deletingId === ref.id}
                      onClick={() => onDelete(ref.id)}
                    >
                      {deletingId === ref.id ? 'Deleting…' : 'Delete'}
                    </button>
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {editable && <AddReferenceForm clusterId={clusterId} onRefetch={onRefetch} />}
    </>
  );
}

// ImportControl wraps a hidden file input behind a button. Picking a YAML
// file confirms (replace-all is destructive), reads its text, and POSTs it.
function ImportControl({
  clusterId,
  onRefetch,
}: {
  clusterId: string;
  onRefetch: () => void;
}) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const onFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    // Reset the input so picking the same file again re-fires onChange.
    e.target.value = '';
    if (!file) return;
    if (!window.confirm('Replace all references for this cluster?')) return;
    setError(null);
    setBusy(true);
    try {
      const text = await file.text();
      await importFlowReferencesYAML(clusterId, text);
      onRefetch();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <span style={{ display: 'inline-flex', gap: '0.5rem', alignItems: 'center' }}>
      <input
        ref={inputRef}
        type="file"
        accept=".yaml,.yml,text/yaml"
        style={{ display: 'none' }}
        aria-label="Import references YAML"
        onChange={onFile}
      />
      <button
        type="button"
        className="link-btn"
        disabled={busy}
        onClick={() => inputRef.current?.click()}
      >
        {busy ? 'Importing…' : 'Import YAML (replace all)'}
      </button>
      {error && <span className="error">{error}</span>}
    </span>
  );
}

// AddReferenceForm is the inline create form. On 4xx it surfaces the
// server's validation detail inline; on success it clears and refetches.
function AddReferenceForm({
  clusterId,
  onRefetch,
}: {
  clusterId: string;
  onRefetch: () => void;
}) {
  const [layer, setLayer] = useState<FlowLayer>('perimeter');
  const [direction, setDirection] = useState<FlowDirection>('ingress');
  const [srcKind, setSrcKind] = useState<FlowEndpointKind>('endpoint_group');
  const [srcRef, setSrcRef] = useState('');
  const [dstKind, setDstKind] = useState<FlowEndpointKind>('workload');
  const [dstRef, setDstRef] = useState('');
  const [protocol, setProtocol] = useState<FlowProtocol>('tcp');
  const [fromPort, setFromPort] = useState('');
  const [toPort, setToPort] = useState('');
  const [justification, setJustification] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const reset = () => {
    setSrcRef('');
    setDstRef('');
    setFromPort('');
    setToPort('');
    setJustification('');
  };

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    const input: FlowReferenceInput = {
      layer,
      direction,
      src_kind: srcKind,
      src_ref: srcRef.trim(),
      dst_kind: dstKind,
      dst_ref: dstRef.trim(),
      protocol,
      from_port: fromPort.trim() === '' ? null : Number(fromPort),
      to_port: toPort.trim() === '' ? null : Number(toPort),
      justification: justification.trim(),
    };
    setBusy(true);
    try {
      await createFlowReference(clusterId, input);
      reset();
      onRefetch();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form className="admin-form" onSubmit={onSubmit} style={{ marginTop: '1.25rem' }}>
      <h4 style={{ margin: '0 0 0.5rem' }}>Add reference</h4>
      <div className="admin-form-row">
        <div>
          <label>Layer</label>
          <select value={layer} onChange={(e) => setLayer(e.target.value as FlowLayer)}>
            {LAYERS.map((l) => (
              <option key={l} value={l}>
                {l}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label>Direction</label>
          <select
            value={direction}
            onChange={(e) => setDirection(e.target.value as FlowDirection)}
          >
            {DIRECTIONS.map((d) => (
              <option key={d} value={d}>
                {d}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label>Protocol</label>
          <select
            value={protocol}
            onChange={(e) => setProtocol(e.target.value as FlowProtocol)}
          >
            {PROTOCOLS.map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
          </select>
        </div>
      </div>

      <div className="admin-form-row" style={{ marginTop: '0.75rem' }}>
        <div>
          <label>Source kind</label>
          <select
            value={srcKind}
            onChange={(e) => setSrcKind(e.target.value as FlowEndpointKind)}
          >
            {KINDS.map((k) => (
              <option key={k} value={k}>
                {k}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label>Source ref</label>
          <input
            type="text"
            value={srcRef}
            onChange={(e) => setSrcRef(e.target.value)}
            placeholder="name or endpoint group"
          />
        </div>
        <div>
          <label>Destination kind</label>
          <select
            value={dstKind}
            onChange={(e) => setDstKind(e.target.value as FlowEndpointKind)}
          >
            {KINDS.map((k) => (
              <option key={k} value={k}>
                {k}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label>Destination ref</label>
          <input
            type="text"
            value={dstRef}
            onChange={(e) => setDstRef(e.target.value)}
            placeholder="name or endpoint group"
          />
        </div>
      </div>

      <div className="admin-form-row" style={{ marginTop: '0.75rem' }}>
        <div>
          <label>From port</label>
          <input
            type="number"
            value={fromPort}
            onChange={(e) => setFromPort(e.target.value)}
            placeholder="optional"
          />
        </div>
        <div>
          <label>To port</label>
          <input
            type="number"
            value={toPort}
            onChange={(e) => setToPort(e.target.value)}
            placeholder="optional"
          />
        </div>
      </div>

      <div style={{ marginTop: '0.75rem' }}>
        <label>Justification</label>
        <textarea
          value={justification}
          onChange={(e) => setJustification(e.target.value)}
          rows={3}
          required
          style={{ width: '100%' }}
          placeholder="Why this flow is allowed (required)"
        />
      </div>

      {error && <div className="error">{error}</div>}

      <div className="admin-form-actions">
        <button type="submit" className="primary" disabled={busy}>
          {busy ? 'Adding…' : 'Add reference'}
        </button>
      </div>
    </form>
  );
}
