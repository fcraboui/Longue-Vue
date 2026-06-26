package api

// Hand-written HTTP handler for GET /v1/container-freshness.
// Returns a paginated list of deployed-container freshness rows with summary
// counts. Backed by BuildContainerFreshness + SummarizeAndFilterContainerFreshness.

import (
	"log/slog"
	"net/http"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// HandleListContainerFreshness handles GET /v1/container-freshness.
// Query params: image, freshness, cluster, namespace, kind, limit, cursor.
func HandleListContainerFreshness(s Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeRead) {
			return
		}

		q := r.URL.Query()
		var filter ContainerFreshnessFilter

		// image filter
		if img := q.Get("image"); img != "" {
			if len(img) > 200 {
				writeProblem(w, http.StatusBadRequest, "Bad Request", "image too long")
				return
			}
			filter.ImageRepo = &img
		}

		// freshness filter
		if fresh := q.Get("freshness"); fresh != "" {
			switch fresh {
			case string(ContainerVersionInfoFreshnessUpToDate),
				string(ContainerVersionInfoFreshnessOutdated),
				string(ContainerVersionInfoFreshnessFarBehind),
				string(ContainerVersionInfoFreshnessUnknown):
				f := ContainerVersionInfoFreshness(fresh)
				filter.Freshness = &f
			default:
				writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid freshness")
				return
			}
		}

		// cluster filter
		if cluster := q.Get("cluster"); cluster != "" {
			filter.Cluster = cluster
		}

		// namespace filter
		if ns := q.Get("namespace"); ns != "" {
			filter.Namespace = &ns
		}

		// kind filter
		if kind := q.Get("kind"); kind != "" {
			filter.Kind = &kind
		}

		limit := parseLimit(q.Get("limit"), 50)

		workloads, err := BuildContainerFreshness(r.Context(), s)
		if err != nil {
			slog.Error("HandleListContainerFreshness: BuildContainerFreshness failed", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "failed to build freshness data")
			return
		}

		rows, summary := SummarizeAndFilterContainerFreshness(workloads, filter)
		page, next := PageContainerFreshness(rows, limit, q.Get("cursor"))

		writeJSON(w, http.StatusOK, map[string]any{
			"items":       page,
			"next_cursor": next,
			"summary":     summary,
		})
	}
}
