import type { Flow, Endpoint } from '../../api/flows';
import { Empty } from '../../components';
import { StatePill } from './StatePill';
import { formatPorts } from './ports';

// EndpointCell renders one side of a flow: the endpoint name plus its CIDR
// in muted text when present (e.g. "Internet (0.0.0.0/0)").
function EndpointCell({ endpoint }: { endpoint: Endpoint }) {
  return (
    <span>
      <strong>{endpoint.name}</strong>
      {endpoint.cidr && (
        <span className="muted" style={{ marginLeft: '0.35rem' }}>
          ({endpoint.cidr})
        </span>
      )}
    </span>
  );
}

// FlowTable is the shared one-row-per-flow table used by both the
// perimeter and internal panels. Columns: direction, protocol/ports,
// source → destination, and the classification pill.
export function FlowTable({ flows, emptyMessage }: { flows: Flow[]; emptyMessage: string }) {
  if (flows.length === 0) {
    return <Empty message={emptyMessage} />;
  }
  return (
    <table className="entities flow-table">
      <thead>
        <tr>
          <th>Direction</th>
          <th>Ports</th>
          <th>Source</th>
          <th aria-label="to" />
          <th>Destination</th>
          <th>State</th>
        </tr>
      </thead>
      <tbody>
        {flows.map((f, i) => (
          <tr key={i}>
            <td>{f.direction}</td>
            <td>
              <code>{formatPorts(f.ports)}</code>
            </td>
            <td>
              <EndpointCell endpoint={f.src} />
            </td>
            <td className="muted flow-arrow">→</td>
            <td>
              <EndpointCell endpoint={f.dst} />
            </td>
            <td>
              <StatePill state={f.state} />
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
