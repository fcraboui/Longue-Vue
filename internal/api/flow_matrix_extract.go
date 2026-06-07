package api

// Audited flow-matrix extracts (R3 Task 4) — bulk download of a cluster's
// read-time flow synthesis as CSV/JSON or a ZIP bundle. Mirrors the search /
// EOL extract plumbing: row-cap from LONGUE_VUE_EXTRACT_MAX_ROWS, the
// X-Longue-Vue-Truncated header written BEFORE any bytes, gated by
// flow_matrix_enabled (409 when off), and audited via the shouldAudit
// allowlist for SecNumCloud chapter-8 evidence.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/flowmatrix"
	"github.com/sthalbert/longue-vue/internal/metrics"
)

// flowMatrixCSVHeader is the fixed column set for the flow-matrix CSV /
// ZIP extracts. Kept as one helper so the handler and the ZIP bundler
// stay in lockstep.
func flowMatrixCSVHeader() []string {
	return []string{
		"cluster", "layer", "direction",
		"src_kind", "src_ref", "dst_kind", "dst_ref",
		"protocol", "ports", "state", "reference_id", "sources",
	}
}

// flowExtractRows concatenates the perimeter + internal flows into a single
// ordered slice for serialization.
func flowExtractRows(syn *flowmatrix.Synthesis) []flowmatrix.Flow {
	rows := make([]flowmatrix.Flow, 0, len(syn.Perimeter)+len(syn.Internal))
	rows = append(rows, syn.Perimeter...)
	rows = append(rows, syn.Internal...)
	return rows
}

// renderPorts renders a flow's port list as "tcp/443", "tcp/443-444", or
// "tcp/any" entries joined by ";". A nil From means "any port".
func renderPorts(ports []flowmatrix.PortRange) string {
	if len(ports) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ports))
	for i := range ports {
		parts = append(parts, renderPort(ports[i]))
	}
	return strings.Join(parts, ";")
}

func renderPort(p flowmatrix.PortRange) string {
	proto := p.Protocol
	if proto == "" {
		proto = driftAny
	}
	if p.From == nil {
		return proto + "/" + driftAny
	}
	if p.To != nil && *p.To != *p.From {
		return proto + "/" + strconv.Itoa(*p.From) + "-" + strconv.Itoa(*p.To)
	}
	return proto + "/" + strconv.Itoa(*p.From)
}

// renderSources renders a flow's contributing-rule summaries joined by "; ".
func renderSources(sources []flowmatrix.Source) string {
	if len(sources) == 0 {
		return ""
	}
	parts := make([]string, 0, len(sources))
	for i := range sources {
		parts = append(parts, sources[i].Summary)
	}
	return strings.Join(parts, "; ")
}

// flowRowToCSV flattens one synthesized flow into the CSV column order.
func flowRowToCSV(clusterID string, f *flowmatrix.Flow) []string {
	referenceID := ""
	if f.ReferenceID != nil {
		referenceID = *f.ReferenceID
	}
	return []string{
		clusterID,
		f.Layer,
		f.Direction,
		f.Src.Kind,
		f.Src.Name,
		f.Dst.Kind,
		f.Dst.Name,
		flowProtocol(f.Ports),
		renderPorts(f.Ports),
		string(f.State),
		referenceID,
		renderSources(f.Sources),
	}
}

// flowProtocol returns the protocol of the first port range, or "any" when
// the flow carries no ports.
func flowProtocol(ports []flowmatrix.PortRange) string {
	if len(ports) == 0 {
		return driftAny
	}
	if ports[0].Protocol == "" {
		return driftAny
	}
	return ports[0].Protocol
}

