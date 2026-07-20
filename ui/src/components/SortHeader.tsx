// SortHeader is the one clickable column header shared by every sortable
// table (ADR-0042 phase 2). The parent owns the sort state — server-side
// pages keep it in the URL (useListControls), client-side tables keep it
// in local state — and this component only renders the header + arrow.
// Non-sortable columns must render a plain <th> instead (no affordance).
export function SortHeader({
  label,
  sortKey,
  activeKey,
  asc,
  onToggle,
}: {
  label: React.ReactNode;
  sortKey: string;
  activeKey: string;
  asc: boolean;
  onToggle: (key: string) => void;
}) {
  const arrow = activeKey === sortKey ? (asc ? ' ▲' : ' ▼') : '';
  return (
    <th className="sortable" onClick={() => onToggle(sortKey)}>
      {label}
      {arrow}
    </th>
  );
}
