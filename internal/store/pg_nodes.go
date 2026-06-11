// Node CRUD + upsert + reconcile (DeleteNodesNotIn). Split out of pg.go.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/timetravel"
)

// CreateNode inserts a new node. Returns api.ErrNotFound when the parent
// cluster does not exist (FK violation), api.ErrConflict on duplicate
// (cluster_id, name).
// nodeColumns is the INSERT/SELECT column order used by every Node SQL
// path — CreateNode, UpsertNode, scanNode, ListNodes. Kept as a single
// const so adding a field is a three-line change (const + values + scan).
const nodeColumns = `id, cluster_id, name, display_name, role,
	kubelet_version, kube_proxy_version, container_runtime_version,
	os_image, operating_system, kernel_version, architecture,
	internal_ip, external_ip, pod_cidr,
	provider_id, instance_type, zone,
	capacity_cpu, capacity_memory, capacity_pods, capacity_ephemeral_storage,
	allocatable_cpu, allocatable_memory, allocatable_pods, allocatable_ephemeral_storage,
	conditions, taints, unschedulable, ready,
	labels,
	owner, criticality, notes, runbook_url, annotations, hardware_model,
	created_at, updated_at`

// nodeColumnsUAliased is nodeColumns with every column qualified by the `u`
// alias plus the denormalized cluster_name from the LEFT JOIN — used by
// UpsertNode's final SELECT, which joins `upserted u` against the snapshot
// CTE `old o` and clusters `c`, and must disambiguate columns present in
// more than one source.
const nodeColumnsUAliased = `u.id, u.cluster_id, u.name, u.display_name, u.role,
	u.kubelet_version, u.kube_proxy_version, u.container_runtime_version,
	u.os_image, u.operating_system, u.kernel_version, u.architecture,
	u.internal_ip, u.external_ip, u.pod_cidr,
	u.provider_id, u.instance_type, u.zone,
	u.capacity_cpu, u.capacity_memory, u.capacity_pods, u.capacity_ephemeral_storage,
	u.allocatable_cpu, u.allocatable_memory, u.allocatable_pods, u.allocatable_ephemeral_storage,
	u.conditions, u.taints, u.unschedulable, u.ready,
	u.labels,
	u.owner, u.criticality, u.notes, u.runbook_url, u.annotations, u.hardware_model,
	u.created_at, u.updated_at,
	c.name AS cluster_name`

// nodeSelectColumns is the read-side projection: every column from the
// nodes row (aliased to `n.`) plus the denormalized cluster name joined
// in via LEFT JOIN (ADR-0027). LEFT JOIN keeps orphan rows visible with
// a NULL cluster_name — the UI renders that as an explicit badge.
const nodeSelectColumns = `n.id, n.cluster_id, n.name, n.display_name, n.role,
	n.kubelet_version, n.kube_proxy_version, n.container_runtime_version,
	n.os_image, n.operating_system, n.kernel_version, n.architecture,
	n.internal_ip, n.external_ip, n.pod_cidr,
	n.provider_id, n.instance_type, n.zone,
	n.capacity_cpu, n.capacity_memory, n.capacity_pods, n.capacity_ephemeral_storage,
	n.allocatable_cpu, n.allocatable_memory, n.allocatable_pods, n.allocatable_ephemeral_storage,
	n.conditions, n.taints, n.unschedulable, n.ready,
	n.labels,
	n.owner, n.criticality, n.notes, n.runbook_url, n.annotations, n.hardware_model,
	n.created_at, n.updated_at,
	c.name AS cluster_name`

const nodeFromJoined = `FROM nodes n
	LEFT JOIN clusters c ON c.id = n.cluster_id`