// loadFlowExtract is the shared gate-parse-load-synthesize path for both the
// flat and ZIP extract handlers. It returns the parsed cluster id, the
// synthesis, and a bool indicating whether the response has already been
// written (gate/parse/load error) — callers must return immediately when true.
func loadFlowExtract(
	w http.ResponseWriter,
	r *http.Request,
	store Store,
	page string,
	format string,
) (clusterID uuid.UUID, syn flowmatrix.Synthesis, handled bool) {
	settings, err := store.GetSettings(r.Context())
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "settings unavailable", err.Error())
		return uuid.Nil, flowmatrix.Synthesis{}, true
	}
	if !settings.FlowMatrixEnabled {
		writeProblem(w, http.StatusConflict, "flow matrix disabled",
			"enable flow_matrix_enabled in admin settings to use this endpoint")
		return uuid.Nil, flowmatrix.Synthesis{}, true
	}
	clusterID, err = uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid cluster id", err.Error())
		return uuid.Nil, flowmatrix.Synthesis{}, true
	}
	inputs, warnings, err := loadFlowInputs(r.Context(), store, clusterID)
	if err != nil {
		slog.Error("flow extract: load inputs", slog.Any("error", err))
		SetAuditDetails(r.Context(), map[string]any{
			"action": "extract", "page": page, "format": format, "outcome": "error",
		})
		metrics.ObserveExtract(page, format, "error", 0)
		writeProblem(w, http.StatusInternalServerError, "load flow inputs", err.Error())
		return uuid.Nil, flowmatrix.Synthesis{}, true
	}
	syn = flowmatrix.Synthesize(inputs)
	syn.Warnings = append(syn.Warnings, warnings...)
	return clusterID, syn, false
}

// HandleFlowMatrixExtract — read scope. GET
// /v1/clusters/{id}/flow-matrix/extract?format=csv|json. Gated by
// flow_matrix_enabled (409). Serializes the perimeter+internal flows as CSV
// (default) or a JSON envelope, capped at maxRows with the truncation header
// set before any bytes are written. Audited via the shouldAudit allowlist.
func HandleFlowMatrixExtract(store Store, maxRows int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		format := r.URL.Query().Get("format")
		if format == "" {
			format = extractFormatCSV
		}
		if format != extractFormatCSV && format != extractFormatJSON {
			SetAuditDetails(r.Context(), map[string]any{
				"action": "extract", "page": "flow_matrix", "format": format, "outcome": "denied",
			})
			metrics.ObserveExtract("flow_matrix", format, "denied", 0)
			writeProblem(w, http.StatusBadRequest, "Bad Request", "format must be 'csv' or 'json'")
			return
		}

		clusterID, syn, handled := loadFlowExtract(w, r, store, "flow_matrix", format)
		if handled {
			return
		}

		rows := flowExtractRows(&syn)
		truncated := false
		if len(rows) > maxRows {
			rows = rows[:maxRows]
			truncated = true
		}

		clusterStr := clusterID.String()
		var buf bytes.Buffer
		if err := renderFlowExtract(&buf, format, clusterStr, rows, truncated); err != nil {
			slog.Error("flow extract: render", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}

		outcome := "ok"
		if truncated {
			outcome = extractOutcomeTruncated
			w.Header().Set("X-Longue-Vue-Truncated", "true")
		}
		filename := fmt.Sprintf("longue-vue-flow-matrix-%s-%s.%s", clusterStr, extractTimestamp(time.Now()), format)
		w.Header().Set("Content-Type", extractContentType(format))
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf.Bytes())

		SetAuditDetails(r.Context(), map[string]any{
			"action":     "extract",
			"page":       "flow_matrix",
			"format":     format,
			"cluster_id": clusterStr,
			"row_count":  len(rows),
			"truncated":  truncated,
			"outcome":    outcome,
		})
		metrics.ObserveExtract("flow_matrix", format, outcome, len(rows))
	}
}

// renderFlowExtract writes the flow rows into buf in the requested format.
// CSV uses the shared extract CSV writer (BOM + CRLF + eager header). JSON
// emits the envelope {cluster_id, total_count, truncated, items:[...]}.
func renderFlowExtract(
	buf *bytes.Buffer,
	format string,
	clusterID string,
	rows []flowmatrix.Flow,
	truncated bool,
) error {
	switch format {
	case extractFormatJSON:
		env := map[string]any{
			"cluster_id":  clusterID,
			"total_count": len(rows),
			"truncated":   truncated,
			"items":       rows,
		}
		if err := json.NewEncoder(buf).Encode(env); err != nil {
			return fmt.Errorf("encode flow extract json: %w", err)
		}
		return nil
	default:
		cw := newExtractCSVWriter(buf, flowMatrixCSVHeader())
		for i := range rows {
			if err := cw.WriteRow(flowRowToCSV(clusterID, &rows[i])); err != nil {
				return err
			}
		}
		return cw.Close()
	}
}

