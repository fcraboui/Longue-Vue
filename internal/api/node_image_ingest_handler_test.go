package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

func postNodeImages(t *testing.T, store Store, caller *auth.Caller, accID uuid.UUID, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost,
		"/v1/ingest/cloud-accounts/"+accID.String()+"/node-images", bytes.NewReader(b))
	req.SetPathValue("id", accID.String())
	if caller != nil {
		req = req.WithContext(auth.WithCaller(req.Context(), caller))
	}
	rr := httptest.NewRecorder()
	HandleBackfillNodeImages(store).ServeHTTP(rr, req)
	return rr
}

func TestHandleBackfillNodeImages_OK(t *testing.T) {
	resetOSImageFake()
	osImageFake.matched = 2
	osImageFake.updated = 1
	accID := uuid.New()
	store := newMemStore()

	rr := postNodeImages(t, store, collectorCaller(&accID), accID, map[string]any{
		"images": []map[string]string{
			{"provider_vm_id": "i-1", "image_id": "ami-1", "image_name": "img-a"},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Matched int `json:"matched"`
		Updated int `json:"updated"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Matched != 2 || resp.Updated != 1 {
		t.Fatalf("matched=%d updated=%d; want 2,1", resp.Matched, resp.Updated)
	}
	if len(osImageFake.backfilled) != 1 || osImageFake.backfilled[0].ProviderVMID != "i-1" {
		t.Fatalf("store not called as expected: %+v", osImageFake.backfilled)
	}
}

func TestHandleBackfillNodeImages_WrongScope(t *testing.T) {
	resetOSImageFake()
	accID := uuid.New()
	rr := postNodeImages(t, newMemStore(), readCaller(), accID, map[string]any{"images": []any{}})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status %d; want 403", rr.Code)
	}
}

func TestHandleBackfillNodeImages_WrongAccountBinding(t *testing.T) {
	resetOSImageFake()
	boundID := uuid.New()
	otherID := uuid.New()
	rr := postNodeImages(t, newMemStore(), collectorCaller(&boundID), otherID, map[string]any{"images": []any{}})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status %d; want 403", rr.Code)
	}
}
