package api

// Handler-level tests for the cluster reference flow matrix CRUD + YAML
// import/export surface (ADR-0036, R2 Task 8).
//
// Fake-store strategy: the memStore flow-reference methods are a real
// in-memory implementation in server_flow_reference_fake_test.go (mirrors
// the ApplicationBlock fake), so these tests run without a Postgres
// dependency. Routes are wired in a later task; the mux here uses the
// intended final patterns so the handlers are exercised exactly as mounted.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// buildFlowRefMux wires the six hand-written flow-reference routes. The wrap
// closure injects the test caller into context so created_by is populated.
func buildFlowRefMux(t *testing.T, store Store, caller *auth.Caller) http.Handler {
	t.Helper()
	mux := http.NewServeMux()

	wrap := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if caller != nil {
				r = r.WithContext(auth.WithCaller(r.Context(), caller))
			}
			h.ServeHTTP(w, r)
		})
	}

	mux.Handle("GET /v1/clusters/{id}/flow-references", wrap(HandleListFlowReferences(store)))
	mux.Handle("POST /v1/clusters/{id}/flow-references", wrap(HandleCreateFlowReference(store)))
	mux.Handle("GET /v1/clusters/{id}/flow-references/export", wrap(HandleExportFlowReferences(store)))
	mux.Handle("POST /v1/clusters/{id}/flow-references/import", wrap(HandleImportFlowReferences(store)))
	mux.Handle("PATCH /v1/flow-references/{id}", wrap(HandleUpdateFlowReference(store)))
	mux.Handle("DELETE /v1/flow-references/{id}", wrap(HandleDeleteFlowReference(store)))
	return mux
}

// internalRef is a valid internal-layer workload→workload TCP/5432 reference.
func internalRef() map[string]any {
	return map[string]any{
		"layer":         "internal",
		"direction":     "egress",
		"src_kind":      "workload",
		"src_ref":       "api",
		"dst_kind":      "workload",
		"dst_ref":       "postgres",
		"protocol":      "tcp",
		"from_port":     5432,
		"to_port":       5432,
		"justification": "api reads from the primary db",
	}
}

// postRaw posts a raw (non-JSON) body, used for the YAML import endpoint.
func postRaw(t *testing.T, h http.Handler, path, contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)) //nolint:noctx // in-process handler test
	req.Header.Set("Content-Type", contentType)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestFlowReferenceCreate_InvalidLayer asserts Validate is wired: an internal
