import { render, screen, fireEvent } from '@testing-library/react';
import { SortHeader } from './SortHeader';

function renderTh(ui: React.ReactElement) {
  // th needs table ancestry to render without DOM nesting warnings.
  return render(<table><thead><tr>{ui}</tr></thead></table>);
}

describe('SortHeader', () => {
  it('shows no arrow when inactive and calls onToggle with its key', () => {
    const onToggle = vi.fn();
    renderTh(
      <SortHeader label="Name" sortKey="name" activeKey="" asc onToggle={onToggle} />,
    );
    const th = screen.getByRole('columnheader');
    expect(th.textContent).toBe('Name');
    fireEvent.click(th);
    expect(onToggle).toHaveBeenCalledWith('name');
  });

  it('shows ▲ when active asc and ▼ when active desc', () => {
    const { rerender } = renderTh(
      <SortHeader label="Name" sortKey="name" activeKey="name" asc onToggle={() => {}} />,
    );
    expect(screen.getByRole('columnheader').textContent).toBe('Name ▲');
    rerender(
      <table><thead><tr>
        <SortHeader label="Name" sortKey="name" activeKey="name" asc={false} onToggle={() => {}} />
      </tr></thead></table>,
    );
    expect(screen.getByRole('columnheader').textContent).toBe('Name ▼');
  });
});
