package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

func seedClusterForKyverno(t *testing.T, pg *PG) (clusterID uuid.UUID, nsID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "kyv-" + uuid.New().String()[:8]})
	if err != nil {
		t.Fatalf("ensure cluster: %v", err)
	}
	ns, _, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{ClusterId: *cluster.Id, Name: "default"})
	if err != nil {
		t.Fatalf("upsert namespace: %v", err)
	}
	return *cluster.Id, *ns.Id
}

func makeCP(clusterID, nsID uuid.UUID, name string) api.ClusterPolicyRow {
	bg := true
	rdy := true
	rc := 3
	sev := "high"
	act := "enforce"
	fp := "Fail"
	cat := "Best Practices"
	desc := "test policy"
	return api.ClusterPolicyRow{
		ClusterID:     clusterID,
		NamespaceID:   &nsID,
		Name:          name,
		ResourceType:  "ClusterPolicy",
		Scope:         "cluster",
		Description:   &desc,
		Category:      &cat,
		Severity:      &sev,
		Action:        &act,
		FailurePolicy: &fp,
		Background:    &bg,
		RuleTypes:     []string{"validate"},
		RulesCount:    &rc,
		Ready:         &rdy,
		Annotations:   json.RawMessage(`{}`),
		SpecRaw:       json.RawMessage(`{}`),
	}
}

func makePR(clusterID, nsID uuid.UUID, name string) api.PolicyReportRow {
	sk := "Namespace"
	sn := "default"
	return api.PolicyReportRow{
		ClusterID:   clusterID,
		NamespaceID: &nsID,
		Name:        name,
		ScopeKind:   &sk,
		ScopeName:   &sn,
		SummaryPass: 5,
		SummaryFail: 2,
		SummaryWarn: 1,
		ResultsRaw:  json.RawMessage(`[]`),
	}
}

func TestKyverno_UpsertClusterPolicy_Idempotent(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	cid, nsID := seedClusterForKyverno(t, pg)

	cp := makeCP(cid, nsID, "require-labels")
	id1, err := pg.UpsertClusterPolicy(ctx, cp)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	cp.Description = ptrStr("updated")
	id2, err := pg.UpsertClusterPolicy(ctx, cp)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("upsert should keep ID stable: %s vs %s", id1, id2)
	}

	got, err := pg.GetClusterPolicy(ctx, id1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Description == nil || *got.Description != "updated" {
		t.Fatalf("description not updated: %+v", got.Description)
	}
}

func TestKyverno_GetClusterPolicy_NotFound(t *testing.T) {
	pg := newTestPG(t)
	_, err := pg.GetClusterPolicy(context.Background(), uuid.New())
	if !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestKyverno_ListClusterPolicies_Filters(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	cid, nsID := seedClusterForKyverno(t, pg)

	for _, name := range []string{"alpha", "beta", "gamma"} {
		cp := makeCP(cid, nsID, name)
		if name == "beta" {
			sev := "low"
			cp.Severity = &sev
			act := "audit"
			cp.Action = &act
		}
		if _, err := pg.UpsertClusterPolicy(ctx, cp); err != nil {
			t.Fatalf("upsert %s: %v", name, err)
		}
	}

	items, _, err := pg.ListClusterPolicies(ctx, api.ClusterPolicyListFilter{ClusterID: &cid}, api.ListPage{Limit: 50})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("want 3, got %d", len(items))
	}

	sev := "high"
	high, _, err := pg.ListClusterPolicies(ctx, api.ClusterPolicyListFilter{Severity: &sev}, api.ListPage{Limit: 50})
	if err != nil {
		t.Fatalf("filter severity: %v", err)
	}
	if len(high) != 2 {
		t.Fatalf("want 2 high-severity, got %d", len(high))
	}

	sevLower := "HIGH"
	high2, _, err := pg.ListClusterPolicies(ctx, api.ClusterPolicyListFilter{Severity: &sevLower}, api.ListPage{Limit: 50})
	if err != nil {
		t.Fatalf("filter severity case-insensitive: %v", err)
	}
	if len(high2) != 2 {
		t.Fatalf("want 2 with UPPER severity filter, got %d", len(high2))
	}

	act := "audit"
	audited, _, err := pg.ListClusterPolicies(ctx, api.ClusterPolicyListFilter{Action: &act}, api.ListPage{Limit: 50})
	if err != nil {
		t.Fatalf("filter action: %v", err)
	}
	if len(audited) != 1 {
		t.Fatalf("want 1 audit action, got %d", len(audited))
	}

	fp := "fail"
	fpItems, _, err := pg.ListClusterPolicies(ctx, api.ClusterPolicyListFilter{FailurePolicy: &fp}, api.ListPage{Limit: 50})
	if err != nil {
		t.Fatalf("filter failure_policy: %v", err)
	}
	if len(fpItems) != 3 {
		t.Fatalf("want 3 with failure_policy=fail (case-insensitive), got %d", len(fpItems))
	}
}

func TestKyverno_SweepClusterPolicies_DeletesUnseen(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	cid, nsID := seedClusterForKyverno(t, pg)

	kept, err := pg.UpsertClusterPolicy(ctx, makeCP(cid, nsID, "kept"))
	if err != nil {
		t.Fatalf("upsert kept: %v", err)
	}
	_, err = pg.UpsertClusterPolicy(ctx, makeCP(cid, nsID, "gone"))
	if err != nil {
		t.Fatalf("upsert gone: %v", err)
	}

	deleted, err := pg.DeleteClusterPoliciesNotIn(ctx, cid, []uuid.UUID{kept})
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("want 1 deleted, got %d", deleted)
	}

	items, _, err := pg.ListClusterPolicies(ctx, api.ClusterPolicyListFilter{ClusterID: &cid}, api.ListPage{Limit: 50})
	if err != nil {
		t.Fatalf("list after sweep: %v", err)
	}
	if len(items) != 1 || items[0].Name != "kept" {
		t.Fatalf("want only 'kept', got %+v", items)
	}
}

