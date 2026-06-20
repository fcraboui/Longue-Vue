package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/sthalbert/longue-vue/internal/auth"
)

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
			"images":       images, //nolint:goconst // shared key name; a named constant is over-engineering for a single-handler response shape
			"generated_at": time.Now().UTC().Format(time.RFC3339),
		})
	}
}