// flowSourceRef is one deduped {kind,id} entry for the ZIP's _sources.json.
type flowSourceRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// dedupeFlowSources collects the distinct {kind,id} pairs across every flow's
// Sources, preserving first-seen order for a stable manifest.
func dedupeFlowSources(rows []flowmatrix.Flow) []flowSourceRef {
	seen := make(map[string]struct{})
	out := make([]flowSourceRef, 0)
	for i := range rows {
		for _, s := range rows[i].Sources {
			key := s.Kind + "|" + s.ID
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, flowSourceRef{Kind: s.Kind, ID: s.ID})
		}
	}
	return out
}

// HandleFlowMatrixExtractZip — read scope. GET
// /v1/clusters/{id}/flow-matrix/extract.zip. Bundles flow-matrix.csv (same
// rows/columns as the flat CSV extract) plus _sources.json (deduped
// {kind,id} from every Flow.Sources). Gated by flow_matrix_enabled (409),
// capped at maxRows with the truncation header set before any bytes, and
// audited via the shouldAudit allowlist.
func HandleFlowMatrixExtractZip(store Store, maxRows int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clusterID, syn, handled := loadFlowExtract(w, r, store, "flow_matrix", "zip")
		if handled {
			return
		}

		rows := flowExtractRows(&syn)
		truncated := false
		if len(rows) > maxRows {
			rows = rows[:maxRows]
			truncated = true
		}

		clusterStr := clusterID.String()
		generated := time.Now()
		var buf bytes.Buffer
		if err := writeFlowZip(&buf, clusterStr, rows, generated); err != nil {
			slog.Error("flow extract: zip", slog.Any("error", err))
			SetAuditDetails(r.Context(), map[string]any{
				"action": "extract", "page": "flow_matrix", "format": "zip", "outcome": "error",
			})
			metrics.ObserveExtract("flow_matrix", "zip", "error", 0)
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}

		outcome := "ok"
		if truncated {
			outcome = extractOutcomeTruncated
			w.Header().Set("X-Longue-Vue-Truncated", "true")
		}
		filename := fmt.Sprintf("longue-vue-flow-matrix-%s-%s.zip", clusterStr, extractTimestamp(generated))
		w.Header().Set("Content-Type", extractContentType("zip"))
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf.Bytes())

		SetAuditDetails(r.Context(), map[string]any{
			"action":     "extract",
			"page":       "flow_matrix",
			"format":     "zip",
			"cluster_id": clusterStr,
			"row_count":  len(rows),
			"truncated":  truncated,
			"outcome":    outcome,
		})
		metrics.ObserveExtract("flow_matrix", "zip", outcome, len(rows))
	}
}

// writeFlowZip bundles flow-matrix.csv + _sources.json into a ZIP. It reuses
// the shared extractZIPWriter for the CSV (and its README) and appends the
// sources manifest as a raw JSON entry.
func writeFlowZip(buf *bytes.Buffer, clusterID string, rows []flowmatrix.Flow, generated time.Time) error {
	zw := newExtractZIPWriter(buf, extractZIPMeta{
		Query: "flow-matrix:" + clusterID, GeneratedAt: generated, Version: extractServerVersion,
	})

	cw, err := zw.AddCSV("flow-matrix.csv", flowMatrixCSVHeader())
	if err != nil {
		return err
	}
	for i := range rows {
		if err := cw.WriteRow(flowRowToCSV(clusterID, &rows[i])); err != nil {
			return err
		}
	}
	if err := cw.Close(); err != nil {
		return err
	}
	zw.SetRowCount("flow-matrix.csv", len(rows))

	sources := dedupeFlowSources(rows)
	sw, err := zw.AddJSON("_sources.json")
	if err != nil {
		return err
	}
	if err := json.NewEncoder(sw).Encode(sources); err != nil {
		return fmt.Errorf("encode _sources.json: %w", err)
	}
	zw.SetRowCount("_sources.json", len(sources))

	return zw.Close()
}
