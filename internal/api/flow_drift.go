package api

// Throttled flow_drift audit events at flow-matrix synthesis time
// (ADR-0036, R3 Task 2). When the read-time synthesis classifies an actual
// flow as non_declare — an implemented rule with no operator-declared
// reference row — that is drift worth a compliance trail. We emit one
// audit_events row (source="system") per drifting flow, throttled to at
// most one per (cluster, flow_key) per 24h via store.RecordFlowDriftSeen.
//
// Only non_declare counts as drift here. large_ouvert is a hardening
// recommendation (default-allow / 0.0.0.0/0) and manquant is a coverage
// gap (a reference with no implementing rule); neither is alerted as
// drift. Emission is strictly best-effort: throttle-check and insert
// failures are logged and swallowed so a flaky audit table can never turn
// a successful synthesis read into a 5xx.

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/flowmatrix"
)

// driftAny is the placeholder used in a flow key when a flow carries no
// concrete protocol/port (the synthesizer left the port range empty).
const driftAny = "any"

// driftKeys returns a stable throttle key per non_declare flow in a synthesis.
// Only non_declare counts as drift (large_ouvert = hardening recommendation,
// manquant = coverage gap; neither is alerted as drift).
//
//nolint:gocritic // hugeParam: Synthesis passed by value mirrors the read-time comparator API; called once per request.
func driftKeys(syn flowmatrix.Synthesis) []string {
	var keys []string
	all := append(append([]flowmatrix.Flow{}, syn.Perimeter...), syn.Internal...)
	for i := range all {
		if all[i].State != flowmatrix.StateNonDeclare {
			continue
		}
		keys = append(keys, flowDriftKey(all[i]))
	}
	return keys
}

// flowDriftKey builds the per-flow throttle key. It is intentionally coarse
// (direction|src|dst|proto|ports) so cosmetic differences don't defeat the
// 24h throttle; it must match the key the PG layer keys its marker on.
//
//nolint:gocritic // hugeParam: Flow passed by value; callers iterate by index and pass the copy, value semantics are intentional.
func flowDriftKey(f flowmatrix.Flow) string {
	proto, ports := driftAny, driftAny
	if len(f.Ports) > 0 {
		proto = f.Ports[0].Protocol
		if f.Ports[0].From != nil && f.Ports[0].To != nil {
			ports = fmt.Sprintf("%d-%d", *f.Ports[0].From, *f.Ports[0].To)
		}
	}
	return fmt.Sprintf("%s|%s|%s|%s|%s", f.Direction, f.Src.Name, f.Dst.Name, proto, ports)
}

// emitFlowDrift writes a throttled flow_drift audit event per non_declare flow.
// Best-effort: throttle/insert errors are logged, never surfaced to the client.
//
//nolint:gocritic // hugeParam: Synthesis passed by value mirrors the read-time comparator API; called once per request.
func emitFlowDrift(
	ctx context.Context,
	store Store,
	clusterID uuid.UUID,
	syn flowmatrix.Synthesis,
) {
	all := append(append([]flowmatrix.Flow{}, syn.Perimeter...), syn.Internal...)
	for i := range all {
		f := all[i]
		if f.State != flowmatrix.StateNonDeclare {
			continue
		}
		key := flowDriftKey(f)
		emit, err := store.RecordFlowDriftSeen(ctx, clusterID, key, 24*time.Hour)
		if err != nil {
			slog.Warn("flow drift: throttle check failed",
				slog.String("cluster_id", clusterID.String()),
				slog.String("flow_key", key),
				slog.Any("error", err))
			continue
		}
		if !emit {
			continue
		}
		ev := AuditEventInsert{
			ID:           uuid.New(),
			OccurredAt:   time.Now().UTC(),
			ActorKind:    "system",
			Action:       "flow_drift",
			ResourceType: "cluster",
			ResourceID:   clusterID.String(),
			Source:       "system",
			Details: map[string]any{
				"action":     "flow_drift",
				"cluster_id": clusterID.String(),
				"flow_key":   key,
				"layer":      f.Layer,
				"direction":  f.Direction,
				"src":        f.Src,
				"dst":        f.Dst,
				"ports":      f.Ports,
				"state":      string(f.State),
			},
		}
		if err := store.InsertAuditEvent(ctx, ev); err != nil {
			slog.Warn("flow drift: audit insert failed",
				slog.String("cluster_id", clusterID.String()),
				slog.String("flow_key", key),
				slog.Any("error", err))
		}
	}
}
