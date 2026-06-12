// Small helpers shared between the detail pages under pages/details/.
// SectionTitle / KV / Dash and friends already live in src/components.tsx;
// only detail-page-specific chrome belongs here.

// Simple tab bar used by ClusterDetail and WorkloadDetail. Uses existing
// design-system tokens.
export function TabBar({
  active,
  tabs,
  onChange,
}: {
  active: string;
  tabs: { id: string; label: string }[];
  onChange: (id: string) => void;
}) {
  return (
    <div style={{ display: 'flex', gap: 0, borderBottom: '2px solid var(--border)', marginBottom: '1rem' }}>
      {tabs.map((t) => (
        <button
          key={t.id}
          onClick={() => onChange(t.id)}
          style={{
            background: 'none',
            border: 'none',
            borderBottom: active === t.id ? '2px solid var(--accent)' : '2px solid transparent',
            marginBottom: -2,
            padding: '0.5rem 1rem',
            cursor: 'pointer',
            color: active === t.id ? 'var(--accent)' : 'var(--fg-muted)',
            fontWeight: active === t.id ? 600 : 400,
            fontSize: '0.9rem',
          }}
        >
          {t.label}
        </button>
      ))}
    </div>
  );
}
