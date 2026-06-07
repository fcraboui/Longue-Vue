import type { Flow } from '../../api/flows';
import { SectionTitle } from '../../components';
import { FlowTable } from './FlowTable';

// InternalPanel renders the internal (NetworkPolicy) layer of the
// synthesis — east/west flows between workloads inside the cluster.
export function InternalPanel({ flows }: { flows: Flow[] }) {
  return (
    <section style={{ marginBottom: '1.5rem' }}>
      <SectionTitle count={flows.length}>Internal</SectionTitle>
      <FlowTable flows={flows} emptyMessage="No internal flows match the current filters." />
    </section>
  );
}
