package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// osImagesResponseKey is the JSON key for the OS-image list in the response body.
const osImagesResponseKey = "images"

// HandleListOSImages — read scope. GET /v1/os-images. Returns the
// deduplicated inventory of OS images in service (cloud VMs ∪ cluster
// nodes), keyed by image name (ADR-0040). Vendor-neutral CMDB inventory.
func HandleListOSImages(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeRead) {
			return
		}
		images, err := store.ListOSImages(r.Context())
		if err != nil {
			slog.Error("list os images", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			osImagesResponseKey: images,
			"generated_at":      time.Now().UTC().Format(time.RFC3339),
		})
	}
}
