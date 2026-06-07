package api

// Tests for the audited flow-matrix extracts (R3 Task 4). Uses the shared
// memStore fake + doReq helper. The fake's flow-matrix store methods return
// empty lists, so the synthesis is empty and CSV is header-only — fine for
// shape / gating / audit-allowlist assertions.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const flowExtractMaxRows = 50000

func flowExtractMux(store Store) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /v1/clusters/{id}/flow-matrix/extract",
		HandleFlowMatrixExtract(store, flowExtractMaxRows))
	mux.Handle("GET /v1/clusters/{id}/flow-matrix/extract.zip",
		HandleFlowMatrixExtractZip(store, flowExtractMaxRows))
	return mux
}

func enableFlowMatrixForExtract(t *testing.T, store Store) {
	t.Helper()
	on := true
	if _, err := store.UpdateSettings(t.Context(), SettingsPatch{FlowMatrixEnabled: &on}); err != nil {
		t.Fatalf("enable flow_matrix: %v", err)
	}
}

func TestFlowMatrixExtract_CSV(t *testing.T) {
	store := newMemStore()
	enableFlowMatrixForExtract(t, store)
	h := flowExtractMux(store)

	path := "/v1/clusters/" + uuid.NewString() + "/flow-matrix/extract?format=csv"
	rr := doReq(t, h, http.MethodGet, path, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("content-type: got %q, want text/csv", ct)
	}
	// Body carries a UTF-8 BOM before the header row.
	body := strings.TrimPrefix(rr.Body.String(), string([]byte{0xEF, 0xBB, 0xBF}))
	wantHeader := "cluster,layer,direction,src_kind,src_ref,dst_kind,dst_ref,protocol,ports,state,reference_id,sources"
	if !strings.HasPrefix(body, wantHeader) {
		t.Errorf("body does not start with header row;\n got: %q\nwant prefix: %q", body, wantHeader)
	}
}

func TestFlowMatrixExtract_GatedOff(t *testing.T) {
	store := newMemStore() // default settings: flow_matrix_enabled = false
	h := flowExtractMux(store)

	path := "/v1/clusters/" + uuid.NewString() + "/flow-matrix/extract?format=csv"
	rr := doReq(t, h, http.MethodGet, path, nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status: got %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != ctProblemJSON {
		t.Errorf("content-type: got %q, want %s", ct, ctProblemJSON)
	}
}

func TestFlowMatrixExtract_JSONEnvelope(t *testing.T) {
	store := newMemStore()
	enableFlowMatrixForExtract(t, store)
	h := flowExtractMux(store)

	clusterID := uuid.NewString()
	path := "/v1/clusters/" + clusterID + "/flow-matrix/extract?format=json"
	rr := doReq(t, h, http.MethodGet, path, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: got %q, want application/json", ct)
	}
	var env struct {
		ClusterID  string `json:"cluster_id"`
		TotalCount int    `json:"total_count"`
		Truncated  bool   `json:"truncated"`
		Items      []any  `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v; raw=%s", err, rr.Body.String())
	}
	if env.ClusterID != clusterID {
		t.Errorf("cluster_id: got %q, want %q", env.ClusterID, clusterID)
	}
	if env.TotalCount != len(env.Items) {
		t.Errorf("total_count %d != len(items) %d", env.TotalCount, len(env.Items))
	}
}

func TestFlowMatrixExtract_Zip(t *testing.T) {
	store := newMemStore()
	enableFlowMatrixForExtract(t, store)
	h := flowExtractMux(store)

	path := "/v1/clusters/" + uuid.NewString() + "/flow-matrix/extract.zip"
	rr := doReq(t, h, http.MethodGet, path, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("content-type: got %q, want application/zip", ct)
	}
	if rr.Body.Len() == 0 {
		t.Error("zip body is empty")
	}
}

func TestFlowMatrixExtract_BadFormat(t *testing.T) {
	store := newMemStore()
	enableFlowMatrixForExtract(t, store)
	h := flowExtractMux(store)

	path := "/v1/clusters/" + uuid.NewString() + "/flow-matrix/extract?format=xml"
	rr := doReq(t, h, http.MethodGet, path, nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestShouldAudit_AllowlistsFlowMatrixExtract(t *testing.T) {
	cid := uuid.NewString()
	cases := []string{
		"/v1/clusters/" + cid + "/flow-matrix/extract",
		"/v1/clusters/" + cid + "/flow-matrix/extract.zip",
	}
	for _, path := range cases {
		req := httptest.NewRequest(http.MethodGet, path, http.NoBody) //nolint:noctx // shouldAudit ignores ctx
		if !shouldAudit(req) {
			t.Errorf("shouldAudit(%q) = false, want true", path)
		}
	}
	// A bare synthesis path must NOT be auto-audited by the prefix rule
	// (it relies on auditWrap, not the GET allowlist).
	req := httptest.NewRequest(http.MethodGet, "/v1/clusters/"+cid+"/flow-matrix", http.NoBody) //nolint:noctx // shouldAudit ignores ctx
	if shouldAudit(req) {
		t.Errorf("shouldAudit(flow-matrix synthesis) = true, want false")
	}
}
