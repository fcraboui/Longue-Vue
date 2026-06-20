package vmcollector

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/vmcollector/apiclient"
	"github.com/sthalbert/longue-vue/internal/vmcollector/provider"
)

// fakeStore implements CollectorStore for collector tests.
type fakeStore struct {
	mu               sync.Mutex
	credsByName      map[string]apiclient.Credentials
	registered       map[string]uuid.UUID
	upsertedVMs      []provider.VM
	reconcileCalls   []reconcileCall
	statusUpdates    []statusUpdate
	upsertConflicts  map[string]bool // provider_vm_id -> ErrAlreadyKubeNode
	registerCallback func(name string, id uuid.UUID)
	sweptAttachments []apiclient.SGAttachment
	sweptSeenIDs     []string
	backedupNodeImages []apiclient.NodeImageMapping
}

type reconcileCall struct {
	accountID uuid.UUID
	keep      []string
}

type statusUpdate struct {
	accountID  uuid.UUID
	status     string
	lastSeenAt *time.Time
	lastErr    *string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		credsByName:     make(map[string]apiclient.Credentials),
		registered:      make(map[string]uuid.UUID),
		upsertConflicts: make(map[string]bool),
	}
}

func (f *fakeStore) FetchCredentialsByName(_ context.Context, name string) (apiclient.Credentials, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.credsByName[name]
	if !ok {
		return apiclient.Credentials{}, apiclient.ErrNotRegistered
	}
	return c, nil
}

func (f *fakeStore) RegisterCloudAccount(_ context.Context, _, name, region string) (apiclient.CloudAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.registered[name]
	if !ok {
		id = uuid.New()
		f.registered[name] = id
		if f.registerCallback != nil {
			f.registerCallback(name, id)
		}
	}
	return apiclient.CloudAccount{
		ID: id, Provider: "outscale", Name: name, Region: region,
		Status: "pending_credentials",
	}, nil
}

func (f *fakeStore) UpdateCloudAccountStatus(_ context.Context, id uuid.UUID, status string, lastSeenAt *time.Time, lastErr *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusUpdates = append(f.statusUpdates, statusUpdate{accountID: id, status: status, lastSeenAt: lastSeenAt, lastErr: lastErr})
	return nil
}

//nolint:gocritic // hugeParam: VM struct is fine here
func (f *fakeStore) UpsertVirtualMachine(_ context.Context, _ uuid.UUID, vm provider.VM) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upsertConflicts[vm.ProviderVMID] {
		return apiclient.ErrAlreadyKubeNode
	}
	f.upsertedVMs = append(f.upsertedVMs, vm)
	return nil
}

func (f *fakeStore) ReconcileVirtualMachines(_ context.Context, accountID uuid.UUID, keep []string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reconcileCalls = append(f.reconcileCalls, reconcileCall{accountID: accountID, keep: append([]string(nil), keep...)})
	return 0, nil
}

func (f *fakeStore) SweepSecurityGroups(
	_ context.Context,
	_ uuid.UUID,
	seenIDs []string,
	_ []provider.SecurityGroup,
	attachments []apiclient.SGAttachment,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sweptSeenIDs = append(f.sweptSeenIDs, seenIDs...)
	f.sweptAttachments = append(f.sweptAttachments, attachments...)
	return nil
}

// sawSeenID reports whether the given SG id was included in the sweep's
// seen-set (and therefore preserved from the server's delete-unseen pass).
func (f *fakeStore) sawSeenID(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sweptSeenIDs {
		if s == id {
			return true
		}
	}
	return false
}

// sawAttachment reports whether the sweep received an attachment binding
// the given VM to the given SG.
func (f *fakeStore) sawAttachment(vmID, sgID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.sweptAttachments {
		if a.ProviderVMID != vmID {
			continue
		}
		for _, id := range a.ProviderSGIDs {
			if id == sgID {
				return true
			}
		}
	}
	return false
}

func (f *fakeStore) BackfillNodeImages(_ context.Context, _ uuid.UUID, images []apiclient.NodeImageMapping) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.backedupNodeImages = append(f.backedupNodeImages, images...)
	return nil
}

// sawVMUpsert reports whether the given VM was upserted into inventory.
func (f *fakeStore) sawVMUpsert(vmID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.upsertedVMs {
		if f.upsertedVMs[i].ProviderVMID == vmID {
			return true
		}
	}
	return false
}

