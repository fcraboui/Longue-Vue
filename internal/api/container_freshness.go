package api

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// ContainerVersionInfoFreshness* aliases bridge the oapi-codegen naming shift:
// when a second schema property named "freshness" exists, codegen emits short
// names (FarBehind/Outdated/UpToDate) instead of the fully-qualified prefixed
// form (ContainerVersionInfoFreshnessFarBehind/…). Hand-defining the prefixed
// constants here keeps the rest of the package compiling regardless of which
// codegen form is emitted.
const (
	ContainerVersionInfoFreshnessUpToDate  ContainerVersionInfoFreshness = "up_to_date"
	ContainerVersionInfoFreshnessOutdated  ContainerVersionInfoFreshness = "outdated"
	ContainerVersionInfoFreshnessFarBehind ContainerVersionInfoFreshness = "far_behind"
)

// ContainerVersionInfoFreshnessUnknown is a package-local freshness tier for
// deployed containers that carry no version enrichment (non-parseable tag,
// registry outside the allowlist, or not yet processed). It is intentionally
// NOT a member of the generated OpenAPI enum (which is
// [up_to_date, outdated, far_behind] — see ContainerVersionInfo.Freshness):
// the wire contract only ever omits the field for unenriched containers. This
// constant exists purely so the fleet-level freshness view can render a row
// for every deployed container rather than silently dropping unenriched ones.
const ContainerVersionInfoFreshnessUnknown ContainerVersionInfoFreshness = "unknown"

// ContainerFreshnessRow is one deployed-container entry, flattened from a
// Workload for tabular display or export. Every container with a non-empty
// name and image produces a row; unenriched containers carry Freshness=unknown.
type ContainerFreshnessRow struct {
	WorkloadName  string                        `json:"workload_name"`
	ClusterName   string                        `json:"cluster_name"`
	NamespaceName string                        `json:"namespace_name"`
	ContainerName string                        `json:"container_name"`
	Image         string                        `json:"image"`
	Freshness     ContainerVersionInfoFreshness `json:"freshness"`
	LatestTag     string                        `json:"latest_tag"`
}

// ContainerFreshnessSummary holds per-tier counts across the full (unfiltered)
// set of deployed containers for a given workload list.
type ContainerFreshnessSummary struct {
	Total     int `json:"total"`
	UpToDate  int `json:"up_to_date"`
	Outdated  int `json:"outdated"`
	FarBehind int `json:"far_behind"`
	Unknown   int `json:"unknown"`
}

// ContainerFreshnessFilter controls which rows SummarizeAndFilterContainerFreshness
// returns. Zero value applies no filtering. Summary counts always reflect the
// full unfiltered set regardless of which filters are set.
type ContainerFreshnessFilter struct {
	// Freshness, when non-nil, restricts rows to that freshness tier
	// (including the package-local "unknown" tier).
	Freshness *ContainerVersionInfoFreshness
	// Cluster, when non-empty, restricts rows to the named cluster (exact).
	Cluster string
	// ImageRepo, when non-nil, restricts rows to containers whose image
	// string case-insensitively contains the value (substring).
	ImageRepo *string
	// Namespace, when non-nil, restricts rows to the named namespace (exact).
	Namespace *string
	// Kind, when non-nil, restricts rows to workloads of that Kubernetes
	// controller kind (exact match against Workload.Kind).
	Kind *string
}

