package api

// Tests for the cluster flow-matrix synthesis handler (R2 Task 7).
// Uses the shared memStore fake + doReq helper. The fake's flow-matrix
// store methods return empty lists by default, so an empty cluster yields
// an empty-but-valid synthesis.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// ctProblemJSON is the RFC 7807 content type asserted across api handler tests.
const ctProblemJSON = "application/problem+json"

func flowMatrixMux(store Store) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /v1/clusters/{id}/flow-matrix", HandleClusterFlowMatrix(store))
	return mux
}

func TestHandleClusterFlowMatrix_GatedOff(t *testing.T) {
	store := newMemStore() // default settings: flow_matrix_enabled = false
	h := flowMatrixMux(store)

	rr := doReq(t, h, http.MethodGet, "/v1/clusters/"+uuid.NewString()+"/flow-matrix", nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status: got %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != ctProblemJSON {
		t.Errorf("content-type: got %q, want %s", ct, ctProblemJSON)
	}
}

func TestHandleClusterFlowMatrix_Enabled_ReturnsSynthesis(t *testing.T) {
	store := newMemStore()
	on := true
	if _, err := store.UpdateSettings(t.Context(), SettingsPatch{FlowMatrixEnabled: &on}); err != nil {
		t.Fatalf("enable flow_matrix: %v", err)
	}
	h := flowMatrixMux(store)

	clusterID := uuid.NewString()
	rr := doReq(t, h, http.MethodGet, "/v1/clusters/"+clusterID+"/flow-matrix", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type: got %q, want application/json", ct)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, rr.Body.String())
	}
	for _, key := range []string{"cluster_id", "perimeter", "internal"} {
		if _, ok := body[key]; !ok {
			t.Errorf("response missing key %q; body=%s", key, rr.Body.String())
		}
	}

	var gotCluster string
	if err := json.Unmarshal(body["cluster_id"], &gotCluster); err != nil {
		t.Fatalf("decode cluster_id: %v", err)
	}
	if gotCluster != clusterID {
		t.Errorf("cluster_id: got %q, want %q", gotCluster, clusterID)
	}
}