func TestCollectorAwaitsCredentials(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	fakeProv := &provider.Fake{}
	calls := atomic.Int32{}
	factory := func(_ apiclient.Credentials) (provider.Provider, error) {
		calls.Add(1)
		return fakeProv, nil
	}
	c := New(Config{
		Provider:    "outscale",
		AccountName: "x",
		Region:      "eu-west-2",
		Interval:    1 * time.Hour,
	}, store, factory)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.runOnce(ctx)
	// No creds yet → no provider built, no upserts.
	if calls.Load() != 0 {
		t.Errorf("factory calls = %d", calls.Load())
	}
	store.mu.Lock()
	registered := len(store.registered)
	store.mu.Unlock()
	if registered != 1 {
		t.Errorf("registered = %d", registered)
	}
}

func TestCollectorTickEndToEnd(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.credsByName["x"] = apiclient.Credentials{
		AccessKey: "ak", SecretKey: "sk", Region: "eu-west-2", Provider: "outscale",
	}
	fakeProv := &provider.Fake{
		VMs: []provider.VM{
			{ProviderVMID: "i-1", Name: "vault", PowerState: "running"},
			{ProviderVMID: "i-2", Name: "dns", PowerState: "running"},
			// Filtered out — kube node tag.
			{
				ProviderVMID: "i-3", Name: "kube-w1", PowerState: "running",
				Tags: map[string]string{"OscK8sNodeName": "kube-w1"},
			},
		},
	}
	c := New(Config{
		Provider:    "outscale",
		AccountName: "x",
		Region:      "eu-west-2",
		Interval:    1 * time.Hour,
		Reconcile:   true,
	}, store, func(_ apiclient.Credentials) (provider.Provider, error) {
		return fakeProv, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.runOnce(ctx)

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.upsertedVMs) != 2 {
		t.Errorf("upserted = %d, want 2", len(store.upsertedVMs))
	}
	if len(store.reconcileCalls) != 1 {
		t.Fatalf("reconcile calls = %d", len(store.reconcileCalls))
	}
	if got := store.reconcileCalls[0].keep; len(got) != 2 {
		t.Errorf("keep = %v, want 2 entries", got)
	}
	// Last status update should be active with last_seen_at.
	if len(store.statusUpdates) == 0 {
		t.Fatalf("no status updates")
	}
	last := store.statusUpdates[len(store.statusUpdates)-1]
	if last.status != "active" || last.lastSeenAt == nil {
		t.Errorf("last status update = %+v", last)
	}
}

func TestTick_EmitsAttachmentsForFilteredNodeVMs(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.credsByName["x"] = apiclient.Credentials{
		AccessKey: "ak", SecretKey: "sk", Region: "eu-west-2", Provider: "outscale",
	}
	fakeProv := &provider.Fake{
		VMs: []provider.VM{
			// Kube node: dropped by filter.Apply via OscK8sNodeName tag, but
			// still carries the cluster perimeter SG.
			{
				ProviderVMID: "i-node1", PowerState: "running",
				Tags:           map[string]string{"OscK8sNodeName": "i-node1"},
				SecurityGroups: provider.VMSecurityGroupsPayload{Version: 1, Groups: []provider.SecurityGroup{{ProviderSGID: "sg-perimeter"}}},
			},
			{
				ProviderVMID: "i-app1", PowerState: "running",
				SecurityGroups: provider.VMSecurityGroupsPayload{Version: 1, Groups: []provider.SecurityGroup{{ProviderSGID: "sg-app"}}},
			},
		},
		SGs: []provider.SecurityGroup{{ProviderSGID: "sg-perimeter", Name: "nodes"}, {ProviderSGID: "sg-app", Name: "app"}},
	}
	c := New(Config{Provider: "outscale", AccountName: "x", Region: "eu-west-2", Interval: 1 * time.Hour, Reconcile: true},
		store, func(_ apiclient.Credentials) (provider.Provider, error) { return fakeProv, nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.runOnce(ctx)

	if !fakeProv.GetSecurityGroupsCalled {
		t.Fatalf("expected GetSecurityGroups called")
	}
	if !store.sawAttachment("i-node1", "sg-perimeter") {
		t.Fatalf("missing attachment for filtered node VM")
	}
	if store.sawVMUpsert("i-node1") {
		t.Fatalf("node VM must not be inventoried")
	}
}

// TestTick_EnumerationFailure_PreservesNodePerimeterSG asserts that when
// account-wide SG enumeration fails, the sweep seen-set still includes the
// perimeter SG attached only to a filtered-out node VM — otherwise the
// server's always-on delete-unseen sweep would purge the cluster perimeter.
func TestTick_EnumerationFailure_PreservesNodePerimeterSG(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.credsByName["x"] = apiclient.Credentials{
		AccessKey: "ak", SecretKey: "sk", Region: "eu-west-2", Provider: "outscale",
	}
	fakeProv := &provider.Fake{
		VMs: []provider.VM{
			// Kube node: dropped by filter.Apply, carries the perimeter SG only.
			{
				ProviderVMID: "i-node1", PowerState: "running",
				Tags:           map[string]string{"OscK8sNodeName": "i-node1"},
				SecurityGroups: provider.VMSecurityGroupsPayload{Version: 1, Groups: []provider.SecurityGroup{{ProviderSGID: "sg-perimeter"}}},
			},
			{
				ProviderVMID: "i-app1", PowerState: "running",
				SecurityGroups: provider.VMSecurityGroupsPayload{Version: 1, Groups: []provider.SecurityGroup{{ProviderSGID: "sg-app"}}},
			},
		},
		SGErr: errors.New("enumeration outage"),
	}
	c := New(Config{Provider: "outscale", AccountName: "x", Region: "eu-west-2", Interval: 1 * time.Hour, Reconcile: true},
		store, func(_ apiclient.Credentials) (provider.Provider, error) { return fakeProv, nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.runOnce(ctx) // must not panic/abort despite the enumeration failure

	if !fakeProv.GetSecurityGroupsCalled {
		t.Fatalf("expected GetSecurityGroups called")
	}
	if !store.sawSeenID("sg-perimeter") {
		t.Fatalf("node perimeter SG must survive enumeration failure in the sweep seen-set")
	}
	if !store.sawSeenID("sg-app") {
		t.Fatalf("app SG must be present in the sweep seen-set")
	}
}

func TestCollectorSkipsKubeNodeOnConflict(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.credsByName["x"] = apiclient.Credentials{
		AccessKey: "ak", SecretKey: "sk", Region: "eu-west-2", Provider: "outscale",
	}
	store.upsertConflicts["i-kube"] = true
	fakeProv := &provider.Fake{
		VMs: []provider.VM{
			{ProviderVMID: "i-vault", Name: "vault", PowerState: "running"},
			{ProviderVMID: "i-kube", Name: "kube", PowerState: "running"},
		},
	}
	c := New(Config{Provider: "outscale", AccountName: "x", Region: "eu-west-2", Interval: 1 * time.Hour, Reconcile: true},
		store, func(_ apiclient.Credentials) (provider.Provider, error) { return fakeProv, nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.runOnce(ctx)

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.upsertedVMs) != 1 {
		t.Errorf("upserted = %d, want 1", len(store.upsertedVMs))
	}
	// keep list excludes the kube-node provider id (it was rejected).
	if len(store.reconcileCalls) != 1 || len(store.reconcileCalls[0].keep) != 1 {
		t.Errorf("reconcile = %+v", store.reconcileCalls)
	}
}

func TestCollectorReportsErrorOnProviderFailure(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.credsByName["x"] = apiclient.Credentials{
		AccessKey: "ak", SecretKey: "sk", Region: "eu-west-2", Provider: "outscale",
	}
	listErr := errors.New("boom")
	fakeProv := &provider.Fake{ListErr: listErr}
	c := New(Config{Provider: "outscale", AccountName: "x", Region: "eu-west-2", Interval: 1 * time.Hour},
		store, func(_ apiclient.Credentials) (provider.Provider, error) { return fakeProv, nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.runOnce(ctx)

	store.mu.Lock()
	defer store.mu.Unlock()
	last := store.statusUpdates[len(store.statusUpdates)-1]
	if last.status != "error" || last.lastErr == nil {
		t.Errorf("expected error status, got %+v", last)
	}
}

func TestBuildNodeImageMappings(t *testing.T) {
	t.Parallel()
	vms := []provider.VM{
		{ProviderVMID: "i-1", ImageID: "ami-1", ImageName: "img-a"},
		{ProviderVMID: "i-2", ImageID: "ami-2", ImageName: ""}, // dropped: no image name
		{ProviderVMID: "i-3", ImageID: "ami-3", ImageName: "img-c"},
	}
	got := buildNodeImageMappings(vms)
	if len(got) != 2 {
		t.Fatalf("got %d mappings; want 2 (%+v)", len(got), got)
	}
	if got[0].ProviderVMID != "i-1" || got[0].ImageName != "img-a" {
		t.Fatalf("unexpected mapping[0]: %+v", got[0])
	}
	if got[1].ProviderVMID != "i-3" {
		t.Fatalf("unexpected mapping[1]: %+v", got[1])
	}
}
