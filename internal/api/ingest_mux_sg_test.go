package api

// Tests for the SG-persistence side-effect of HandleUpsertVirtualMachine
// (flow-matrix P1, Task 13). Uses the existing memStore + buildCloudMux
// test helpers — no PostgreSQL required.
//
// The integration coverage for the full store path is in internal/store/
// pg_security_groups_test.go. A cross-package integration test exercising
// the full HTTP→handler→PG path is deferred to Task 27.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// TestIngest_VMUpsert_PersistsSGs verifies that posting a VM ingest payload
// with a canonical SGSchemaVersion=1 block causes:
//  1. An HTTP 200 from HandleUpsertVirtualMachine (VM upsert succeeds).
//  2. One SecurityGroupRow written to the fake store with ProviderSGID="sg-test".
//  3. One SecurityGroupRuleRow for that SG with Protocol="tcp".
//
// Failure to persist SGs must NOT fail the VM upsert (best-effort contract).
func TestIngest_VMUpsert_PersistsSGs(t *testing.T) {
	t.Parallel()

	resetCloudFake()
	resetSGFake()

	// Create a cloud account so the token binding checks pass.
	accID := uuid.New()
	cloudFake.mu.Lock()
	cloudFake.accounts[accID] = CloudAccount{
		ID:       accID,
		Provider: "outscale",
		Name:     "test-account",
		Region:   "eu-west-2",
		Status:   CloudAccountStatusActive,
	}
	cloudFake.mu.Unlock()

	store := newMemStore()
	caller := collectorCaller(&accID)
	h := buildCloudMux(t, store, nil, caller)

	// Build a canonical SGSchemaVersion=1 payload with one ingress TCP/5432 rule.
	port := 5432
	sg := sgWireGroup{
		ProviderSGID: "sg-test",
		Name:         "test-sg",
		VPCID:        "vpc-abc",
		Ingress: []sgWireRule{
			{
				Protocol: "tcp",
				FromPort: &port,
				ToPort:   &port,
				Peer: sgWirePeer{
					Kind: "cidr",
					CIDR: "10.0.0.0/8",
				},
			},
		},
		Tags: map[string]string{"env": "test"},
	}
	sgPayload := sgWirePayload{
		Version: 1, // SGSchemaVersion
		Groups:  []sgWireGroup{sg},
	}
	sgJSON, err := json.Marshal(sgPayload)
	if err != nil {
		t.Fatalf("marshal SG payload: %v", err)
	}

	body := map[string]any{
		"cloud_account_id": accID.String(),
		"provider_vm_id":   "i-test-vm",
		"name":             "test-vm",
		"power_state":      "running",
		"security_groups":  json.RawMessage(sgJSON),
	}

	rr := doReq(t, h, http.MethodPost, "/v1/virtual-machines", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Assert: one SG with the expected ProviderSGID.
	sgs, _, err := store.ListSecurityGroupsByAccount(t.Context(), accID, 100, "")
	if err != nil {
		t.Fatalf("ListSecurityGroupsByAccount: %v", err)
	}
	if len(sgs) != 1 {
		t.Fatalf("want 1 SG, got %d", len(sgs))
	}
	if sgs[0].ProviderSGID != "sg-test" {
		t.Errorf("ProviderSGID = %q, want %q", sgs[0].ProviderSGID, "sg-test")
	}
	if sgs[0].CloudAccountID != accID {
		t.Errorf("CloudAccountID = %v, want %v", sgs[0].CloudAccountID, accID)
	}

	// Assert: one rule with Protocol="tcp".
	rules, err := store.ListSecurityGroupRules(t.Context(), sgs[0].ID)
	if err != nil {
		t.Fatalf("ListSecurityGroupRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("want 1 rule, got %d", len(rules))
	}
	if rules[0].Protocol != "tcp" {
		t.Errorf("Protocol = %q, want %q", rules[0].Protocol, "tcp")
	}
	if rules[0].Direction != "ingress" {
		t.Errorf("Direction = %q, want %q", rules[0].Direction, "ingress")
	}
}

// TestIngest_VMUpsert_PreCanonical_SkipsSGs verifies that a VM ingest with a
// pre-canonical security_groups field (schema_version=0 / missing) leaves the
// SG table empty and still returns 200.
func TestIngest_VMUpsert_PreCanonical_SkipsSGs(t *testing.T) {
	t.Parallel()

	resetCloudFake()
	resetSGFake()

	accID := uuid.New()
	cloudFake.mu.Lock()
	cloudFake.accounts[accID] = CloudAccount{
		ID:       accID,
		Provider: "outscale",
		Name:     "test-account2",
		Region:   "eu-west-2",
		Status:   CloudAccountStatusActive,
	}
	cloudFake.mu.Unlock()

	store := newMemStore()
	caller := &auth.Caller{
		Kind:                auth.CallerKindToken,
		TokenID:             uuid.New(),
		Scopes:              []string{auth.ScopeVMCollector},
		BoundCloudAccountID: &accID,
	}
	h := buildCloudMux(t, store, nil, caller)

	// Payload with no schema_version (pre-deploy shape).
	body := map[string]any{
		"cloud_account_id": accID.String(),
		"provider_vm_id":   "i-old-vm",
		"name":             "old-vm",
		"power_state":      "running",
		"security_groups":  json.RawMessage(`[{"id":"sg-old","name":"old"}]`),
	}

	rr := doReq(t, h, http.MethodPost, "/v1/virtual-machines", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// SG table must be empty — pre-canonical payload must be skipped.
	sgs, _, err := store.ListSecurityGroupsByAccount(t.Context(), accID, 100, "")
	if err != nil {
		t.Fatalf("ListSecurityGroupsByAccount: %v", err)
	}
	if len(sgs) != 0 {
		t.Errorf("want 0 SGs for pre-canonical payload, got %d", len(sgs))
	}
}

// TestSweepSecurityGroups_RemovesUnseen verifies that the sweep endpoint
// deletes SGs not present in seen_provider_sg_ids and returns 204.
func TestSweepSecurityGroups_RemovesUnseen(t *testing.T) {
	resetCloudFake()
	resetSGFake()

	accID := uuid.New()
	cloudFake.mu.Lock()
	cloudFake.accounts[accID] = CloudAccount{
		ID:       accID,
		Provider: "outscale",
		Name:     "sweep-test-account",
		Region:   "eu-west-2",
		Status:   CloudAccountStatusActive,
	}
	cloudFake.mu.Unlock()

	store := newMemStore()

	// Seed an SG that will NOT be in the seen list.
	if _, err := store.UpsertSecurityGroup(t.Context(), SecurityGroupRow{
		CloudAccountID: accID,
		ProviderSGID:   "sg-gone",
		Name:           "gone-sg",
	}); err != nil {
		t.Fatalf("seed UpsertSecurityGroup: %v", err)
	}

	caller := collectorCaller(&accID)
	h := buildCloudMux(t, store, nil, caller)

	body := map[string]any{
		"seen_provider_sg_ids": []string{"sg-other"},
	}
	rr := doReq(t, h, http.MethodPost,
		"/v1/ingest/cloud-accounts/"+accID.String()+"/security-groups/sweep", body)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status: %d %s", rr.Code, rr.Body.String())
	}

	sgs, _, err := store.ListSecurityGroupsByAccount(t.Context(), accID, 100, "")
	if err != nil {
		t.Fatalf("ListSecurityGroupsByAccount: %v", err)
	}
	if len(sgs) != 0 {
		t.Fatalf("expected sweep to delete sg-gone, got %d remaining", len(sgs))
	}
}

// TestSweepSecurityGroups_KeepsSeen verifies that SGs in seen_provider_sg_ids
// survive the sweep.
func TestSweepSecurityGroups_KeepsSeen(t *testing.T) {
	resetCloudFake()
	resetSGFake()

	accID := uuid.New()
	cloudFake.mu.Lock()
	cloudFake.accounts[accID] = CloudAccount{
		ID:       accID,
		Provider: "outscale",
		Name:     "sweep-keep-account",
		Region:   "eu-west-2",
		Status:   CloudAccountStatusActive,
	}
	cloudFake.mu.Unlock()

	store := newMemStore()

	// Seed two SGs; one will be seen, one will not.
	if _, err := store.UpsertSecurityGroup(t.Context(), SecurityGroupRow{
		CloudAccountID: accID,
		ProviderSGID:   "sg-keep",
		Name:           "keep-sg",
	}); err != nil {
		t.Fatalf("seed sg-keep: %v", err)
	}
	if _, err := store.UpsertSecurityGroup(t.Context(), SecurityGroupRow{
		CloudAccountID: accID,
		ProviderSGID:   "sg-drop",
		Name:           "drop-sg",
	}); err != nil {
		t.Fatalf("seed sg-drop: %v", err)
	}

	caller := collectorCaller(&accID)
	h := buildCloudMux(t, store, nil, caller)

	body := map[string]any{
		"seen_provider_sg_ids": []string{"sg-keep"},
	}
	rr := doReq(t, h, http.MethodPost,
		"/v1/ingest/cloud-accounts/"+accID.String()+"/security-groups/sweep", body)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status: %d %s", rr.Code, rr.Body.String())
	}

	sgs, _, err := store.ListSecurityGroupsByAccount(t.Context(), accID, 100, "")
	if err != nil {
		t.Fatalf("ListSecurityGroupsByAccount: %v", err)
	}
	if len(sgs) != 1 {
		t.Fatalf("expected 1 SG after sweep, got %d", len(sgs))
	}
	if sgs[0].ProviderSGID != "sg-keep" {
		t.Errorf("surviving SG = %q, want sg-keep", sgs[0].ProviderSGID)
	}
}

// TestSweepSecurityGroups_ForbiddenForOtherAccount verifies that a vm-collector
// token bound to account A cannot sweep account B (403).
func TestSweepSecurityGroups_ForbiddenForOtherAccount(t *testing.T) {
	resetCloudFake()
	resetSGFake()

	accA := uuid.New()
	accB := uuid.New()
	cloudFake.mu.Lock()
	cloudFake.accounts[accA] = CloudAccount{ID: accA, Provider: "outscale", Name: "acct-a", Region: "eu-west-2", Status: CloudAccountStatusActive}
	cloudFake.accounts[accB] = CloudAccount{ID: accB, Provider: "outscale", Name: "acct-b", Region: "eu-west-2", Status: CloudAccountStatusActive}
	cloudFake.mu.Unlock()

	store := newMemStore()
	// Caller is bound to accA, tries to sweep accB.
	caller := collectorCaller(&accA)
	h := buildCloudMux(t, store, nil, caller)

	body := map[string]any{"seen_provider_sg_ids": []string{}}
	rr := doReq(t, h, http.MethodPost,
		"/v1/ingest/cloud-accounts/"+accB.String()+"/security-groups/sweep", body)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d %s", rr.Code, rr.Body.String())
	}
}