// SummarizeAndFilterContainerFreshness flattens the deployed containers from
// every Workload in ws into a []ContainerFreshnessRow and a
// ContainerFreshnessSummary. Every container with a non-empty name and image
// produces a row; containers lacking version enrichment are reported with the
// "unknown" freshness tier. Summary counts reflect the full unfiltered set so
// the caller can display total coverage regardless of which filter is active.
//
//nolint:gocognit,gocyclo // summarize-filter is inherently complex: per-container tally across workloads
func SummarizeAndFilterContainerFreshness(ws []Workload, f ContainerFreshnessFilter) ([]ContainerFreshnessRow, ContainerFreshnessSummary) {
	var rows []ContainerFreshnessRow
	var summary ContainerFreshnessSummary

	for i := range ws {
		w := &ws[i]
		if w.Containers == nil {
			continue
		}
		var versions map[string]ContainerVersionInfo
		if w.ContainersVersions != nil {
			versions = *w.ContainersVersions
		}
		clusterName := ""
		if w.ClusterName != nil {
			clusterName = *w.ClusterName
		}
		namespaceName := ""
		if w.NamespaceName != nil {
			namespaceName = *w.NamespaceName
		}
		kind := string(w.Kind)

		for _, c := range *w.Containers {
			name, _ := c["name"].(string)
			img, _ := c["image"].(string)
			if name == "" || img == "" {
				continue
			}

			// Resolve freshness tier: enriched value, or "unknown".
			freshness := ContainerVersionInfoFreshnessUnknown
			latestTag := ""
			if cv, ok := versions[name]; ok && cv.Freshness != nil {
				freshness = *cv.Freshness
				if cv.LatestTag != nil {
					latestTag = *cv.LatestTag
				}
			}

			// Tally into summary (always, before any filter).
			summary.Total++
			switch freshness {
			case ContainerVersionInfoFreshnessUpToDate:
				summary.UpToDate++
			case ContainerVersionInfoFreshnessOutdated:
				summary.Outdated++
			case ContainerVersionInfoFreshnessFarBehind:
				summary.FarBehind++
			default:
				summary.Unknown++
			}

			// Apply filters.
			if f.Freshness != nil && freshness != *f.Freshness {
				continue
			}
			if f.Cluster != "" && clusterName != f.Cluster {
				continue
			}
			if f.ImageRepo != nil && !strings.Contains(strings.ToLower(img), strings.ToLower(*f.ImageRepo)) {
				continue
			}
			if f.Namespace != nil && namespaceName != *f.Namespace {
				continue
			}
			if f.Kind != nil && kind != *f.Kind {
				continue
			}

			rows = append(rows, ContainerFreshnessRow{
				WorkloadName:  w.Name,
				ClusterName:   clusterName,
				NamespaceName: namespaceName,
				ContainerName: name,
				Image:         img,
				Freshness:     freshness,
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
func PageContainerFreshness(rows []ContainerFreshnessRow, limit int, cursor string) (page []ContainerFreshnessRow, next string) {
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

// buildContainerFreshnessPageSize is the per-page limit BuildContainerFreshness
// uses when walking ListWorkloads. Matches the extract path's page size.
const buildContainerFreshnessPageSize = 200

// BuildContainerFreshness pages the full workload fleet via ListWorkloads and
// returns each Workload with its Containers and ContainersVersions populated.
//
// Plain ListWorkloads does NOT fill ContainersVersions — that enrichment is
// opt-in (ADR-0022/0041). For each workload it calls
// EnrichContainersVersions(ctx, s, containers) and attaches the result. The
// Store interface satisfies EnrichContainersVersions' containerVersionLookup
// surface (GetImageOriginResolution + GetImageVersionsByRepo).
func BuildContainerFreshness(ctx context.Context, s Store) ([]Workload, error) {
	var out []Workload
	cursor := ""
	for {
		items, next, err := s.ListWorkloads(ctx, WorkloadListFilter{}, buildContainerFreshnessPageSize, cursor)
		if err != nil {
			return nil, fmt.Errorf("list workloads: %w", err)
		}
		for i := range items {
			var containers []map[string]any
			if items[i].Containers != nil {
				containers = *items[i].Containers
			}
			if cv := EnrichContainersVersions(ctx, s, containers); cv != nil {
				m := map[string]ContainerVersionInfo(cv)
				items[i].ContainersVersions = &m
			}
			out = append(out, items[i])
		}
		if next == "" {
			return out, nil
		}
		cursor = next
	}
}
