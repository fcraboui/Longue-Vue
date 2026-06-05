package api

// Handler tests for GET /v1/security-groups and GET /v1/security-groups/{id}
// (flow-matrix Phase 1, Task 19). Uses the in-memory sgFake fake from
// server_cloud_fake_test.go; no Postgres dependency.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// buildSecurityGroupMux wires the two read-only security-group routes.
func buildSecurityGroupMux(t *testing.T, store Store, caller *auth.Caller) http.Handler {
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
	mux.Handle("GET /v1/security-groups", wrap(HandleListSecurityGroups(store)))
	mux.Handle("GET /v1/security-groups/{id}", wrap(HandleGetSecurityGroup(store)))
	return mux
}

func TestListSecurityGroups_FilterByCloudAccount(t *testing.T) {
	resetSGFake()
	store := newMemStore()

	accountA := uuid.New()
	accountB := uuid.New()

	// Two SGs in accountA, one in accountB.
	sg1ID, err := store.UpsertSecurityGroup(t.Context(), SecurityGroupRow{
		CloudAccountID: accountA, ProviderSGID: "sg-001", Name: "web-sg", VPCID: "vpc-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = sg1ID
	_, err = store.UpsertSecurityGroup(t.Context(), SecurityGroupRow{
		CloudAccountID: accountA, ProviderSGID: "sg-002", Name: "db-sg", VPCID: "vpc-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.UpsertSecurityGroup(t.Context(), SecurityGroupRow{
		CloudAccountID: accountB, ProviderSGID: "sg-003", Name: "other-sg", VPCID: "vpc-2",
	})
	if err != nil {
		t.Fatal(err)
	}

	h := buildSecurityGroupMux(t, store, readCaller())

	rr := doReq(t, h, http.MethodGet,
		fmt.Sprintf("/v1/security-groups?cloud_account_id=%s", accountA),
		nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	var resp struct {
		Items []SecurityGroupRow `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("want 2 SGs for accountA, got %d", len(resp.Items))
	}
	for _, sg := range resp.Items {
		if sg.CloudAccountID != accountA {
			t.Errorf("unexpected cloud_account_id %s", sg.CloudAccountID)
		}
	}
}

func TestListSecurityGroups_MissingAccountID_Returns400(t *testing.T) {
	resetSGFake()
	store := newMemStore()
	h := buildSecurityGroupMux(t, store, readCaller())

	rr := doReq(t, h, http.MethodGet, "/v1/security-groups", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
}

func TestGetSecurityGroup_EmbedsRules(t *testing.T) {
	resetSGFake()
	store := newMemStore()

	accountID := uuid.New()
	sgID, err := store.UpsertSecurityGroup(t.Context(), SecurityGroupRow{
		CloudAccountID: accountID, ProviderSGID: "sg-100", Name: testSGName,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceSecurityGroupRules(t.Context(), sgID, []SecurityGroupRuleRow{
		{
			Direction: testDirIngress,
			Protocol:  testProtocolTCP,
			FromPort:  intPtr(443),
			ToPort:    intPtr(443),
			PeerKind:  testPeerKindCIDR,
			PeerCIDR:  testCIDRAny,
		},
		{Direction: "egress", Protocol: "any", PeerKind: testPeerKindCIDR, PeerCIDR: testCIDRAny},
	}); err != nil {
		t.Fatal(err)
	}

	h := buildSecurityGroupMux(t, store, readCaller())

	rr := doReq(t, h, http.MethodGet, fmt.Sprintf("/v1/security-groups/%s", sgID), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	var resp sgDetailResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID != sgID {
		t.Errorf("id mismatch: got %s want %s", resp.ID, sgID)
	}
	if resp.Name != testSGName {
		t.Errorf("name: got %q want %s", resp.Name, testSGName)
	}
	if len(resp.Rules) != 2 {
		t.Fatalf("want 2 rules, got %d", len(resp.Rules))
	}
}

func TestGetSecurityGroup_NotFound(t *testing.T) {
	resetSGFake()
	store := newMemStore()
	h := buildSecurityGroupMux(t, store, readCaller())

	rr := doReq(t, h, http.MethodGet, fmt.Sprintf("/v1/security-groups/%s", uuid.New()), nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d body=%q", rr.Code, rr.Body.String())
	}
}

// intPtr is a local helper returning a pointer to an int for SG rule ports.
func intPtr(v int) *int { return &v }
