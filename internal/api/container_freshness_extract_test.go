//nolint:noctx // httptest.NewRequest carries no request context in these unit tests
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sthalbert/longue-vue/internal/auth"
)

func newFreshnessExtractRequest(format string) *http.Request {
	url := "/v1/container-freshness/extract"
	if format != "" {
		url += "?format=" + format
	}
	req := httptest.NewRequest(http.MethodGet, url, http.NoBody)
	return req.WithContext(auth.WithCaller(req.Context(), readCaller()))
}

func TestHandleContainerFreshnessExtract_CSV(t *testing.T) {
	ms := newMemStore()
	seedFreshnessStore(t, ms)

	req := newFreshnessExtractRequest("csv")
	rr := httptest.NewRecorder()
	HandleContainerFreshnessExtract(ms, 50000).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type: want text/csv, got %q", ct)
	}
	body := rr.Body.String()
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected header + ≥1 data row, got %d lines:\n%s", len(lines), body)
	}
	// Verify header columns.
	header := lines[0]
	for _, col := range []string{csvColClusterName, "namespace_name", csvColWorkloadName, "container_name", csvColImage, "latest_tag", "freshness"} {
		if !strings.Contains(header, col) {
			t.Errorf("header missing column %q: %s", col, header)
		}
	}
}

func TestHandleContainerFreshnessExtract_JSON(t *testing.T) {
	ms := newMemStore()
	seedFreshnessStore(t, ms)

	req := newFreshnessExtractRequest("json")
	rr := httptest.NewRecorder()
	HandleContainerFreshnessExtract(ms, 50000).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type: want application/json, got %q", ct)
	}
	var rows []ContainerFreshnessRow
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if len(rows) < 1 {
		t.Fatalf("expected ≥1 row, got 0")
	}
}

func TestHandleContainerFreshnessExtract_MissingFormat(t *testing.T) {
	ms := newMemStore()

	req := newFreshnessExtractRequest("")
	rr := httptest.NewRecorder()
	HandleContainerFreshnessExtract(ms, 50000).ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d; want 400", rr.Code)
	}
}

func TestHandleContainerFreshnessExtract_InvalidFormat(t *testing.T) {
	ms := newMemStore()

	req := newFreshnessExtractRequest("xlsx")
	rr := httptest.NewRecorder()
	HandleContainerFreshnessExtract(ms, 50000).ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d; want 400", rr.Code)
	}
}

func TestHandleContainerFreshnessExtract_Truncation(t *testing.T) {
	ms := newMemStore()
	seedFreshnessStore(t, ms) // seeds 1 enriched container row

	// maxRows=0 → the 1 seeded row is truncated.
	req := newFreshnessExtractRequest("csv")
	rr := httptest.NewRecorder()
	HandleContainerFreshnessExtract(ms, 0).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-Longue-Vue-Truncated") != headerValueTrue {
		t.Errorf("expected X-Longue-Vue-Truncated: true")
	}
}

func TestHandleContainerFreshnessExtract_NoScope(t *testing.T) {
	ms := newMemStore()

	// No caller in context → requireScope returns 403.
	req := httptest.NewRequest(http.MethodGet, "/v1/container-freshness/extract?format=csv", http.NoBody)
	rr := httptest.NewRecorder()
	HandleContainerFreshnessExtract(ms, 50000).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status %d; want 403", rr.Code)
	}
}