func nodeInsertValues(in *api.NodeCreate, id uuid.UUID, now time.Time) ([]any, error) {
	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return nil, err
	}
	annotationsJSON, err := marshalLabels(in.Annotations)
	if err != nil {
		return nil, fmt.Errorf("marshal node annotations: %w", err)
	}
	conditionsJSON, err := marshalPorts(in.Conditions)
	if err != nil {
		return nil, err
	}
	taintsJSON, err := marshalPorts(in.Taints)
	if err != nil {
		return nil, err
	}
	return []any{
		id, in.ClusterId, in.Name, in.DisplayName, in.Role,
		in.KubeletVersion, in.KubeProxyVersion, in.ContainerRuntimeVersion,
		in.OsImage, in.OperatingSystem, in.KernelVersion, in.Architecture,
		in.InternalIp, in.ExternalIp, in.PodCidr,
		in.ProviderId, in.InstanceType, in.Zone,
		in.CapacityCpu, in.CapacityMemory, in.CapacityPods, in.CapacityEphemeralStorage,
		in.AllocatableCpu, in.AllocatableMemory, in.AllocatablePods, in.AllocatableEphemeralStorage,
		conditionsJSON, taintsJSON, boolOrFalse(in.Unschedulable), boolOrFalse(in.Ready),
		labelsJSON,
		in.Owner, in.Criticality, in.Notes, in.RunbookUrl, annotationsJSON, in.HardwareModel,
		now,
	}, nil
}

func boolOrFalse(b *bool) bool {
	if b == nil {
		return false
	}
	return *b
}

// CreateNode inserts a new node into the given cluster.
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) CreateNode(ctx context.Context, in api.NodeCreate) (api.Node, error) {
	id := uuid.New()
	now := time.Now().UTC()

	values, err := nodeInsertValues(&in, id, now)
	if err != nil {
		return api.Node{}, err
	}

	// 38 placeholders: 36 "value" slots + created_at + updated_at (both = $38).
	const q = `
		INSERT INTO nodes (` + nodeColumns + `)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$38)
	`
	if _, err := p.pool.Exec(ctx, q, values...); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505":
				return api.Node{}, fmt.Errorf("node %q in cluster %s already exists: %w", in.Name, in.ClusterId, api.ErrConflict)
			case "23503":
				return api.Node{}, fmt.Errorf("cluster %s does not exist: %w", in.ClusterId, api.ErrNotFound)
			}
		}
		return api.Node{}, fmt.Errorf("insert node: %w", err)
	}

	return p.GetNode(ctx, id)
}

// GetNode fetches a node by id, including the denormalized cluster_name
// from the node's cluster (ADR-0027).
func (p *PG) GetNode(ctx context.Context, id uuid.UUID) (api.Node, error) {
	q := `SELECT ` + nodeSelectColumns + ` ` + nodeFromJoined + ` WHERE n.id = $1`
	row := p.pool.QueryRow(ctx, q, id)
	n, err := scanNode(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.Node{}, api.ErrNotFound
		}
		return api.Node{}, fmt.Errorf("select node: %w", err)
	}
	return n, nil
}

