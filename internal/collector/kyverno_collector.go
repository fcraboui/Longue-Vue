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

var errKyvernoTickFailed = errors.New("kyverno tick: one or more operations failed")

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
	DeleteClusterScopedPoliciesNotIn(ctx context.Context, clusterID uuid.UUID, keepIDs []uuid.UUID) (int64, error)
	DeleteClusterPoliciesByNamespace(ctx context.Context, clusterID uuid.UUID, namespaceID uuid.UUID, keepIDs []uuid.UUID) (int64, error)
	UpsertPolicyReport(ctx context.Context, pr api.PolicyReportRow) (uuid.UUID, error)
	DeleteClusterScopedPolicyReportsNotIn(ctx context.Context, clusterID uuid.UUID, keepIDs []uuid.UUID) (int64, error)
	DeletePolicyReportsByNamespace(ctx context.Context, clusterID uuid.UUID, namespaceID uuid.UUID, keepIDs []uuid.UUID) (int64, error)
}

// CollectKyvernoPolicies runs one tick of Kyverno policy and policy-report
// reconciliation for the local cluster. Issues FOUR list calls (two
// cluster-scoped, two namespace-wide) via the dynamic client, groups
// results, upserts each row, and sweeps stale entries per-namespace.
// ADR-0043 §5.
//
// The sweep is per-namespace (following the netpol pattern from
// ADR-0038): only known namespaces are swept, so policies in unknown
// namespaces survive the reconcile tick. Cluster-scoped policies are
// swept separately with DeleteClusterScopedPoliciesNotIn.
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

	cpResult, cpErr := collectClusterPolicies(ctx, src, st, clusterID, clusterName, namespaceIDsByName)
	if cpErr != nil {
		slog.Warn("collector: kyverno cluster-policies tick partially failed",
			slog.String("cluster", clusterID.String()),
			slog.Any("err", cpErr))
		policyFailures++
	}

	if cpResult != nil {
		deleted, err := sweepClusterPolicies(ctx, st, clusterID, namespaceIDsByName, cpResult)
		if err != nil {
			slog.Warn("collector: sweep kyverno cluster-policies failed",
				slog.String("cluster", clusterID.String()),
				slog.Any("err", err))
			metrics.ObserveError(clusterName, "cluster_policies", "reconcile")
			policyFailures++
		} else {
			metrics.ObserveReconciled(clusterName, "cluster_policies", deleted)
		}
		metrics.MarkPoll(clusterName, "cluster_policies")
	}

	prResult, prErr := collectPolicyReports(ctx, src, st, clusterID, clusterName, namespaceIDsByName)
	if prErr != nil {
		slog.Warn("collector: kyverno policy-reports tick partially failed",
			slog.String("cluster", clusterID.String()),
			slog.Any("err", prErr))
		reportFailures++
	}

	if prResult != nil {
		deleted, err := sweepPolicyReports(ctx, st, clusterID, namespaceIDsByName, prResult)
		if err != nil {
			slog.Warn("collector: sweep kyverno policy-reports failed",
				slog.String("cluster", clusterID.String()),
				slog.Any("err", err))
			metrics.ObserveError(clusterName, "policy_reports", "reconcile")
			reportFailures++
		} else {
			metrics.ObserveReconciled(clusterName, "policy_reports", deleted)
		}
		metrics.MarkPoll(clusterName, "policy_reports")
	}

	if policyFailures+reportFailures > 0 {
		return fmt.Errorf("%w (%d policy, %d report)", errKyvernoTickFailed, policyFailures, reportFailures)
	}
	return nil
}

type kyvernoSweepResult struct {
	clusterScoped []uuid.UUID
	byNamespace   map[uuid.UUID][]uuid.UUID
}

func newKyvernoSweepResult() *kyvernoSweepResult {
	return &kyvernoSweepResult{
		clusterScoped: make([]uuid.UUID, 0),
		byNamespace:   make(map[uuid.UUID][]uuid.UUID),
	}
}

func (r *kyvernoSweepResult) addClusterScoped(id uuid.UUID) {
	r.clusterScoped = append(r.clusterScoped, id)
}

func (r *kyvernoSweepResult) addNamespaced(nsID uuid.UUID, id uuid.UUID) {
	r.byNamespace[nsID] = append(r.byNamespace[nsID], id)
}

func sweepClusterPolicies(
	ctx context.Context,
	st KyvernoStore,
	clusterID uuid.UUID,
	namespaceIDsByName map[string]uuid.UUID,
	result *kyvernoSweepResult,
) (int64, error) {
	var total int64
	var sweepErrors int

	n, err := st.DeleteClusterScopedPoliciesNotIn(ctx, clusterID, result.clusterScoped)
	if err != nil {
		slog.Error("collector: sweep cluster-scoped policies failed",
			slog.String("cluster", clusterID.String()),
			slog.Any("error", err))
		sweepErrors++
	} else {
		total += n
	}

	for _, nsID := range namespaceIDsByName {
		keep := result.byNamespace[nsID]
		n, err := st.DeleteClusterPoliciesByNamespace(ctx, clusterID, nsID, keep)
		if err != nil {
			metrics.ObserveError("", "cluster_policies", "reconcile")
			slog.Error("collector: sweep cluster_policies by namespace failed",
				slog.Any("error", err), slog.String("namespace_id", nsID.String()), slog.String("cluster_name", ""))
			sweepErrors++
			continue
		}
		total += n
	}

	if sweepErrors > 0 {
		return total, fmt.Errorf("%d sweep errors", sweepErrors)
	}
	return total, nil
}