func TestKyverno_ListClusterPolicies_Pagination(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	cid, nsID := seedClusterForKyverno(t, pg)

	for i := range 5 {
		if _, err := pg.UpsertClusterPolicy(ctx, makeCP(cid, nsID, fmt.Sprintf("pol-%02d", i))); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}

	seen := make(map[uuid.UUID]bool)
	var cursor string
	total := 0

	for {
		items, next, err := pg.ListClusterPolicies(ctx,
			api.ClusterPolicyListFilter{ClusterID: &cid},
			api.ListPage{Limit: 2, Cursor: cursor, Sort: "name", Order: "asc"},
		)
		if err != nil {
			t.Fatalf("list page: %v", err)
		}
		for _, it := range items {
			if seen[it.ID] {
				t.Fatalf("duplicate %s", it.ID)
			}
			seen[it.ID] = true
		}
		total += len(items)
		if next == "" {
			break
		}
		cursor = next
	}
	if total != 5 {
		t.Fatalf("want 5 total, got %d", total)
	}
}

func TestKyverno_UpsertPolicyReport_Idempotent(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	cid, nsID := seedClusterForKyverno(t, pg)

	pr := makePR(cid, nsID, "cpr-cluster")
	id1, err := pg.UpsertPolicyReport(ctx, pr)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	pr.SummaryFail = 10
	id2, err := pg.UpsertPolicyReport(ctx, pr)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("upsert should keep ID stable: %s vs %s", id1, id2)
	}

	got, err := pg.GetPolicyReport(ctx, id1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SummaryFail != 10 {
		t.Fatalf("summary_fail not updated: %d", got.SummaryFail)
	}
}

func TestKyverno_GetPolicyReport_NotFound(t *testing.T) {
	pg := newTestPG(t)
	_, err := pg.GetPolicyReport(context.Background(), uuid.New())
	if !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestKyverno_ListPolicyReports_ScopeFilters(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	cid, nsID := seedClusterForKyverno(t, pg)

	pr := makePR(cid, nsID, "cpr-cluster")
	if _, err := pg.UpsertPolicyReport(ctx, pr); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	sk := "Namespace"
	items, _, err := pg.ListPolicyReports(ctx,
		api.PolicyReportListFilter{ScopeKind: &sk},
		api.ListPage{Limit: 50},
	)
	if err != nil {
		t.Fatalf("filter scope_kind: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1, got %d", len(items))
	}

	sn := "cluster"
	items2, _, err := pg.ListPolicyReports(ctx,
		api.PolicyReportListFilter{ScopeName: &sn},
		api.ListPage{Limit: 50},
	)
	if err != nil {
		t.Fatalf("filter scope_name: %v", err)
	}
	if len(items2) != 1 {
		t.Fatalf("want 1, got %d", len(items2))
	}
}

func TestKyverno_SweepPolicyReports_DeletesUnseen(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	cid, nsID := seedClusterForKyverno(t, pg)

	kept, err := pg.UpsertPolicyReport(ctx, makePR(cid, nsID, "kept"))
	if err != nil {
		t.Fatalf("upsert kept: %v", err)
	}
	_, err = pg.UpsertPolicyReport(ctx, makePR(cid, nsID, "gone"))
	if err != nil {
		t.Fatalf("upsert gone: %v", err)
	}

	deleted, err := pg.DeletePolicyReportsNotIn(ctx, cid, []uuid.UUID{kept})
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("want 1 deleted, got %d", deleted)
	}

	items, _, err := pg.ListPolicyReports(ctx,
		api.PolicyReportListFilter{ClusterID: &cid},
		api.ListPage{Limit: 50},
	)
	if err != nil {
		t.Fatalf("list after sweep: %v", err)
	}
	if len(items) != 1 || items[0].Name != "kept" {
		t.Fatalf("want only 'kept', got %+v", items)
	}
}

func ptrStr(s string) *string { return &s }

func TestKyverno_ListClusterPolicies_SeverityPagination_NullSeverity(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	cid, nsID := seedClusterForKyverno(t, pg)

	withSev := makeCP(cid, nsID, "has-severity")
	withoutSev := makeCP(cid, nsID, "no-severity")
	withoutSev.Severity = nil
	withoutSev2 := makeCP(cid, nsID, "also-no-severity")
	withoutSev2.Severity = nil

	for _, cp := range []api.ClusterPolicyRow{withSev, withoutSev, withoutSev2} {
		if _, err := pg.UpsertClusterPolicy(ctx, cp); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	seen := make(map[uuid.UUID]bool)
	var cursor string
	total := 0

	for {
		items, next, err := pg.ListClusterPolicies(ctx,
			api.ClusterPolicyListFilter{ClusterID: &cid},
			api.ListPage{Limit: 2, Cursor: cursor, Sort: "severity", Order: "asc"},
		)
		if err != nil {
			t.Fatalf("list page: %v", err)
		}
		for _, it := range items {
			if seen[it.ID] {
				t.Fatalf("duplicate %s", it.ID)
			}
			seen[it.ID] = true
		}
		total += len(items)
		if next == "" {
			break
		}
		cursor = next
	}

	if total != 3 {
		t.Fatalf("want 3 total across pages, got %d", total)
	}
}