// ListNodes returns up to limit nodes sorted (created_at DESC, id DESC),
// optionally filtered by cluster id.
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListNodes(ctx context.Context, clusterID *uuid.UUID, limit int, cursor string, includeTerminated bool) ([]api.Node, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT `)
	sb.WriteString(nodeSelectColumns)
	sb.WriteString(` `)
	sb.WriteString(nodeFromJoined)
	args := make([]any, 0, 4)
	conds := make([]string, 0, 3)

	if !includeTerminated {
		conds = append(conds, "n.terminated_at IS NULL")
	}
	if clusterID != nil {
		args = append(args, *clusterID)
		conds = append(conds, fmt.Sprintf("n.cluster_id = $%d", len(args)))
	}
	if cursor != "" {
		ts, cid, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		args = append(args, ts)
		tsIdx := len(args)
		args = append(args, cid)
		idIdx := len(args)
		conds = append(conds, fmt.Sprintf("(n.created_at, n.id) < ($%d, $%d)", tsIdx, idIdx))
	}
	if len(conds) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(conds, " AND "))
	}
	args = append(args, limit+1)
	fmt.Fprintf(&sb, " ORDER BY n.created_at DESC, n.id DESC LIMIT $%d", len(args))

	rows, err := p.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, "", fmt.Errorf("query nodes: %w", err)
	}
	defer rows.Close()

	items := make([]api.Node, 0, limit)
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan node: %w", err)
		}
		items = append(items, n)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate nodes: %w", err)
	}

	var next string
	if len(items) > limit {
		last := items[limit-1]
		if last.CreatedAt != nil && last.Id != nil {
			next = encodeCursor(*last.CreatedAt, *last.Id)
		}
		items = items[:limit]
	}
	return items, next, nil
}

// buildNodeUpdateSets converts a NodeUpdate merge-patch into the SET clause
// fragments and positional arg slice consumed by UpdateNode. A trailing
// updated_at set is always appended; the caller owns the id arg for the
// WHERE clause and the surrounding transaction.
func buildNodeUpdateSets(in *api.NodeUpdate) (sets []string, args []any, err error) {
	sets = make([]string, 0, 24)
	args = make([]any, 0, 26)
	add := func(column string, value any) {
		sets = append(sets, fmt.Sprintf("%s=$%d", column, len(sets)+1))
		args = append(args, value)
	}
	addPtr := func(column string, ptr any) {
		v := reflect.ValueOf(ptr)
		if v.Kind() == reflect.Ptr && !v.IsNil() {
			add(column, v.Elem().Interface())
		}
	}

	addPtr("display_name", in.DisplayName)
	addPtr("role", in.Role)
	addPtr("kubelet_version", in.KubeletVersion)
	addPtr("kube_proxy_version", in.KubeProxyVersion)
	addPtr("container_runtime_version", in.ContainerRuntimeVersion)
	addPtr("os_image", in.OsImage)
	addPtr("operating_system", in.OperatingSystem)
	addPtr("kernel_version", in.KernelVersion)
	addPtr("architecture", in.Architecture)
	addPtr("internal_ip", in.InternalIp)
	addPtr("external_ip", in.ExternalIp)
	addPtr("pod_cidr", in.PodCidr)
	addPtr("provider_id", in.ProviderId)
	addPtr("instance_type", in.InstanceType)
	addPtr("zone", in.Zone)
	addPtr("capacity_cpu", in.CapacityCpu)
	addPtr("capacity_memory", in.CapacityMemory)
	addPtr("capacity_pods", in.CapacityPods)
	addPtr("capacity_ephemeral_storage", in.CapacityEphemeralStorage)
	addPtr("allocatable_cpu", in.AllocatableCpu)
	addPtr("allocatable_memory", in.AllocatableMemory)
	addPtr("allocatable_pods", in.AllocatablePods)
	addPtr("allocatable_ephemeral_storage", in.AllocatableEphemeralStorage)
	var jsonErr error
	addJSON := func(column string, ptr any, marshal func() ([]byte, error)) {
		if jsonErr != nil {
			return
		}
		v := reflect.ValueOf(ptr)
		if v.Kind() != reflect.Ptr || v.IsNil() {
			return
		}
		b, err := marshal()
		if err != nil {
			jsonErr = err
			return
		}
		add(column, b)
	}
	addJSON("conditions", in.Conditions, func() ([]byte, error) { return marshalPorts(in.Conditions) })
	addJSON("taints", in.Taints, func() ([]byte, error) { return marshalPorts(in.Taints) })
	addPtr("unschedulable", in.Unschedulable)
	addPtr("ready", in.Ready)
	addJSON("labels", in.Labels, func() ([]byte, error) { return marshalLabels(in.Labels) })
	// Curated metadata — collector never writes these, so merge-patch
	// omission is enough to keep operator edits safe across polls.
	addPtr("owner", in.Owner)
	addPtr("criticality", in.Criticality)
	addPtr("notes", in.Notes)
	addPtr("runbook_url", in.RunbookUrl)
	addJSON("annotations", in.Annotations, func() ([]byte, error) {
		b, err := marshalLabels(in.Annotations)
		if err != nil {
			return nil, fmt.Errorf("marshal node annotations: %w", err)
		}
		return b, nil
	})
	if jsonErr != nil {
		return nil, nil, jsonErr
	}
	addPtr("hardware_model", in.HardwareModel)
	add("updated_at", time.Now().UTC())
	return sets, args, nil
}

// UpdateNode applies merge-patch semantics on mutable fields only. Each
// non-nil pointer on NodeUpdate translates to a single column set; omitted
// fields keep their existing value.
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) UpdateNode(ctx context.Context, id uuid.UUID, in api.NodeUpdate) (api.Node, error) {
	sets, args, err := buildNodeUpdateSets(&in)
	if err != nil {
		return api.Node{}, err
	}
	idx := len(sets) + 1
	args = append(args, id)

	tx, txErr := p.pool.Begin(ctx)
	if txErr != nil {
		return api.Node{}, fmt.Errorf("begin update node: %w", txErr)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	prev, _ := nodeRowMap(ctx, tx, id) // FOR UPDATE; ignore error for capture
	if prev == nil {
		return api.Node{}, api.ErrNotFound
	}

	q := fmt.Sprintf("UPDATE nodes SET %s WHERE id=$%d", strings.Join(sets, ", "), idx)
	tag, err := tx.Exec(ctx, q, args...)
	if err != nil {
		return api.Node{}, fmt.Errorf("update node: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.Node{}, api.ErrNotFound
	}

	if next, err := nodeRowMapNoLock(ctx, tx, id); err == nil {
		actor := timetravel.ActorFromContext(ctx)
		_ = timetravel.Capture(ctx, tx, timetravel.KindNode, id, prev, next, changeTypeUpdate, actor)
	}

	if err := tx.Commit(ctx); err != nil {
		return api.Node{}, fmt.Errorf("commit update node: %w", err)
	}
	return p.GetNode(ctx, id)
}

// DeleteNodesNotIn soft-deletes every node of the given cluster whose name
// is not in keepNames AND that is not already terminated. Returns the number
// of rows newly soft-deleted. Despite the name, this is a soft-delete: per
// ADR-0021 §5 the row stays in the table with terminated_at = NOW() so list
// queries can opt back in via include_terminated, history (Phase 2) can
// reconstruct the lifecycle, and a re-appearing node resurrects via the
// upsert path. keepNames may be nil or empty (soft-deletes every live node
// for the cluster).
//
// COALESCE guards against pgx encoding a nil []string as SQL NULL: without
// it, 'name <> ALL(NULL)' evaluates to NULL and the UPDATE matches nothing
// instead of clearing the cluster's nodes.
func (p *PG) DeleteNodesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin delete nodes not in: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Collect IDs that will be soft-deleted so we can write history after the UPDATE.
	toDeleteRows, err := tx.Query(ctx,
		`SELECT id FROM nodes
		  WHERE cluster_id = $1
		    AND name <> ALL(COALESCE($2::text[], ARRAY[]::text[]))
		    AND terminated_at IS NULL`,
		clusterID, keepNames)
	if err != nil {
		return 0, fmt.Errorf("list nodes to soft-delete: %w", err)
	}
	toDelete, err := scanUUIDs(toDeleteRows)
	if err != nil {
		return 0, fmt.Errorf("scan nodes to soft-delete: %w", err)
	}

	tag, err := tx.Exec(ctx,
		`UPDATE nodes
		    SET terminated_at = NOW(), updated_at = NOW()
		  WHERE cluster_id = $1
		    AND name <> ALL(COALESCE($2::text[], ARRAY[]::text[]))
		    AND terminated_at IS NULL`,
		clusterID, keepNames,
	)
	if err != nil {
		return 0, fmt.Errorf("soft-delete nodes not in: %w", err)
	}

	actor := timetravel.ActorFromContext(ctx)
	for _, nodeID := range toDelete {
		if snap, err := nodeRowMapNoLock(ctx, tx, nodeID); err == nil {
			_ = timetravel.Capture(ctx, tx, timetravel.KindNode, nodeID, nil, snap, changeTypeSoftDelete, actor)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit delete nodes not in: %w", err)
	}
	return tag.RowsAffected(), nil
}

// DeleteNode removes a node by id.
func (p *PG) DeleteNode(ctx context.Context, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, "DELETE FROM nodes WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("delete node: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// detectNodeUpsertChangeType inspects the existing node row (if any) keyed
// by (clusterID, name) and returns the timetravel change type to record:
// create when no row exists, restore when a soft-deleted row will be revived,
// update otherwise. Errors from the lookup are swallowed — capture is best-
// effort and the caller treats a missing row as "create".
func detectNodeUpsertChangeType(ctx context.Context, tx pgx.Tx, clusterID uuid.UUID, name string) string {
	var prevTerminatedAt *time.Time
	var prevNodeID *uuid.UUID
	_ = tx.QueryRow(ctx,
		`SELECT id, terminated_at FROM nodes WHERE cluster_id=$1 AND name=$2`,
		clusterID, name,
	).Scan(&prevNodeID, &prevTerminatedAt)
	switch {
	case prevNodeID == nil:
		return changeTypeCreate
	case prevTerminatedAt != nil:
		return changeTypeRestore
	default:
		return changeTypeUpdate
	}
}

// UpsertNode inserts-or-updates a node keyed by (cluster_id, name). The
// unique index on (cluster_id, name) drives the ON CONFLICT target. On
// conflict only mutable columns are overwritten so created_at is preserved.
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) UpsertNode(ctx context.Context, in api.NodeCreate) (api.Node, api.UpsertOutcome, error) {
	id := uuid.New()
	now := time.Now().UTC()

	values, err := nodeInsertValues(&in, id, now)
	if err != nil {
		return api.Node{}, api.OutcomeNoChange, err
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return api.Node{}, api.OutcomeNoChange, fmt.Errorf("begin upsert node: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	changeType := detectNodeUpsertChangeType(ctx, tx, in.ClusterId, in.Name)

	// AUDIT_BUSINESS_FIELDS: display_name, role, kubelet_version, kube_proxy_version,
	// container_runtime_version, os_image, operating_system, kernel_version, architecture,
	// internal_ip, external_ip, pod_cidr, provider_id, instance_type, zone,
	// capacity_cpu, capacity_memory, capacity_pods, capacity_ephemeral_storage,
	// allocatable_cpu, allocatable_memory, allocatable_pods, allocatable_ephemeral_storage,
	// conditions, taints, unschedulable, ready, labels, terminated_at (restore flips it).
	// updated_at is a clock field — excluded. Curator-only fields
	// (owner/criticality/notes/runbook_url/annotations/hardware_model) are not touched
	// by the collector upsert and excluded from the OR-chain.
	const q = `
		WITH old AS (
		  SELECT display_name, role, kubelet_version, kube_proxy_version,
		         container_runtime_version, os_image, operating_system, kernel_version,
		         architecture, internal_ip, external_ip, pod_cidr,
		         provider_id, instance_type, zone,
		         capacity_cpu, capacity_memory, capacity_pods, capacity_ephemeral_storage,
		         allocatable_cpu, allocatable_memory, allocatable_pods, allocatable_ephemeral_storage,
		         conditions, taints, unschedulable, ready, labels, terminated_at
		    FROM nodes WHERE cluster_id=$2 AND name=$3
		),
		upserted AS (
		  INSERT INTO nodes (` + nodeColumns + `)
		    VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$38)
		  ON CONFLICT (cluster_id, name) DO UPDATE SET
		      display_name                  = EXCLUDED.display_name,
		      role                          = EXCLUDED.role,
		      kubelet_version               = EXCLUDED.kubelet_version,
		      kube_proxy_version            = EXCLUDED.kube_proxy_version,
		      container_runtime_version     = EXCLUDED.container_runtime_version,
		      os_image                      = EXCLUDED.os_image,
		      operating_system              = EXCLUDED.operating_system,
		      kernel_version                = EXCLUDED.kernel_version,
		      architecture                  = EXCLUDED.architecture,
		      internal_ip                   = EXCLUDED.internal_ip,
		      external_ip                   = EXCLUDED.external_ip,
		      pod_cidr                      = EXCLUDED.pod_cidr,
		      provider_id                   = EXCLUDED.provider_id,
		      instance_type                 = EXCLUDED.instance_type,
		      zone                          = EXCLUDED.zone,
		      capacity_cpu                  = EXCLUDED.capacity_cpu,
		      capacity_memory               = EXCLUDED.capacity_memory,
		      capacity_pods                 = EXCLUDED.capacity_pods,
		      capacity_ephemeral_storage    = EXCLUDED.capacity_ephemeral_storage,
		      allocatable_cpu               = EXCLUDED.allocatable_cpu,
		      allocatable_memory            = EXCLUDED.allocatable_memory,
		      allocatable_pods              = EXCLUDED.allocatable_pods,
		      allocatable_ephemeral_storage = EXCLUDED.allocatable_ephemeral_storage,
		      conditions                    = EXCLUDED.conditions,
		      taints                        = EXCLUDED.taints,
		      unschedulable                 = EXCLUDED.unschedulable,
		      ready                         = EXCLUDED.ready,
		      labels                        = EXCLUDED.labels,
		      terminated_at                 = NULL,
		      updated_at                    = EXCLUDED.updated_at
		  RETURNING ` + nodeColumns + `, terminated_at, xmax
		)
		SELECT ` + nodeColumnsUAliased + `,
		       (u.xmax = 0) AS inserted,
		       (u.xmax <> 0 AND (
		           o.display_name                  IS DISTINCT FROM u.display_name                  OR
		           o.role                          IS DISTINCT FROM u.role                          OR
		           o.kubelet_version               IS DISTINCT FROM u.kubelet_version               OR
		           o.kube_proxy_version            IS DISTINCT FROM u.kube_proxy_version            OR
		           o.container_runtime_version     IS DISTINCT FROM u.container_runtime_version     OR
		           o.os_image                      IS DISTINCT FROM u.os_image                      OR
		           o.operating_system              IS DISTINCT FROM u.operating_system              OR
		           o.kernel_version                IS DISTINCT FROM u.kernel_version                OR
		           o.architecture                  IS DISTINCT FROM u.architecture                  OR
		           o.internal_ip                   IS DISTINCT FROM u.internal_ip                   OR
		           o.external_ip                   IS DISTINCT FROM u.external_ip                   OR
		           o.pod_cidr                      IS DISTINCT FROM u.pod_cidr                      OR
		           o.provider_id                   IS DISTINCT FROM u.provider_id                   OR
		           o.instance_type                 IS DISTINCT FROM u.instance_type                 OR
		           o.zone                          IS DISTINCT FROM u.zone                          OR
		           o.capacity_cpu                  IS DISTINCT FROM u.capacity_cpu                  OR
		           o.capacity_memory               IS DISTINCT FROM u.capacity_memory               OR
		           o.capacity_pods                 IS DISTINCT FROM u.capacity_pods                 OR
		           o.capacity_ephemeral_storage    IS DISTINCT FROM u.capacity_ephemeral_storage    OR
		           o.allocatable_cpu               IS DISTINCT FROM u.allocatable_cpu               OR
		           o.allocatable_memory            IS DISTINCT FROM u.allocatable_memory            OR
		           o.allocatable_pods              IS DISTINCT FROM u.allocatable_pods              OR
		           o.allocatable_ephemeral_storage IS DISTINCT FROM u.allocatable_ephemeral_storage OR
		           o.conditions                    IS DISTINCT FROM u.conditions                    OR
		           o.taints                        IS DISTINCT FROM u.taints                        OR
		           o.unschedulable                 IS DISTINCT FROM u.unschedulable                 OR
		           o.ready                         IS DISTINCT FROM u.ready                         OR
		           o.labels                        IS DISTINCT FROM u.labels                        OR
		           o.terminated_at                 IS DISTINCT FROM u.terminated_at
		       )) AS business_changed
		  FROM upserted u
		  LEFT JOIN old o      ON true
		  LEFT JOIN clusters c ON c.id = u.cluster_id
	`
	row := tx.QueryRow(ctx, q, values...)
	var inserted, businessChanged bool
	n, err := scanNode(scanRowWith{row: row, extra: []any{&inserted, &businessChanged}})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return api.Node{}, api.OutcomeNoChange, fmt.Errorf("cluster %s does not exist: %w", in.ClusterId, api.ErrNotFound)
		}
		return api.Node{}, api.OutcomeNoChange, fmt.Errorf("upsert node: %w", err)
	}

	actualID := *n.Id
	if snap, err := nodeRowMapNoLock(ctx, tx, actualID); err == nil {
		actor := timetravel.ActorFromContext(ctx)
		_ = timetravel.Capture(ctx, tx, timetravel.KindNode, actualID, nil, snap, changeType, actor)
	}

	if err := tx.Commit(ctx); err != nil {
		return api.Node{}, api.OutcomeNoChange, fmt.Errorf("commit upsert node: %w", err)
	}
	return n, classifyOutcome(inserted, businessChanged), nil
}

func scanNode(row pgx.Row) (api.Node, error) {
	var (
		n                       api.Node
		id                      uuid.UUID
		clusterID               uuid.UUID
		createdAt               time.Time
		updatedAt               time.Time
		displayName             sql.NullString
		role                    sql.NullString
		kubeletVersion          sql.NullString
		kubeProxyVersion        sql.NullString
		containerRuntimeVersion sql.NullString
		osImage                 sql.NullString
		operatingSystem         sql.NullString
		kernelVersion           sql.NullString
		architecture            sql.NullString
		internalIP              sql.NullString
		externalIP              sql.NullString
		podCIDR                 sql.NullString
		providerID              sql.NullString
		instanceType            sql.NullString
		zone                    sql.NullString
		capacityCPU             sql.NullString
		capacityMemory          sql.NullString
		capacityPods            sql.NullString
		capacityEphemeral       sql.NullString
		allocatableCPU          sql.NullString
		allocatableMemory       sql.NullString
		allocatablePods         sql.NullString
		allocatableEphemeral    sql.NullString
		conditionsJSON          []byte
		taintsJSON              []byte
		unschedulable           bool
		ready                   bool
		labelsJSON              []byte
		owner                   sql.NullString
		criticality             sql.NullString
		notes                   sql.NullString
		runbookURL              sql.NullString
		annotationsJSON         []byte
		hardwareModel           sql.NullString
		clusterName             sql.NullString
	)
	if err := row.Scan(
		&id, &clusterID, &n.Name, &displayName, &role,
		&kubeletVersion, &kubeProxyVersion, &containerRuntimeVersion,
		&osImage, &operatingSystem, &kernelVersion, &architecture,
		&internalIP, &externalIP, &podCIDR,
		&providerID, &instanceType, &zone,
		&capacityCPU, &capacityMemory, &capacityPods, &capacityEphemeral,
		&allocatableCPU, &allocatableMemory, &allocatablePods, &allocatableEphemeral,
		&conditionsJSON, &taintsJSON, &unschedulable, &ready,
		&labelsJSON,
		&owner, &criticality, &notes, &runbookURL, &annotationsJSON, &hardwareModel,
		&createdAt, &updatedAt,
		&clusterName,
	); err != nil {
		return api.Node{}, fmt.Errorf("scan node: %w", err)
	}

	n.Id = &id
	n.ClusterId = clusterID
	n.CreatedAt = &createdAt
	n.UpdatedAt = &updatedAt
	n.ClusterName = nullableString(clusterName)
	n.DisplayName = nullableString(displayName)
	n.Role = nullableString(role)
	n.KubeletVersion = nullableString(kubeletVersion)
	n.KubeProxyVersion = nullableString(kubeProxyVersion)
	n.ContainerRuntimeVersion = nullableString(containerRuntimeVersion)
	n.OsImage = nullableString(osImage)
	n.OperatingSystem = nullableString(operatingSystem)
	n.KernelVersion = nullableString(kernelVersion)
	n.Architecture = nullableString(architecture)
	n.InternalIp = nullableString(internalIP)
	n.ExternalIp = nullableString(externalIP)
	n.PodCidr = nullableString(podCIDR)
	n.ProviderId = nullableString(providerID)
	n.InstanceType = nullableString(instanceType)
	n.Zone = nullableString(zone)
	n.CapacityCpu = nullableString(capacityCPU)
	n.CapacityMemory = nullableString(capacityMemory)
	n.CapacityPods = nullableString(capacityPods)
	n.CapacityEphemeralStorage = nullableString(capacityEphemeral)
	n.AllocatableCpu = nullableString(allocatableCPU)
	n.AllocatableMemory = nullableString(allocatableMemory)
	n.AllocatablePods = nullableString(allocatablePods)
	n.AllocatableEphemeralStorage = nullableString(allocatableEphemeral)
	n.Unschedulable = &unschedulable
	n.Ready = &ready

	if cs, err := unmarshalMapArray(conditionsJSON); err != nil {
		return api.Node{}, fmt.Errorf("unmarshal node conditions: %w", err)
	} else {
		n.Conditions = cs
	}
	if ts, err := unmarshalMapArray(taintsJSON); err != nil {
		return api.Node{}, fmt.Errorf("unmarshal node taints: %w", err)
	} else {
		n.Taints = ts
	}

	if len(labelsJSON) > 0 {
		var labels map[string]string
		if err := json.Unmarshal(labelsJSON, &labels); err != nil {
			return api.Node{}, fmt.Errorf("unmarshal node labels: %w", err)
		}
		if len(labels) > 0 {
			n.Labels = &labels
		}
	}

	n.Owner = nullableString(owner)
	n.Criticality = nullableString(criticality)
	n.Notes = nullableString(notes)
	n.RunbookUrl = nullableString(runbookURL)
	n.HardwareModel = nullableString(hardwareModel)
	if len(annotationsJSON) > 0 {
		var annotations map[string]string
		if err := json.Unmarshal(annotationsJSON, &annotations); err != nil {
			return api.Node{}, fmt.Errorf("unmarshal node annotations: %w", err)
		}
		if len(annotations) > 0 {
			n.Annotations = &annotations
		}
	}
	return n, nil
}
