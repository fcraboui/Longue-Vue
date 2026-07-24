// IncompleteListBanner flags a view built from FIRST-PAGE fetches when
// the server says more rows exist (next_cursor). Sibling of
// TruncationBanner (which is extract-driven); this one is list-driven.
// Non-dismissible: the incompleteness doesn't go away by closing it.
export function IncompleteListBanner({ visible, what }: { visible: boolean; what: string }) {
  if (!visible) return null;
  return (
    <div className="banner banner-warn">
      <span>
        Showing only the first page of {what} — totals and coverage are partial. Use the
        extract for a complete view.
      </span>
    </div>
  );
}
