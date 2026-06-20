import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor } from '@testing-library/react';
import OSImages from './OSImages';
import { renderWithRouter } from '../test/render';
import { server } from '../test/server';
import { fixtureOSImage } from '../test/fixtures';

describe('OSImages list', () => {
  it('renders the heading', () => {
    renderWithRouter(<OSImages />, { initialPath: '/os-images' });
    expect(screen.getByRole('heading', { name: /os images/i })).toBeInTheDocument();
  });

  it('renders an image row once data arrives', async () => {
    renderWithRouter(<OSImages />, { initialPath: '/os-images' });
    await waitFor(() =>
      expect(screen.getByText(fixtureOSImage.image_name)).toBeInTheDocument(),
    );
  });

  it('renders error state on 500', async () => {
    server.use(
      http.get('/v1/os-images', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(<OSImages />, { initialPath: '/os-images' });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});
