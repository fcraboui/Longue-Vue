package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// freshFixture builds a minimal Workload with enriched containers for testing.
func freshFixture(name, cluster, namespace string, containers ContainerList, versions map[string]ContainerVersionInfo) Workload {
	cv := map[string]ContainerVersionInfo(versions)
	return Workload{
		Name:               name,
		ClusterName:        &cluster,
		NamespaceName:      &namespace,
		Containers:         &containers,
		ContainersVersions: &cv,
	}
}

func freshnessPtr[T any](v T) *T { return &v }

func TestSummarizeAndFilterContainerFreshness(t *testing.T) {
	latest1 := "1.27.4"
	latest2 := "3.2.0"

	workloads := []Workload{
		freshFixture("api", "prod", "default",
			ContainerList{
				{"name": "web", "image": "nginx:1.25.3"},
				{"name": "sidecar", "image": "busybox:latest"}, // no tag -> not enriched
			},
			map[string]ContainerVersionInfo{
				"web": {
					LatestTag: &latest1,
					Freshness: freshnessPtr(ContainerVersionInfoFreshnessFarBehind),
				},
			},
		),
		freshFixture("worker", "prod", "jobs",
			ContainerList{
				{"name": "main", "image": "redis:3.0.0"},
			},
			map[string]ContainerVersionInfo{
				"main": {
					LatestTag: &latest2,
					Freshness: freshnessPtr(ContainerVersionInfoFreshnessOutdated),
				},
			},
		),
		freshFixture("cache", "staging", "default",
			ContainerList{
				{"name": "redis", "image": "redis:3.2.0"},
			},
			map[string]ContainerVersionInfo{
				"redis": {
					LatestTag: &latest2,
					Freshness: freshnessPtr(ContainerVersionInfoFreshnessUpToDate),
				},
			},
		),
	}

	t.Run("no filter returns a row per deployed container (incl. unknown)", func(t *testing.T) {
		// 4 containers total: web (far_behind), sidecar (unknown — no tag),
		// main (outdated), redis (up_to_date).
		rows, summary := SummarizeAndFilterContainerFreshness(workloads, ContainerFreshnessFilter{})
		if len(rows) != 4 {
			t.Fatalf("want 4 rows, got %d", len(rows))
		}
		if summary.Total != 4 {
			t.Errorf("summary.Total want 4, got %d", summary.Total)
		}
		if summary.FarBehind != 1 {
			t.Errorf("summary.FarBehind want 1, got %d", summary.FarBehind)
		}
		if summary.Outdated != 1 {
			t.Errorf("summary.Outdated want 1, got %d", summary.Outdated)
		}
		if summary.UpToDate != 1 {
			t.Errorf("summary.UpToDate want 1, got %d", summary.UpToDate)
		}
		if summary.Unknown != 1 {
			t.Errorf("summary.Unknown want 1, got %d", summary.Unknown)
		}
	})

	t.Run("unenriched container yields an unknown-tier row", func(t *testing.T) {
		f := ContainerVersionInfoFreshnessUnknown
		rows, _ := SummarizeAndFilterContainerFreshness(workloads, ContainerFreshnessFilter{Freshness: &f})
		if len(rows) != 1 {
			t.Fatalf("want 1 unknown row, got %d", len(rows))
		}
		if rows[0].ContainerName != "sidecar" {
			t.Errorf("want container sidecar, got %q", rows[0].ContainerName)
		}
		if rows[0].Freshness != ContainerVersionInfoFreshnessUnknown {
			t.Errorf("want unknown freshness, got %q", rows[0].Freshness)
		}
		if rows[0].LatestTag != "" {
			t.Errorf("want empty LatestTag for unknown, got %q", rows[0].LatestTag)
		}
	})

	t.Run("container with empty image is excluded", func(t *testing.T) {
		ws := []Workload{freshFixture("x", "c", "n",
			ContainerList{{"name": "noimage", "image": ""}},
			map[string]ContainerVersionInfo{},
		)}
		rows, summary := SummarizeAndFilterContainerFreshness(ws, ContainerFreshnessFilter{})
		if len(rows) != 0 {
			t.Fatalf("want 0 rows for empty image, got %d", len(rows))
		}
		if summary.Total != 0 {
			t.Errorf("summary.Total want 0, got %d", summary.Total)
		}
	})

	t.Run("filter by image repo (case-insensitive substring)", func(t *testing.T) {
		repo := "REDIS"
		rows, _ := SummarizeAndFilterContainerFreshness(workloads, ContainerFreshnessFilter{ImageRepo: &repo})
		if len(rows) != 2 {
			t.Fatalf("want 2 redis rows, got %d", len(rows))
		}
		for _, r := range rows {
			if r.ContainerName != "main" && r.ContainerName != "redis" {
				t.Errorf("unexpected container %q", r.ContainerName)
			}
		}
	})

	t.Run("filter by namespace (exact)", func(t *testing.T) {
		ns := "jobs"
		rows, _ := SummarizeAndFilterContainerFreshness(workloads, ContainerFreshnessFilter{Namespace: &ns})
		if len(rows) != 1 {
			t.Fatalf("want 1 jobs row, got %d", len(rows))
		}
		if rows[0].WorkloadName != "worker" {
			t.Errorf("want workload worker, got %q", rows[0].WorkloadName)
		}
	})

	t.Run("filter by kind (exact)", func(t *testing.T) {
		// All fixtures default to StatefulSet kind (zero WorkloadKind is "").
		// Build a fixture set with explicit kinds.
		dep := workloads[0]
		dep.Kind = Deployment
		sts := workloads[1]
		sts.Kind = StatefulSet
		ws := []Workload{dep, sts}
		k := "Deployment"
		rows, _ := SummarizeAndFilterContainerFreshness(ws, ContainerFreshnessFilter{Kind: &k})
		for _, r := range rows {
			if r.WorkloadName != "api" {
				t.Errorf("kind filter leaked: %q", r.WorkloadName)
			}
		}
		if len(rows) == 0 {
			t.Error("want at least 1 Deployment row")
		}
	})

	t.Run("filter by far_behind", func(t *testing.T) {
		f := ContainerVersionInfoFreshnessFarBehind
		rows, _ := SummarizeAndFilterContainerFreshness(workloads, ContainerFreshnessFilter{Freshness: &f})
		if len(rows) != 1 {
			t.Fatalf("want 1 far_behind row, got %d", len(rows))
		}
		if rows[0].ContainerName != "web" {
			t.Errorf("want container web, got %q", rows[0].ContainerName)
		}
	})

	t.Run("filter by cluster", func(t *testing.T) {
		rows, _ := SummarizeAndFilterContainerFreshness(workloads, ContainerFreshnessFilter{Cluster: "staging"})
		if len(rows) != 1 {
			t.Fatalf("want 1 staging row, got %d", len(rows))
		}
		if rows[0].WorkloadName != "cache" {
			t.Errorf("want workload cache, got %q", rows[0].WorkloadName)
		}
	})

	t.Run("summary counts all tiers regardless of filter", func(t *testing.T) {
		// Even when filtering by freshness, summary reflects the full workload set.
		f := ContainerVersionInfoFreshnessFarBehind
		_, summary := SummarizeAndFilterContainerFreshness(workloads, ContainerFreshnessFilter{Freshness: &f})
		if summary.Total != 4 {
			t.Errorf("summary.Total should be 4 (unfiltered), got %d", summary.Total)
		}
	})
}

