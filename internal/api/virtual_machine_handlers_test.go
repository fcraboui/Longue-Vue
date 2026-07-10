package api

// Handler tests for the virtual_machines hand-written endpoints (ADR-0015).
// Builds on the mux+helpers defined in cloud_account_handlers_test.go:
// buildCloudMux, doReq, adminCaller, ctProblemJSON.

import (
	"net/http"
	"testing"
)

func TestListVirtualMachinesBadSort400(t *testing.T) {
	store := newMemStore()
	h := buildCloudMux(t, store, newTestEncrypter(t), adminCaller())

	rr := doReq(t, h, http.MethodGet, "/v1/virtual-machines?sort=bogus", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != ctProblemJSON {
		t.Errorf("content-type=%q", ct)
	}
}

func TestListCloudAccountsBadSort400(t *testing.T) {
	store := newMemStore()
	h := buildCloudMux(t, store, newTestEncrypter(t), adminCaller())

	rr := doReq(t, h, http.MethodGet, "/v1/admin/cloud-accounts?sort=bogus", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != ctProblemJSON {
		t.Errorf("content-type=%q", ct)
	}
}
