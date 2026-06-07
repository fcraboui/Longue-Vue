import { useMemo, useState } from 'react';
import * as api from '../api';
import { useResource } from '../hooks';
import { AsyncView } from '../components';
import { FlowsIcon } from '../icons';
import {
  getClusterFlowMatrix,
  FlowMatrixDisabledError,
  type Flow,
  type FlowState,
  type Synthesis,
} from '../api/flows';
import { PerimeterPanel } from '../components/flows/PerimeterPanel';
import { InternalPanel } from '../components/flows/InternalPanel';
import { StatePill } from '../components/flows/StatePill';

// Flows — the top-level flow-matrix tool (R2 Task 11). An operator picks a
// cluster; we fetch the read-time synthesis from
// GET /v1/clusters/{id}/flow-matrix and render the perimeter + internal
// panels with client-side filters on state and direction. The feature is
// gated server-side by flow_matrix_enabled, so a 409 surfaces as a
// dedicated "feature disabled" panel rather than an error.

const ALL_STATES: FlowState[] = ['conforme', 'non_declare', 'manquant', 'large_ouvert'];

// directionsOf collects the distinct directions present in a synthesis so
// the direction filter only offers values that actually appear.
function directionsOf(s: Synthesis): string[] {
  const set = new Set<string>();
  for (const f of [...s.perimeter, ...s.internal]) set.add(f.direction);
  return Array.from(set).sort();
}

function applyFilters(
  flows: Flow[],
  states: Set<FlowState>,
  direction: string,
): Flow[] {
  return flows.filter((f) => {
    if (states.size > 0 && !states.has(f.state)) return false;
    if (direction && f.direction !== direction) return false;
    return true;
  });
}

export default function Flows() {
  const clustersState = useResource(() => api.listClusters(), []);
  const [clusterId, setClusterId] = useState('');

  return (
    <>
      <h2>
        <FlowsIcon size={20} /> Flow Matrix
      </h2>
      <p className="muted" style={{ marginTop: 0 }}>
        Per-cluster comparison of the discovered network posture (perimeter
        security-group rules and internal NetworkPolicy rules) against the
        operator-declared reference matrix.
      </p>

      <AsyncView state={clustersState}>
        {(clustersResp) => {
          const clusters = clustersResp.items;
          return (
            <>
              <div style={{ marginBottom: '1.25rem' }}>
                <label style={{ display: 'flex', gap: '0.5rem', alignItems: 'center' }}>
                  <span>Cluster</span>
                  <select
                    value={clusterId}
                    aria-label="Select cluster"
                    onChange={(e) => setClusterId(e.target.value)}
                  >
                    <option value="">— select a cluster —</option>
                    {clusters.map((c) => (
                      <option key={c.id} value={c.id}>
                        {c.display_name || c.name}
                      </option>
                    ))}
                  </select>
                </label>
              </div>

              {clusterId ? (
                <FlowMatrixView clusterId={clusterId} />
              ) : (
                <p className="muted">Select a cluster to view its flow matrix.</p>
              )}
            </>
          );
        }}
      </AsyncView>
    </>
  );
}

function FlowMatrixView({ clusterId }: { clusterId: string }) {
  // Re-fetch whenever the chosen cluster changes. The fetcher resolves to
  // a discriminated result so the disabled (409) case renders distinctly
  // from a hard error.
  const state = useResource<
    | { kind: 'ok'; synthesis: Synthesis }
    | { kind: 'disabled'; detail: string }
  >(
    () =>
      getClusterFlowMatrix(clusterId)
        .then((synthesis) => ({ kind: 'ok' as const, synthesis }))
        .catch((err: unknown) => {
          if (err instanceof FlowMatrixDisabledError) {
            return { kind: 'disabled' as const, detail: err.message };
          }
          throw err;
        }),
    [clusterId],
  );

  const [stateFilter, setStateFilter] = useState<Set<FlowState>>(new Set());
  const [direction, setDirection] = useState('');

  const toggleState = (s: FlowState) => {
    setStateFilter((prev) => {
      const next = new Set(prev);
      if (next.has(s)) next.delete(s);
      else next.add(s);
      return next;
    });
  };

  return (
    <AsyncView state={state}>
      {(result) => {
        if (result.kind === 'disabled') {
          return (
            <div className="banner banner-warn">
              <span>
                The flow matrix feature is disabled. Enable{' '}
                <strong>flow_matrix_enabled</strong> in <strong>Admin &gt; Settings</strong>{' '}
                to use this tool.
              </span>
            </div>
          );
        }
        return (
          <FlowMatrixBody
            synthesis={result.synthesis}
            stateFilter={stateFilter}
            direction={direction}
            onToggleState={toggleState}
            onDirection={setDirection}
            onClear={() => {
              setStateFilter(new Set());
              setDirection('');
            }}
          />
        );
      }}
    </AsyncView>
  );
}

function FlowMatrixBody({
  synthesis,
  stateFilter,
  direction,
  onToggleState,
  onDirection,
  onClear,
}: {
  synthesis: Synthesis;
  stateFilter: Set<FlowState>;
  direction: string;
  onToggleState: (s: FlowState) => void;
  onDirection: (v: string) => void;
  onClear: () => void;
}) {
  const directions = useMemo(() => directionsOf(synthesis), [synthesis]);
  const perimeter = useMemo(
    () => applyFilters(synthesis.perimeter, stateFilter, direction),
    [synthesis.perimeter, stateFilter, direction],
  );
  const internal = useMemo(
    () => applyFilters(synthesis.internal, stateFilter, direction),
    [synthesis.internal, stateFilter, direction],
  );

  const hasFilters = stateFilter.size > 0 || !!direction;

  return (
    <>
      {synthesis.warnings.length > 0 && (
        <div className="banner banner-warn" role="alert" style={{ display: 'block' }}>
          <strong>{synthesis.warnings.length} warning(s):</strong>
          <ul style={{ margin: '0.4rem 0 0', paddingLeft: '1.2rem' }}>
            {synthesis.warnings.map((w, i) => (
              <li key={i}>
                {w.kind}
                {w.ref && (
                  <>
                    {' '}— <code>{w.ref}</code>
                  </>
                )}
                {w.reference_id && (
                  <span className="muted"> (reference {w.reference_id.slice(0, 8)}…)</span>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}

      <div className="flow-filter-bar">
        <div className="flow-filter-group">
          <span className="muted">State:</span>
          {ALL_STATES.map((s) => (
            <button
              key={s}
              type="button"
              className={`flow-filter-pill${stateFilter.has(s) ? ' flow-filter-pill--active' : ''}`}
              aria-pressed={stateFilter.has(s)}
              onClick={() => onToggleState(s)}
            >
              <StatePill state={s} />
            </button>
          ))}
        </div>
        <label className="flow-filter-group">
          <span className="muted">Direction:</span>
          <select value={direction} onChange={(e) => onDirection(e.target.value)} aria-label="Filter by direction">
            <option value="">all</option>
            {directions.map((d) => (
              <option key={d} value={d}>
                {d}
              </option>
            ))}
          </select>
        </label>
        {hasFilters && (
          <button type="button" className="link-btn" onClick={onClear}>
            clear filters
          </button>
        )}
      </div>

      <PerimeterPanel flows={perimeter} />
      <InternalPanel flows={internal} />
    </>
  );
}