func sweepPolicyReports(
	ctx context.Context,
	st KyvernoStore,
	clusterID uuid.UUID,
	namespaceIDsByName map[string]uuid.UUID,
	result *kyvernoSweepResult,
) (int64, error) {
	var total int64
	var sweepErrors int

	n, err := st.DeleteClusterScopedPolicyReportsNotIn(ctx, clusterID, result.clusterScoped)
	if err != nil {
		slog.Error("collector: sweep cluster-scoped policy_reports failed",
			slog.String("cluster", clusterID.String()),
			slog.Any("error", err))
		sweepErrors++
	} else {
		total += n
	}

	for _, nsID := range namespaceIDsByName {
		keep := result.byNamespace[nsID]
		n, err := st.DeletePolicyReportsByNamespace(ctx, clusterID, nsID, keep)
		if err != nil {
			metrics.ObserveError("", "policy_reports", "reconcile")
			slog.Error("collector: sweep policy_reports by namespace failed",
				slog.Any("error", err), slog.String("namespace_id", nsID.String()), slog.String("cluster_name", ""))
			sweepErrors++
			continue
		}
		total += n
	}

	if sweepErrors > 0 {
		return total, fmt.Errorf("%d sweep errors", sweepErrors)
	}
	return total, nil
}

// collectClusterPolicies upserts all Kyverno ClusterPolicy + Policy rows
// and returns the sweep result (cluster-scoped IDs + IDs grouped by
// namespace) for per-namespace reconcile.
func collectClusterPolicies(
	ctx context.Context,
	src KubeSource,
	st KyvernoStore,
	clusterID uuid.UUID,
	clusterName string,
	namespaceIDsByName map[string]uuid.UUID,
) (*kyvernoSweepResult, error) {
	result := newKyvernoSweepResult()
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
		result.addClusterScoped(id)
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
		result.addNamespaced(nsID, id)
	}

	totalUpserted := len(result.clusterScoped)
	for _, ids := range result.byNamespace {
		totalUpserted += len(ids)
	}
	metrics.ObserveUpserts(clusterName, "cluster_policies", totalUpserted)
	if listErrors > 0 {
		return result, fmt.Errorf("%d cluster-policy upsert errors", listErrors)
	}
	return result, nil
}

// collectPolicyReports upserts all PolicyReport + ClusterPolicyReport rows
// and returns the sweep result (cluster-scoped IDs + IDs grouped by
// namespace) for per-namespace reconcile.
func collectPolicyReports(
	ctx context.Context,
	src KubeSource,
	st KyvernoStore,
	clusterID uuid.UUID,
	clusterName string,
	namespaceIDsByName map[string]uuid.UUID,
) (*kyvernoSweepResult, error) {
	result := newKyvernoSweepResult()
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
		result.addClusterScoped(id)
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
		result.addNamespaced(nsID, id)
	}

	totalUpserted := len(result.clusterScoped)
	for _, ids := range result.byNamespace {
		totalUpserted += len(ids)
	}
	metrics.ObserveUpserts(clusterName, "policy_reports", totalUpserted)
	if listErrors > 0 {
		return result, fmt.Errorf("%d policy-report upsert errors", listErrors)
	}
	return result, nil
}

// kyvernoPolicyToRow converts a KyvernoClusterPolicyInfo to an
// api.ClusterPolicyRow for upsert.
func kyvernoPolicyToRow(info *KyvernoClusterPolicyInfo, clusterID uuid.UUID, namespaceID *uuid.UUID) api.ClusterPolicyRow {
	now := time.Now().UTC()
	annotations := json.RawMessage(info.Annotations)
	specRaw := json.RawMessage(info.SpecRaw)
	if len(annotations) == 0 || !json.Valid(annotations) {
		annotations = nil
	}
	if len(specRaw) == 0 || !json.Valid(specRaw) {
		specRaw = nil
	}
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
		Annotations:     annotations,
		SpecRaw:         specRaw,
		Source:          api.SourceCollector,
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
	resultsRaw := json.RawMessage(info.ResultsRaw)
	if len(resultsRaw) == 0 || !json.Valid(resultsRaw) {
		resultsRaw = nil
	}
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
		ResultsRaw:      resultsRaw,
		Source:          api.SourceCollector,
		ReconcileSeenAt: time.Now().UTC(),
	}
}
