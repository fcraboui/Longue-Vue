// SearchInput is the uniform list search box (ADR-0042 phase 2).
// Debouncing lives in useListControls — this component is presentation
// only. The tooltip documents the `*` glob convention the server applies.
export function SearchInput({
  value,
  onChange,
  placeholder = 'Filter by name… (* = wildcard)',
  label = 'Name',
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  label?: string;
}) {
  return (
    <label className="list-search">
      <span>{label}</span>
      <input
        type="search"
        value={value}
        placeholder={placeholder}
        title="Plain text matches anywhere in the name. Use * to anchor: du* = starts with, *du = ends with."
        onChange={(e) => onChange(e.target.value)}
      />
    </label>
  );
}
