package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/metrics"
)

var errKyvernoSweepFailed = errors.New("sweep kyverno policies: one or more operations failed")

// SettingsGetter is the minimal settings interface the Kyverno collector
// needs to gate itself behind policies_enabled. Both *store.PG and
// apiclient.Store can satisfy it.
type SettingsGetter interface {
	GetSettings(ctx context.Context) (api.Settings, error)
}

// KyvernoStore is the slice of the store interface the Kyverno collector
// uses. Both *store.PG (direct, in-process) and apiclient.Store (HTTP
// push via the ingest GW) can satisfy this interface. Defined here so a
// test fake can stub without dragging the full store. ADR-0043.
type KyvernoStore interface {
	UpsertClusterPolicy(ctx context.Context, cp api.ClusterPolicyRow) (uuid.UUID, error)
	DeleteClusterPoliciesNotIn(ctx context.Context, clusterID uuid.UUID, keepIDs []uuid.UUID) (int64, error)
	UpsertPolicyReport(ctx context.Context, pr api.PolicyReportRow) (uuid.UUID, error)
	DeletePolicyReportsNotIn(ctx context.Context, clusterID uuid.UUID, keepIDs []uuid.UUID) (int64, error)
}

// CollectKyvernoPolicies runs one tick of Kyverno policy and policy-report
// reconciliation for the local cluster. Issues FOUR list calls (two
// cluster-scoped, two namespace-wide) via the dynamic client, groups
// results, upserts each row, and sweeps stale entries per cluster.
// ADR-0043 §5.
//
// MUST only be called after the kube list calls succeed — transient API
// errors must never wipe the store (reconcile contract per CLAUDE.md).
func CollectKyvernoPolicies(
	ctx context.Context,
	src KubeSource,
	st KyvernoStore,
	clusterID uuid.UUID,
	clusterName string,
	namespaceIDsByName map[string]uuid.UUID,
) error {
	var policyFailures, reportFailures int

	cpIDs, err := collectClusterPolicies(ctx, src, st, clusterID, clusterName, namespaceIDsByName)
	if err != nil {
		slog.Warn("collector: kyverno cluster-policies tick failed",
			slog.String("cluster", clusterID.String()),
			slog.Any("err", err))
		policyFailures++
	} else {
		metrics.MarkPoll(clusterName, "cluster_policies")
	}

	if cpIDs != nil {
		deleted, err := st.DeleteClusterPoliciesNotIn(ctx, clusterID, cpIDs)
		if err != nil {
			slog.Warn("collector: sweep kyverno cluster-policies failed",
				slog.String("cluster", clusterID.String()),
				slog.Any("err", err))
			metrics.ObserveError(clusterName, "cluster_policies", "reconcile")
			policyFailures++
		} else {
			metrics.ObserveReconciled(clusterName, "cluster_policies", deleted)
		}
	}

	prIDs, err := collectPolicyReports(ctx, src, st, clusterID, clusterName, namespaceIDsByName)
	if err != nil {
		slog.Warn("collector: kyverno policy-reports tick failed",
			slog.String("cluster", clusterID.String()),
			slog.Any("err", err))
		reportFailures++
	} else {
		metrics.MarkPoll(clusterName, "policy_reports")
	}

	if prIDs != nil {
		deleted, err := st.DeletePolicyReportsNotIn(ctx, clusterID, prIDs)
		if err != nil {
			slog.Warn("collector: sweep kyverno policy-reports failed",
				slog.String("cluster", clusterID.String()),
				slog.Any("err", err))
			metrics.ObserveError(clusterName, "policy_reports", "reconcile")
			reportFailures++
		} else {
			metrics.ObserveReconciled(clusterName, "policy_reports", deleted)
		}
	}

	if policyFailures+reportFailures > 0 {
		return fmt.Errorf("%w (%d policy, %d report)", errKyvernoSweepFailed, policyFailures, reportFailures)
	}
	return nil
}

