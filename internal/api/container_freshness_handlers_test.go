//nolint:noctx // httptest.NewRequest carries no request context in these unit tests
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// seedFreshnessStore inserts a cluster, namespace, workload (with one container
// far behind latest), and an image-version record so BuildContainerFreshness
// returns an enriched row with freshness=far_behind.
func seedFreshnessStore(t *testing.T, ms *memStore) {
	t.Helper()
	ctx := context.Background()

	cluster, _, err := ms.EnsureCluster(ctx, ClusterCreate{Name: "prod"})
	if err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	ns, err := ms.CreateNamespace(ctx, NamespaceCreate{ClusterId: *cluster.Id, Name: "default"})
	if err != nil {
		t.Fatalf("seed namespace: %v", err)
	}
	containers := ContainerList{{testFieldName: netpolTestWebLabelValue, csvColImage: testNginxImage}}
	if _, err := ms.CreateWorkload(ctx, WorkloadCreate{
		NamespaceId: *ns.Id,
		Kind:        Deployment,
		Name:        netpolTestWebLabelValue,
		Containers:  &containers,
	}); err != nil {
		t.Fatalf("seed workload: %v", err)
	}
	latest := tagNginxLatest // "1.27.4" — far ahead of "1.25.3"
	if _, err := ms.UpsertImageVersion(ctx, ImageVersionUpsert{
		ImageRepo:     testNginxRepo,
		Variant:       "",
		Registry:      testDockerRegistry,
		LatestTag:     &latest,
		Annotation:    json.RawMessage(`{}`),
		Source:        string(Registry),
		LastCheckedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert image version: %v", err)
	}
}

func TestHandleListContainerFreshness_FarBehindFilter(t *testing.T) {
	ms := newMemStore()
	seedFreshnessStore(t, ms)

	req := httptest.NewRequest(http.MethodGet, "/v1/container-freshness?freshness=far_behind", http.NoBody)
	req = req.WithContext(auth.WithCaller(req.Context(), readCaller()))
	rr := httptest.NewRecorder()
	HandleListContainerFreshness(ms).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Items   []ContainerFreshnessRow   `json:"items"`
		Summary ContainerFreshnessSummary `json:"summary"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("want 1 item, got %d: %+v", len(resp.Items), resp.Items)
	}
	if resp.Items[0].Freshness != ContainerVersionInfoFreshnessFarBehind {
		t.Errorf("item freshness: want far_behind, got %q", resp.Items[0].Freshness)
	}
	if resp.Summary.FarBehind != 1 {
		t.Errorf("summary.far_behind: want 1, got %d", resp.Summary.FarBehind)
	}
}

func TestHandleListContainerFreshness_InvalidFreshness(t *testing.T) {
	ms := newMemStore()

	req := httptest.NewRequest(http.MethodGet, "/v1/container-freshness?freshness=bogus", http.NoBody)
	req = req.WithContext(auth.WithCaller(req.Context(), readCaller()))
	rr := httptest.NewRecorder()
	HandleListContainerFreshness(ms).ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d; want 400", rr.Code)
	}
}

func TestHandleListContainerFreshness_MissingScope(t *testing.T) {
	ms := newMemStore()

	// No caller in context → requireScope returns 403.
	req := httptest.NewRequest(http.MethodGet, "/v1/container-freshness", http.NoBody)
	rr := httptest.NewRecorder()
	HandleListContainerFreshness(ms).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status %d; want 403", rr.Code)
	}
}

// callerWithoutRead returns a caller that has no scopes (write-only test).
func callerWithoutRead() *auth.Caller {
	return &auth.Caller{Role: auth.RoleViewer}
}

func TestHandleListContainerFreshness_WrongScope(t *testing.T) {
	ms := newMemStore()

	req := httptest.NewRequest(http.MethodGet, "/v1/container-freshness", http.NoBody)
	req = req.WithContext(auth.WithCaller(req.Context(), callerWithoutRead()))
	rr := httptest.NewRecorder()
	HandleListContainerFreshness(ms).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status %d; want 403", rr.Code)
	}
}
