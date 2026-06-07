import type { FlowState } from '../../api/flows';

// French labels for each synthesized flow state — the UI's audience is
// French SecNumCloud operators, but the rest of this SPA renders English
// chrome, so we keep the surrounding UI English and only translate the
// classification term itself (which matches the SNC vocabulary on the
// server: conforme / non_declare / manquant / large_ouvert).
const LABELS: Record<FlowState, string> = {
  conforme: 'Conforme',
  non_declare: 'Non déclaré',
  manquant: 'Manquant',
  large_ouvert: 'Large ouvert',
};

// StatePill renders a flow's classification as a coloured pill. The class
// `flow-pill--<state>` carries the colour (see styles.css).
export function StatePill({ state }: { state: FlowState }) {
  return <span className={`flow-pill flow-pill--${state}`}>{LABELS[state]}</span>;
}