// collectClusterPolicies upserts all Kyverno ClusterPolicy + Policy rows
// and returns the set of row UUIDs seen this tick (for reconcile sweep).
func collectClusterPolicies(
	ctx context.Context,
	src KubeSource,
	st KyvernoStore,
	clusterID uuid.UUID,
	clusterName string,
	namespaceIDsByName map[string]uuid.UUID,
) ([]uuid.UUID, error) {
	seen := make([]uuid.UUID, 0)
	var listErrors int

	clusterPol, err := src.ListKyvernoClusterPolicies(ctx)
	if err != nil {
		metrics.ObserveError(clusterName, "cluster_policies", "list")
		return nil, fmt.Errorf("list kyverno clusterpolicies: %w", err)
	}
	for i := range clusterPol {
		cp := &clusterPol[i]
		row := kyvernoPolicyToRow(cp, clusterID, nil)
		id, err := st.UpsertClusterPolicy(ctx, row)
		if err != nil {
			slog.Warn("collector: upsert kyverno clusterpolicy failed",
				slog.String("policy", cp.Name),
				slog.String("cluster", clusterID.String()),
				slog.Any("err", err))
			metrics.ObserveError(clusterName, "cluster_policies", "upsert")
			listErrors++
			continue
		}
		seen = append(seen, id)
	}

	namespacedPol, err := src.ListKyvernoPolicies(ctx)
	if err != nil {
		metrics.ObserveError(clusterName, "cluster_policies", "list")
		return nil, fmt.Errorf("list kyverno policies: %w", err)
	}
	for i := range namespacedPol {
		p := &namespacedPol[i]
		nsID, ok := namespaceIDsByName[p.Namespace]
		if !ok {
			slog.Warn("collector: kyverno policy in unknown namespace; skipping",
				slog.String("policy", p.Name),
				slog.String("namespace", p.Namespace),
				slog.String("cluster", clusterID.String()))
			metrics.ObserveError(clusterName, "cluster_policies", "namespace_unknown")
			listErrors++
			continue
		}
		row := kyvernoPolicyToRow(p, clusterID, &nsID)
		id, err := st.UpsertClusterPolicy(ctx, row)
		if err != nil {
			slog.Warn("collector: upsert kyverno policy failed",
				slog.String("policy", p.Name),
				slog.String("namespace", p.Namespace),
				slog.String("cluster", clusterID.String()),
				slog.Any("err", err))
			metrics.ObserveError(clusterName, "cluster_policies", "upsert")
			listErrors++
			continue
		}
		seen = append(seen, id)
	}

	metrics.ObserveUpserts(clusterName, "cluster_policies", len(seen))
	if listErrors > 0 {
		return nil, fmt.Errorf("%d cluster-policy upsert errors", listErrors)
	}
	return seen, nil
}

