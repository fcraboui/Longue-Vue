//nolint:noctx,goconst // httptest.NewRequest for brevity; literal strings in assertions are clearer than named constants
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sthalbert/longue-vue/internal/auth"
)

func TestHandleListOSImages_OK(t *testing.T) {
	resetOSImageFake()
	osImageFake.images = []OSImage{
		{ImageName: "img-a", ImageIDs: []string{"ami-1"}, VMCount: 2, NodeCount: 3},
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/os-images", http.NoBody)
	req = req.WithContext(auth.WithCaller(req.Context(), readCaller()))
	rr := httptest.NewRecorder()
	HandleListOSImages(newMemStore()).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Images []OSImage `json:"images"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Images) != 1 || resp.Images[0].ImageName != "img-a" || resp.Images[0].NodeCount != 3 {
		t.Fatalf("unexpected images: %+v", resp.Images)
	}
}

func TestHandleListOSImages_NoCallerForbidden(t *testing.T) {
	resetOSImageFake()
	req := httptest.NewRequest(http.MethodGet, "/v1/os-images", http.NoBody)
	rr := httptest.NewRecorder()
	HandleListOSImages(newMemStore()).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status %d; want 403", rr.Code)
	}
}
