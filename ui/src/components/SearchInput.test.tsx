import { render, screen, fireEvent } from '@testing-library/react';
import { SearchInput } from './SearchInput';

describe('SearchInput', () => {
  it('renders the glob hint and forwards changes', () => {
    const onChange = vi.fn();
    render(<SearchInput value="" onChange={onChange} />);
    const input = screen.getByPlaceholderText('Filter by name… (* = wildcard)');
    expect(input).toHaveAttribute('title', expect.stringContaining('du* = starts with'));
    fireEvent.change(input, { target: { value: 'prod-*' } });
    expect(onChange).toHaveBeenCalledWith('prod-*');
  });
});
