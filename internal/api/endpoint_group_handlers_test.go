package api

// Handler-level tests for the admin endpoint-group CRUD surface (ADR-0036).
//
// Fake-store strategy: the memStore endpoint-group methods are a real
// in-memory implementation in server_endpoint_group_fake_test.go, so these
// tests run without a Postgres dependency. Routes are wired in a later task;
// the mux here uses the intended final patterns so the handlers are exercised
// exactly as mounted.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// buildEndpointGroupMux wires the five hand-written endpoint-group routes. The
// wrap closure injects the test caller into context so created_by is populated.
func buildEndpointGroupMux(t *testing.T, store Store, caller *auth.Caller) http.Handler {
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

	mux.Handle("GET /v1/admin/endpoint-groups", wrap(HandleListEndpointGroups(store)))
	mux.Handle("POST /v1/admin/endpoint-groups", wrap(HandleCreateEndpointGroup(store)))
	mux.Handle("GET /v1/admin/endpoint-groups/{id}", wrap(HandleGetEndpointGroup(store)))
	mux.Handle("PATCH /v1/admin/endpoint-groups/{id}", wrap(HandleUpdateEndpointGroup(store)))
	mux.Handle("DELETE /v1/admin/endpoint-groups/{id}", wrap(HandleDeleteEndpointGroup(store)))
	return mux
}

// seedEndpointGroup creates a corp-vpn group and returns the decoded row,
// asserting 201 + name/cidrs/created_by along the way.
func seedEndpointGroup(t *testing.T, h http.Handler, caller *auth.Caller) EndpointGroup {
	t.Helper()
	body := map[string]any{
		"name":  "corp-vpn",
		"notes": "office egress ranges",
		"cidrs": []string{"10.0.0.0/8", "192.168.0.0/16"},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/admin/endpoint-groups", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	var created EndpointGroup
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.Name != "corp-vpn" || len(created.CIDRs) != 2 || created.CIDRs[0] != "10.0.0.0/8" {
		t.Fatalf("unexpected created group: %+v", created)
	}
	if created.CreatedBy == nil || *created.CreatedBy != caller.UserID {
		t.Fatalf("created_by not set from caller: got %v want %v", created.CreatedBy, caller.UserID)
	}
	return created
}

// TestEndpointGroupCreateList covers create (name+cidrs) → 201 and list → 200
// containing the created group with its CIDR.
func TestEndpointGroupCreateList(t *testing.T) {
	resetEndpointGroupFake()
	store := newMemStore()
	caller := editorCaller()
	h := buildEndpointGroupMux(t, store, caller)

	created := seedEndpointGroup(t, h, caller)

	lr := doReq(t, h, http.MethodGet, "/v1/admin/endpoint-groups", nil)
	if lr.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", lr.Code)
	}
	var listed struct {
		Items []EndpointGroup `json:"items"`
	}
	if err := json.Unmarshal(lr.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Items) != 1 || listed.Items[0].ID != created.ID {
		t.Fatalf("list: want the created group, got %+v", listed.Items)
	}
	if len(listed.Items[0].CIDRs) != 2 || listed.Items[0].CIDRs[1] != "192.168.0.0/16" {
		t.Fatalf("list: cidrs not returned: %+v", listed.Items[0].CIDRs)
	}
}

// TestEndpointGroupGetDelete covers get-unknown → 404, delete → 204, and
// get-after-delete → 404.
func TestEndpointGroupGetDelete(t *testing.T) {
	resetEndpointGroupFake()
	store := newMemStore()
	caller := editorCaller()
	h := buildEndpointGroupMux(t, store, caller)

	created := seedEndpointGroup(t, h, caller)

	if gr := doReq(t, h, http.MethodGet, "/v1/admin/endpoint-groups/"+uuid.New().String(), nil); gr.Code != http.StatusNotFound {
		t.Fatalf("get unknown: want 404, got %d", gr.Code)
	}
	if dr := doReq(t, h, http.MethodDelete, "/v1/admin/endpoint-groups/"+created.ID.String(), nil); dr.Code != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d (body=%s)", dr.Code, dr.Body.String())
	}
	if gr2 := doReq(t, h, http.MethodGet, "/v1/admin/endpoint-groups/"+created.ID.String(), nil); gr2.Code != http.StatusNotFound {
		t.Fatalf("get after delete: want 404, got %d", gr2.Code)
	}
}
