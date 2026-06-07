import type { Flow } from '../../api/flows';
import { SectionTitle } from '../../components';
import { FlowTable } from './FlowTable';

// PerimeterPanel renders the perimeter (security-group) layer of the
// synthesis — north/south flows between the cluster and external CIDRs.
export function PerimeterPanel({ flows }: { flows: Flow[] }) {
  return (
    <section style={{ marginBottom: '1.5rem' }}>
      <SectionTitle count={flows.length}>Perimeter</SectionTitle>
      <FlowTable flows={flows} emptyMessage="No perimeter flows match the current filters." />
    </section>
  );
}
