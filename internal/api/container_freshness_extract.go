package api

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/sthalbert/longue-vue/internal/auth"
	"github.com/sthalbert/longue-vue/internal/metrics"
)

// HandleContainerFreshnessExtract — read scope.
// GET /v1/container-freshness/extract?format=csv|json
//
// Walks the full workload fleet via BuildContainerFreshness, flattens every
// deployed container into a ContainerFreshnessRow and emits CSV or JSON.
// The response body is buffered before flushing so the
// X-Longue-Vue-Truncated header can be written before any bytes hit the
// wire (mirrors HandleEolExtract).
func HandleContainerFreshnessExtract(s Store, maxRows int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeRead) {
			return
		}
		q := r.URL.Query()
		format := q.Get("format")
		if format != extractFormatCSV && format != extractFormatJSON {
			SetAuditDetails(r.Context(), map[string]any{
				"action": "extract", "page": "container_freshness", "format": format, "outcome": "denied",
			})
			metrics.ObserveExtract("container_freshness", format, "denied", 0)
			writeProblem(w, http.StatusBadRequest, "Bad Request", "format must be 'csv' or 'json'")
			return
		}

		workloads, err := BuildContainerFreshness(r.Context(), s)
		if err != nil {
			slog.Error("extract: build container freshness", slog.Any("error", err))
			SetAuditDetails(r.Context(), map[string]any{
				"action": "extract", "page": "container_freshness", "format": format, "outcome": "error",
			})
			metrics.ObserveExtract("container_freshness", format, "error", 0)
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}

		rows, _ := SummarizeAndFilterContainerFreshness(workloads, ContainerFreshnessFilter{})

		truncated := false
		if len(rows) > maxRows {
			rows = rows[:maxRows]
			truncated = true
		}

		var buf bytes.Buffer
		switch format {
		case extractFormatCSV:
			cw := newExtractCSVWriter(&buf, containerFreshnessCSVHeader())
			for i := range rows {
				if err := cw.WriteRow(containerFreshnessRowToCSV(rows[i])); err != nil {
					slog.Error("extract: container freshness csv row", slog.Any("error", err))
					writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
					return
				}
			}
			if err := cw.Close(); err != nil {
				slog.Error("extract: container freshness csv close", slog.Any("error", err))
				writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
				return
			}
		case extractFormatJSON:
			jw := newExtractJSONWriter(&buf)
			for i := range rows {
				if err := jw.WriteRow(rows[i]); err != nil {
					slog.Error("extract: container freshness json row", slog.Any("error", err))
					writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
					return
				}
			}
			if err := jw.Close(); err != nil {
				slog.Error("extract: container freshness json close", slog.Any("error", err))
				writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
				return
			}
		}

		outcome := "ok"
		if truncated {
			outcome = extractOutcomeTruncated
			w.Header().Set("X-Longue-Vue-Truncated", "true")
		}
		filename := fmt.Sprintf("longue-vue-container-freshness-%s.%s", extractTimestamp(time.Now()), format)
		w.Header().Set("Content-Type", extractContentType(format))
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf.Bytes())

		SetAuditDetails(r.Context(), map[string]any{
			"action":    "extract",
			"page":      "container_freshness",
			"format":    format,
			"row_count": len(rows),
			"truncated": truncated,
			"outcome":   outcome,
		})
		metrics.ObserveExtract("container_freshness", format, outcome, len(rows))
	}
}

func containerFreshnessCSVHeader() []string {
	return []string{
		"cluster_name", "namespace_name", "workload_name",
		"container_name", "image", "latest_tag", "freshness",
	}
}

func containerFreshnessRowToCSV(r ContainerFreshnessRow) []string {
	return []string{
		r.ClusterName, r.NamespaceName, r.WorkloadName,
		r.ContainerName, r.Image, r.LatestTag, string(r.Freshness),
	}
}
