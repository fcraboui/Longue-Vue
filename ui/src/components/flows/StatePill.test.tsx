import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { StatePill } from './StatePill';
import type { FlowState } from '../../api/flows';

describe('StatePill', () => {
  const cases: Array<{ state: FlowState; label: string }> = [
    { state: 'conforme', label: 'Conforme' },
    { state: 'non_declare', label: 'Non déclaré' },
    { state: 'manquant', label: 'Manquant' },
    { state: 'large_ouvert', label: 'Large ouvert' },
  ];

  for (const { state, label } of cases) {
    it(`renders the French label and state class for ${state}`, () => {
      const { container } = render(<StatePill state={state} />);
      const span = container.querySelector('span');
      expect(span).not.toBeNull();
      expect(span!.textContent).toBe(label);
      expect(span!.className).toContain('flow-pill');
      expect(span!.className).toContain(`flow-pill--${state}`);
    });
  }
});