func TestPageContainerFreshness(t *testing.T) {
	makeRows := func(n int) []ContainerFreshnessRow {
		rows := make([]ContainerFreshnessRow, n)
		for i := range rows {
			rows[i] = ContainerFreshnessRow{ContainerName: "c", WorkloadName: "w"}
		}
		return rows
	}

	t.Run("first page", func(t *testing.T) {
		rows := makeRows(5)
		page, next := PageContainerFreshness(rows, 2, "")
		if len(page) != 2 {
			t.Fatalf("want 2, got %d", len(page))
		}
		if next == "" {
			t.Error("want non-empty next cursor")
		}
	})

	t.Run("last page has no cursor", func(t *testing.T) {
		rows := makeRows(3)
		_, next1 := PageContainerFreshness(rows, 2, "")
		page, next2 := PageContainerFreshness(rows, 2, next1)
		if len(page) != 1 {
			t.Fatalf("want 1 item on last page, got %d", len(page))
		}
		if next2 != "" {
			t.Errorf("want empty cursor on last page, got %q", next2)
		}
	})

	t.Run("limit >= len returns all with no cursor", func(t *testing.T) {
		rows := makeRows(3)
		page, next := PageContainerFreshness(rows, 10, "")
		if len(page) != 3 {
			t.Fatalf("want 3, got %d", len(page))
		}
		if next != "" {
			t.Errorf("want empty cursor, got %q", next)
		}
	})

	t.Run("empty input", func(t *testing.T) {
		page, next := PageContainerFreshness(nil, 10, "")
		if len(page) != 0 {
			t.Errorf("want empty page, got %d", len(page))
		}
		if next != "" {
			t.Errorf("want empty cursor, got %q", next)
		}
	})
}

