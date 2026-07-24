import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { IncompleteListBanner } from './IncompleteListBanner';

describe('IncompleteListBanner', () => {
  it('renders nothing when visible is false', () => {
    const { container } = render(
      <IncompleteListBanner visible={false} what="matching workloads" />,
    );
    expect(container.firstChild).toBeNull();
  });

  it('shows the banner with first-page text and the what prop when visible', () => {
    render(<IncompleteListBanner visible={true} what="matching workloads" />);
    expect(screen.getByText(/first page of/i)).toBeInTheDocument();
    expect(screen.getByText(/matching workloads/i)).toBeInTheDocument();
  });
});