// TestIngest_VMUpsert_SGFailure_DoesNotFailVM verifies the best-effort contract:
// even if the SG store returns an error, the VM ingest still returns 200.
// (This is inherently covered by the memStore never returning errors, but we
// document the intent explicitly.)
func TestIngest_VMUpsert_SGFailure_DoesNotFailVM(t *testing.T) {
	t.Parallel()

	resetCloudFake()
	resetSGFake()

	accID := uuid.New()
	cloudFake.mu.Lock()
	cloudFake.accounts[accID] = CloudAccount{
		ID:       accID,
		Provider: "outscale",
		Name:     "test-account3",
		Region:   "eu-west-2",
		Status:   CloudAccountStatusActive,
	}
	cloudFake.mu.Unlock()

	store := newMemStore()
	caller := &auth.Caller{
		Kind:                auth.CallerKindToken,
		TokenID:             uuid.New(),
		Scopes:              []string{auth.ScopeVMCollector},
		BoundCloudAccountID: &accID,
	}
	h := buildCloudMux(t, store, nil, caller)

	// Deliberately malformed SG JSON inside a valid schema_version=1 envelope.
	// The handler should log WARN and still return 200 for the VM.
	body := map[string]any{
		"cloud_account_id": accID.String(),
		"provider_vm_id":   "i-malformed-sg-vm",
		"name":             "malformed-vm",
		"power_state":      "running",
		"security_groups":  json.RawMessage(`{"schema_version":1,"groups":"not-an-array"}`),
	}

	rr := doReq(t, h, http.MethodPost, "/v1/virtual-machines", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 even with bad SG payload, got %d: %s", rr.Code, rr.Body.String())
	}
}