// flow that references an endpoint_group is rejected with 409.
func TestFlowReferenceCreate_InvalidLayer(t *testing.T) {
	resetFlowRefFake()
	store := newMemStore()
	h := buildFlowRefMux(t, store, editorCaller())

	clusterID := uuid.New()
	bad := internalRef()
	bad["dst_kind"] = "endpoint_group" // internal must not reference an endpoint_group

	rr := doReq(t, h, http.MethodPost, "/v1/clusters/"+clusterID.String()+"/flow-references", bad)
	if rr.Code != http.StatusConflict {
		t.Fatalf("invalid create: want 409, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

// TestFlowReferenceCreate_Valid creates a valid internal reference (201) and
// verifies it lists back with created_by populated from the caller.
func TestFlowReferenceCreate_Valid(t *testing.T) {
	resetFlowRefFake()
	store := newMemStore()
	caller := editorCaller()
	h := buildFlowRefMux(t, store, caller)

	clusterID := uuid.New()
	rr := doReq(t, h, http.MethodPost, "/v1/clusters/"+clusterID.String()+"/flow-references", internalRef())
	if rr.Code != http.StatusCreated {
		t.Fatalf("valid create: want 201, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	var created FlowReference
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.SrcRef != "api" || created.DstRef != "postgres" {
		t.Fatalf("unexpected created ref: %+v", created)
	}
	if created.CreatedBy == nil || *created.CreatedBy != caller.UserID {
		t.Fatalf("created_by not set from caller: got %v want %v", created.CreatedBy, caller.UserID)
	}

	// List returns it.
	lr := doReq(t, h, http.MethodGet, "/v1/clusters/"+clusterID.String()+"/flow-references", nil)
	if lr.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", lr.Code)
	}
	var listed struct {
		Items []FlowReference `json:"items"`
	}
	if err := json.Unmarshal(lr.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Items) != 1 {
		t.Fatalf("list: want 1 item, got %d", len(listed.Items))
	}
}

// TestFlowReferenceExportImport_RoundTrip exports a created reference as YAML,
// then imports that same YAML into a fresh cluster (replace-all) and confirms
// the row is present.
func TestFlowReferenceExportImport_RoundTrip(t *testing.T) {
	resetFlowRefFake()
	store := newMemStore()
	h := buildFlowRefMux(t, store, editorCaller())

	srcCluster := uuid.New()
	if rr := doReq(t, h, http.MethodPost, "/v1/clusters/"+srcCluster.String()+"/flow-references", internalRef()); rr.Code != http.StatusCreated {
		t.Fatalf("seed create: want 201, got %d (body=%s)", rr.Code, rr.Body.String())
	}

	// Export → 200 text/yaml, body carries the ref data.
	exPath := "/v1/clusters/" + srcCluster.String() + "/flow-references/export"
	exReq := httptest.NewRequest(http.MethodGet, exPath, http.NoBody) //nolint:noctx // in-process handler test
	exRR := httptest.NewRecorder()
	h.ServeHTTP(exRR, exReq)
	if exRR.Code != http.StatusOK {
		t.Fatalf("export: want 200, got %d", exRR.Code)
	}
	if ct := exRR.Header().Get("Content-Type"); ct != "text/yaml" {
		t.Fatalf("export content-type: want text/yaml, got %q", ct)
	}
	yamlBody := exRR.Body.Bytes()
	for _, want := range []string{"references:", "src_ref: api", "dst_ref: postgres", "justification:"} {
		if !strings.Contains(string(yamlBody), want) {
			t.Fatalf("export body missing %q; got:\n%s", want, yamlBody)
		}
	}

	// Import the exported YAML into a different cluster (replace-all) → 200.
	dstCluster := uuid.New()
	imRR := postRaw(t, h, "/v1/clusters/"+dstCluster.String()+"/flow-references/import", "text/yaml", yamlBody)
	if imRR.Code != http.StatusOK {
		t.Fatalf("import: want 200, got %d (body=%s)", imRR.Code, imRR.Body.String())
	}
	var imported struct {
		Items []FlowReference `json:"items"`
	}
	if err := json.Unmarshal(imRR.Body.Bytes(), &imported); err != nil {
		t.Fatalf("decode import: %v", err)
	}
	if len(imported.Items) != 1 || imported.Items[0].DstRef != "postgres" {
		t.Fatalf("import result mismatch: %+v", imported.Items)
	}
}

// TestFlowReferenceImport_InvalidRow asserts per-row validation: an invalid
// entry fails the whole import with a 409 naming the row index.
func TestFlowReferenceImport_InvalidRow(t *testing.T) {
	resetFlowRefFake()
	store := newMemStore()
	h := buildFlowRefMux(t, store, editorCaller())

	clusterID := uuid.New()
	badYAML := []byte(`references:
  - layer: internal
    direction: egress
    src_kind: workload
    src_ref: api
    dst_kind: endpoint_group
    dst_ref: internet
    protocol: tcp
    justification: bogus
`)
	rr := postRaw(t, h, "/v1/clusters/"+clusterID.String()+"/flow-references/import", "text/yaml", badYAML)
	if rr.Code != http.StatusConflict {
		t.Fatalf("invalid import: want 409, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "row 0") {
		t.Fatalf("import error should name the offending row; got %s", rr.Body.String())
	}
}
