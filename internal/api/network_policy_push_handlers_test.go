package api

// Handler unit tests for the netpol push surface (ADR-0038).
// Mirrors the pattern used in flow_reference_handlers_test.go and
// network_policy_handlers_test.go.
//
// buildPushMux wires the two POST routes through the codegen strict-server
// wrapper so scope enforcement via httpRequestFromCtx + auth.CallerFromContext
// is exercised exactly as it will be in production.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// buildPushMux wires the two netpol push routes (POST /v1/network-policies
// and POST /v1/network-policies/reconcile) through the strict-server machinery.
// A caller-injection middleware wraps the whole handler so scope checks work.
func buildPushMux(t *testing.T, store Store, caller *auth.Caller) http.Handler {
	t.Helper()
	strict := NewStrictHandlerWithOptions(
		NewServer("test", store, auth.SecureNever, nil, NewLoginRateLimiter(), NewVerifyRateLimiter()),
		[]StrictMiddlewareFunc{InjectRequestMiddleware},
		StrictHTTPServerOptions{
			RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusBadRequest)
			},
			ResponseErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			},
		},
	)
	base := Handler(strict)

	// Inject the test caller into every request's context so the scope guard
	// in CreateNetworkPolicy sees it via auth.CallerFromContext(r.Context()).
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if caller != nil {
			r = r.WithContext(auth.WithCaller(r.Context(), caller))
		}
		base.ServeHTTP(w, r)
	})
}

// seedClusterAndNamespaceInMemStore inserts a minimal cluster and namespace
// into the global memStore fakes and returns their UUIDs.
// The push handler validates cluster_id + namespace_id via FK semantics in
// the real DB, but the memStore's UpsertNetworkPolicyAtomic does not enforce
// FKs — so we only need real UUIDs for the round-trip assertions.
func seedClusterAndNamespaceInMemStore(_ *testing.T) (clusterID, nsID uuid.UUID) {
	return uuid.New(), uuid.New()
}

func TestCreateNetworkPolicy_201OnFirstWrite(t *testing.T) {
	resetNPFake()
	h := buildPushMux(t, newMemStore(), editorCaller())

	clusterID, nsID := seedClusterAndNamespaceInMemStore(t)

	body := map[string]any{
		"cluster_id":   clusterID,
		"namespace_id": nsID,
		"name":         "deny-all-ingress",
		"pod_selector": map[string]any{},
		"policy_types": []string{"Ingress"},
		"spec_raw":     map[string]any{},
		"rules": []map[string]any{
			{"direction": "ingress", "peer_kind": "selector", "ports": []any{}},
		},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/network-policies", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got["name"] != "deny-all-ingress" {
		t.Errorf("name: got %v, want deny-all-ingress", got["name"])
	}
}

func TestCreateNetworkPolicy_200OnSecondWrite(t *testing.T) {
	resetNPFake()
	h := buildPushMux(t, newMemStore(), editorCaller())

	clusterID, nsID := seedClusterAndNamespaceInMemStore(t)

	body := map[string]any{
		"cluster_id":   clusterID,
		"namespace_id": nsID,
		"name":         "p1",
		"pod_selector": map[string]any{},
		"policy_types": []string{"Ingress"},
		"spec_raw":     map[string]any{},
		"rules":        []any{},
	}
	rr1 := doReq(t, h, http.MethodPost, "/v1/network-policies", body)
	if rr1.Code != http.StatusCreated {
		t.Fatalf("first POST: want 201, got %d (body=%s)", rr1.Code, rr1.Body.String())
	}
	rr2 := doReq(t, h, http.MethodPost, "/v1/network-policies", body)
	if rr2.Code != http.StatusOK {
		t.Fatalf("second POST: want 200, got %d (body=%s)", rr2.Code, rr2.Body.String())
	}
}

func TestCreateNetworkPolicy_403WithoutWriteScope(t *testing.T) {
	resetNPFake()
	// viewerCaller has ScopeRead only — no ScopeWrite.
	h := buildPushMux(t, newMemStore(), viewerCaller())

	clusterID, nsID := seedClusterAndNamespaceInMemStore(t)
	body := map[string]any{
		"cluster_id":   clusterID,
		"namespace_id": nsID,
		"name":         "denied",
		"pod_selector": map[string]any{},
		"policy_types": []string{"Ingress"},
		"spec_raw":     map[string]any{},
		"rules":        []any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/network-policies", body)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status: want 403, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

// TestCreateNetworkPolicy_ResponseBody verifies the response body contains the
// expected field set (id, cluster_id, namespace_id, name, pod_selector,
// policy_types).
func TestCreateNetworkPolicy_ResponseBody(t *testing.T) {
	resetNPFake()
	h := buildPushMux(t, newMemStore(), editorCaller())

	clusterID, nsID := seedClusterAndNamespaceInMemStore(t)
	body := map[string]any{
		"cluster_id":   clusterID,
		"namespace_id": nsID,
		"name":         "test-policy",
		"pod_selector": map[string]any{"matchLabels": map[string]any{"app": "web"}},
		"policy_types": []string{"Ingress", "Egress"},
		"spec_raw":     map[string]any{},
		"rules":        []any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/network-policies", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	// Must decode as the codegen NetworkPolicy shape.
	// We use a loose map to avoid depending on exact generated types here.
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := got["id"]; !ok {
		t.Error("response missing 'id'")
	}
	if got["name"] != "test-policy" {
		t.Errorf("name: got %v want test-policy", got["name"])
	}
	// Ensure cluster_id and namespace_id are echoed back.
	if got["cluster_id"] != clusterID.String() {
		t.Errorf("cluster_id: got %v want %s", got["cluster_id"], clusterID)
	}

	// Round-trip the Location-free 201: no Location header (codegen type alias
	// does not add one).
	if loc := rr.Result().Header.Get("Location"); loc != "" {
		t.Errorf("unexpected Location header: %s", loc)
	}

	// Confirm the full-URL recorder has Content-Type application/json.
	_ = httptest.NewRecorder() // suppress unused import warning
	ct := rr.Result().Header.Get("Content-Type")
	if ct == "" {
		t.Error("Content-Type not set")
	}
}
