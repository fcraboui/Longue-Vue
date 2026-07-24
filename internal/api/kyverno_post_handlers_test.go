package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

func buildKyvernoPostMux(t *testing.T, store Store, caller *auth.Caller) http.Handler {
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
	mux.Handle("POST /v1/cluster-policies", wrap(HandleCreateClusterPolicy(store)))
	mux.Handle("POST /v1/policy-reports", wrap(HandleCreatePolicyReport(store)))
	return mux
}

func enablePolicies(t *testing.T, store Store) {
	t.Helper()
	on := true
	if _, err := store.UpdateSettings(t.Context(), SettingsPatch{PoliciesEnabled: &on}); err != nil {
		t.Fatalf("enable policies: %v", err)
	}
}

func TestCreateClusterPolicy_201(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"scope":         "cluster",
		"spec_raw":      map[string]any{"rules": []any{}},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := got["id"]; !ok {
		t.Error("response missing 'id'")
	}
}

func TestCreateClusterPolicy_400MissingName(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"resource_type": "ClusterPolicy",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_400MissingClusterID(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_400MissingResourceType(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id": uuid.New(),
		"name":       "require-labels",
		"spec_raw":   map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_400MissingSpecRaw(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_400InvalidResourceType(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "InvalidType",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_400InvalidScope(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"scope":         "global",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_403WithoutWriteScope(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, viewerCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_409PoliciesDisabled(t *testing.T) {
	store := newMemStore()
	// PoliciesEnabled defaults to false — no enablePolicies call
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status: got %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_400PolicyWithoutNamespace(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "Policy",
		"scope":         "namespace",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400 (Policy requires namespace_id); body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_400ClusterPolicyWithNamespace(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"namespace_id":  uuid.New(),
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"scope":         "cluster",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400 (ClusterPolicy forbids namespace_id); body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_DefaultScopeCluster(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_Normalisation(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":     uuid.New(),
		"name":           "require-labels",
		"resource_type":  "ClusterPolicy",
		"severity":       "HIGH",
		"action":         "ENFORCE",
		"failure_policy": "fail",
		"spec_raw":       map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["severity"] != "high" {
		t.Errorf("severity: got %v, want high", got["severity"])
	}
	if got["action"] != "enforce" {
		t.Errorf("action: got %v, want enforce", got["action"])
	}
	if got["failure_policy"] != "Fail" {
		t.Errorf("failure_policy: got %v, want Fail", got["failure_policy"])
	}
}

func TestCreatePolicyReport_201(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":   uuid.New(),
		"name":         "pr-ns-default",
		"scope_kind":   "Namespace",
		"scope_name":   "default",
		"summary_pass": 5,
		"summary_fail": 1,
	}
	rr := doReq(t, h, http.MethodPost, "/v1/policy-reports", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := got["id"]; !ok {
		t.Error("response missing 'id'")
	}
}

func TestCreatePolicyReport_400MissingName(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id": uuid.New(),
	}
	rr := doReq(t, h, http.MethodPost, "/v1/policy-reports", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreatePolicyReport_400MissingClusterID(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"name": "pr-ns-default",
	}
	rr := doReq(t, h, http.MethodPost, "/v1/policy-reports", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreatePolicyReport_403WithoutWriteScope(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, viewerCaller())

	body := map[string]any{
		"cluster_id": uuid.New(),
		"name":       "pr-ns-default",
	}
	rr := doReq(t, h, http.MethodPost, "/v1/policy-reports", body)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreatePolicyReport_409PoliciesDisabled(t *testing.T) {
	store := newMemStore()
	// PoliciesEnabled defaults to false
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id": uuid.New(),
		"name":       "pr-ns-default",
	}
	rr := doReq(t, h, http.MethodPost, "/v1/policy-reports", body)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status: got %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreatePolicyReport_ScopeKindNormalisation(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":   uuid.New(),
		"name":         "pr-deploy",
		"scope_kind":   "deployment",
		"scope_name":   "web",
		"summary_pass": 3,
	}
	rr := doReq(t, h, http.MethodPost, "/v1/policy-reports", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["scope_kind"] != "Deployment" {
		t.Errorf("scope_kind: got %v, want Deployment", got["scope_kind"])
	}
}

func TestCreateClusterPolicy_InvalidJSON(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	req := httptest.NewRequest(http.MethodPost, "/v1/cluster-policies", strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreatePolicyReport_InvalidJSON(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	req := httptest.NewRequest(http.MethodPost, "/v1/policy-reports", strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}
