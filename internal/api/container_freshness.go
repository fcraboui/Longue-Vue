package api

import (
	"strconv"
)

// ContainerFreshnessRow is one enriched-container entry, flattened from a
// Workload for tabular display or export. Only containers that have a
// populated Freshness field contribute a row.
type ContainerFreshnessRow struct {
	WorkloadName  string
	ClusterName   string
	NamespaceName string
	ContainerName string
	Image         string
	Freshness     ContainerVersionInfoFreshness
	LatestTag     string
}

// ContainerFreshnessSummary holds per-tier counts across the full (unfiltered)
// set of enriched containers for a given workload list.
type ContainerFreshnessSummary struct {
	Total     int
	UpToDate  int
	Outdated  int
	FarBehind int
}

// ContainerFreshnessFilter controls which rows SummarizeAndFilterContainerFreshness
// returns. Zero value applies no filtering.
type ContainerFreshnessFilter struct {
	// Freshness, when non-nil, restricts rows to that freshness tier.
	Freshness *ContainerVersionInfoFreshness
	// Cluster, when non-empty, restricts rows to the named cluster.
	Cluster string
}

// SummarizeAndFilterContainerFreshness flattens the enriched containers from
// every Workload in ws into a []ContainerFreshnessRow and a
// ContainerFreshnessSummary. Summary counts reflect the full unfiltered set so
// the caller can display total coverage regardless of which filter is active.
func SummarizeAndFilterContainerFreshness(ws []Workload, f ContainerFreshnessFilter) ([]ContainerFreshnessRow, ContainerFreshnessSummary) {
	var rows []ContainerFreshnessRow
	var summary ContainerFreshnessSummary

	for i := range ws {
		w := &ws[i]
		if w.Containers == nil || w.ContainersVersions == nil {
			continue
		}
		versions := map[string]ContainerVersionInfo(*w.ContainersVersions)
		clusterName := ""
		if w.ClusterName != nil {
			clusterName = *w.ClusterName
		}
		namespaceName := ""
		if w.NamespaceName != nil {
			namespaceName = *w.NamespaceName
		}

		for _, c := range *w.Containers {
			name, _ := c["name"].(string)
			img, _ := c["image"].(string)
			if name == "" {
				continue
			}
			cv, ok := versions[name]
			if !ok || cv.Freshness == nil {
				continue
			}
			// Tally into summary (always, before filter).
			summary.Total++
			switch *cv.Freshness {
			case ContainerVersionInfoFreshnessUpToDate:
				summary.UpToDate++
			case ContainerVersionInfoFreshnessOutdated:
				summary.Outdated++
			case ContainerVersionInfoFreshnessFarBehind:
				summary.FarBehind++
			}

			// Apply filter.
			if f.Freshness != nil && *cv.Freshness != *f.Freshness {
				continue
			}
			if f.Cluster != "" && clusterName != f.Cluster {
				continue
			}

			latestTag := ""
			if cv.LatestTag != nil {
				latestTag = *cv.LatestTag
			}
			rows = append(rows, ContainerFreshnessRow{
				WorkloadName:  w.Name,
				ClusterName:   clusterName,
				NamespaceName: namespaceName,
				ContainerName: name,
				Image:         img,
				Freshness:     *cv.Freshness,
				LatestTag:     latestTag,
			})
		}
	}
	return rows, summary
}

// PageContainerFreshness returns a window of rows starting at cursor (empty =
// first page) with at most limit items, plus the cursor for the next page
// (empty when there are no more items). The cursor is an opaque string
// encoding the next-start index.
func PageContainerFreshness(rows []ContainerFreshnessRow, limit int, cursor string) ([]ContainerFreshnessRow, string) {
	start := 0
	if cursor != "" {
		n, err := strconv.Atoi(cursor)
		if err == nil && n > 0 {
			start = n
		}
	}
	if start >= len(rows) {
		return nil, ""
	}
	end := start + limit
	if end >= len(rows) {
		return rows[start:], ""
	}
	return rows[start:end], strconv.Itoa(end)
}
