package store

import (
	"context"
	"fmt"

	"github.com/sthalbert/longue-vue/internal/api"
)

// ListOSImages returns the deduplicated inventory of OS images in service:
// every distinct image_name referenced by a non-terminated VM or an active
// node, with distinct image ids and per-source counts (ADR-0040).
func (p *PG) ListOSImages(ctx context.Context) ([]api.OSImage, error) {
	const q = `
		WITH used AS (
		  SELECT image_name, image_id, 'vm'   AS src FROM virtual_machines
		   WHERE terminated_at IS NULL AND image_name IS NOT NULL AND image_name <> ''
		  UNION ALL
		  SELECT image_name, image_id, 'node' AS src FROM nodes
		   WHERE terminated_at IS NULL AND image_name IS NOT NULL AND image_name <> ''
		)
		SELECT image_name,
		       COALESCE(
		         array_agg(DISTINCT image_id) FILTER (WHERE image_id IS NOT NULL AND image_id <> ''),
		         '{}'
		       ) AS image_ids,
		       count(*) FILTER (WHERE src = 'vm')   AS vm_count,
		       count(*) FILTER (WHERE src = 'node') AS node_count
		  FROM used
		 GROUP BY image_name
		 ORDER BY image_name`
	rows, err := p.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query os images: %w", err)
	}
	defer rows.Close()
	out := make([]api.OSImage, 0, 32)
	for rows.Next() {
		var im api.OSImage
		if err := rows.Scan(&im.ImageName, &im.ImageIDs, &im.VMCount, &im.NodeCount); err != nil {
			return nil, fmt.Errorf("scan os image: %w", err)
		}
		out = append(out, im)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate os images: %w", err)
	}
	return out, nil
}