// TestBuildContainerFreshness drives the store-walking fleet builder against
// the in-memory memStore, verifying each returned Workload carries its
// ContainersVersions populated (which plain ListWorkloads does NOT do — the
// builder must enrich per-workload, as collectWorkloadEolRows does).
func TestBuildContainerFreshness(t *testing.T) {
	ms := newMemStore()
	ctx := context.Background()

	cluster, _, err := ms.EnsureCluster(ctx, ClusterCreate{Name: "fleet"})
	if err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	ns, err := ms.CreateNamespace(ctx, NamespaceCreate{ClusterId: *cluster.Id, Name: "apps"})
	if err != nil {
		t.Fatalf("seed namespace: %v", err)
	}
	containers := ContainerList{{"name": "web", "image": "nginx:1.25.3"}}
	if _, err := ms.CreateWorkload(ctx, WorkloadCreate{
		NamespaceId: *ns.Id,
		Kind:        Deployment,
		Name:        "web",
		Containers:  &containers,
	}); err != nil {
		t.Fatalf("seed workload: %v", err)
	}
	latest := tagNginxLatest
	if _, err := ms.UpsertImageVersion(ctx, ImageVersionUpsert{
		ImageRepo:     "docker.io/library/nginx",
		Variant:       "",
		Registry:      "docker.io",
		LatestTag:     &latest,
		Annotation:    json.RawMessage(`{}`),
		Source:        "registry",
		LastCheckedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert image version: %v", err)
	}

	ws, err := BuildContainerFreshness(ctx, ms)
	if err != nil {
		t.Fatalf("BuildContainerFreshness: %v", err)
	}
	if len(ws) != 1 {
		t.Fatalf("want 1 workload, got %d", len(ws))
	}
	if ws[0].ContainersVersions == nil {
		t.Fatal("ContainersVersions not populated by builder")
	}
	web, ok := (*ws[0].ContainersVersions)["web"]
	if !ok {
		t.Fatalf("expected 'web' enriched, got %v", *ws[0].ContainersVersions)
	}
	if web.Freshness == nil || *web.Freshness != ContainerVersionInfoFreshnessFarBehind {
		t.Errorf("web.Freshness: want far_behind, got %v", web.Freshness)
	}

	// End-to-end: rows flatten with the enriched freshness.
	rows, summary := SummarizeAndFilterContainerFreshness(ws, ContainerFreshnessFilter{})
	if len(rows) != 1 || summary.FarBehind != 1 {
		t.Errorf("rows=%d summary=%+v", len(rows), summary)
	}
}