// collectPolicyReports upserts all PolicyReport + ClusterPolicyReport rows
// and returns the set of row UUIDs seen this tick (for reconcile sweep).
func collectPolicyReports(
	ctx context.Context,
	src KubeSource,
	st KyvernoStore,
	clusterID uuid.UUID,
	clusterName string,
	namespaceIDsByName map[string]uuid.UUID,
) ([]uuid.UUID, error) {
	seen := make([]uuid.UUID, 0)
	var listErrors int

	clusterReports, err := src.ListKyvernoClusterPolicyReports(ctx)
	if err != nil {
		metrics.ObserveError(clusterName, "policy_reports", "list")
		return nil, fmt.Errorf("list kyverno clusterpolicyreports: %w", err)
	}
	for i := range clusterReports {
		r := &clusterReports[i]
		row := kyvernoReportToRow(r, clusterID, nil)
		id, err := st.UpsertPolicyReport(ctx, row)
		if err != nil {
			slog.Warn("collector: upsert kyverno clusterpolicyreport failed",
				slog.String("report", r.Name),
				slog.String("cluster", clusterID.String()),
				slog.Any("err", err))
			metrics.ObserveError(clusterName, "policy_reports", "upsert")
			listErrors++
			continue
		}
		seen = append(seen, id)
	}

	namespacedReports, err := src.ListKyvernoPolicyReports(ctx)
	if err != nil {
		metrics.ObserveError(clusterName, "policy_reports", "list")
		return nil, fmt.Errorf("list kyverno policyreports: %w", err)
	}
	for i := range namespacedReports {
		r := &namespacedReports[i]
		nsID, ok := namespaceIDsByName[r.Namespace]
		if !ok {
			slog.Warn("collector: kyverno policyreport in unknown namespace; skipping",
				slog.String("report", r.Name),
				slog.String("namespace", r.Namespace),
				slog.String("cluster", clusterID.String()))
			metrics.ObserveError(clusterName, "policy_reports", "namespace_unknown")
			listErrors++
			continue
		}
		row := kyvernoReportToRow(r, clusterID, &nsID)
		id, err := st.UpsertPolicyReport(ctx, row)
		if err != nil {
			slog.Warn("collector: upsert kyverno policyreport failed",
				slog.String("report", r.Name),
				slog.String("namespace", r.Namespace),
				slog.String("cluster", clusterID.String()),
				slog.Any("err", err))
			metrics.ObserveError(clusterName, "policy_reports", "upsert")
			listErrors++
			continue
		}
		seen = append(seen, id)
	}

	metrics.ObserveUpserts(clusterName, "policy_reports", len(seen))
	if listErrors > 0 {
		return nil, fmt.Errorf("%d policy-report upsert errors", listErrors)
	}
	return seen, nil
}

// kyvernoPolicyToRow converts a KyvernoClusterPolicyInfo to an
// api.ClusterPolicyRow for upsert.
func kyvernoPolicyToRow(info *KyvernoClusterPolicyInfo, clusterID uuid.UUID, namespaceID *uuid.UUID) api.ClusterPolicyRow {
	now := time.Now().UTC()
	row := api.ClusterPolicyRow{
		ClusterID:       clusterID,
		NamespaceID:     namespaceID,
		Name:            info.Name,
		ResourceType:    info.ResourceType,
		Scope:           info.Scope,
		Description:     info.Description,
		Category:        info.Category,
		Severity:        info.Severity,
		Action:          info.Action,
		FailurePolicy:   info.FailurePolicy,
		Background:      info.Background,
		RuleTypes:       info.RuleTypes,
		TargetResources: info.TargetResources,
		KeyExclusions:   info.KeyExclusions,
		Ready:           info.Ready,
		Annotations:     json.RawMessage(info.Annotations),
		SpecRaw:         json.RawMessage(info.SpecRaw),
		ReconcileSeenAt: now,
	}
	if info.RulesCount > 0 {
		rc := info.RulesCount
		row.RulesCount = &rc
	}
	return row
}

// kyvernoReportToRow converts a KyvernoPolicyReportInfo to an
// api.PolicyReportRow for upsert.
func kyvernoReportToRow(info *KyvernoPolicyReportInfo, clusterID uuid.UUID, namespaceID *uuid.UUID) api.PolicyReportRow {
	return api.PolicyReportRow{
		ClusterID:       clusterID,
		NamespaceID:     namespaceID,
		Name:            info.Name,
		ScopeKind:       info.ScopeKind,
		ScopeName:       info.ScopeName,
		SummaryPass:     info.SummaryPass,
		SummaryFail:     info.SummaryFail,
		SummaryWarn:     info.SummaryWarn,
		SummaryError:    info.SummaryError,
		SummarySkip:     info.SummarySkip,
		ResultsRaw:      json.RawMessage(info.ResultsRaw),
		ReconcileSeenAt: time.Now().UTC(),
	}
}
