package api

import (
	"testing"
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

	t.Run("no filter returns all enriched containers", func(t *testing.T) {
		rows, summary := SummarizeAndFilterContainerFreshness(workloads, ContainerFreshnessFilter{})
		if len(rows) != 3 {
			t.Fatalf("want 3 rows, got %d", len(rows))
		}
		if summary.Total != 3 {
			t.Errorf("summary.Total want 3, got %d", summary.Total)
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

	t.Run("unenriched containers are excluded", func(t *testing.T) {
		// busybox:latest has no enrichment -> should not appear
		rows, _ := SummarizeAndFilterContainerFreshness(workloads, ContainerFreshnessFilter{})
		for _, r := range rows {
			if r.ContainerName == "sidecar" {
				t.Errorf("sidecar (unenriched) should not appear in rows")
			}
		}
	})

	t.Run("summary counts all tiers regardless of filter", func(t *testing.T) {
		// Even when filtering by freshness, summary reflects the full workload set.
		f := ContainerVersionInfoFreshnessFarBehind
		_, summary := SummarizeAndFilterContainerFreshness(workloads, ContainerFreshnessFilter{Freshness: &f})
		if summary.Total != 3 {
			t.Errorf("summary.Total should be 3 (unfiltered), got %d", summary.Total)
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
