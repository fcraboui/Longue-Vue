package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/sthalbert/longue-vue/internal/auth"
	"github.com/sthalbert/longue-vue/internal/metrics"
)

// backfillNodeImagesRequest is the body for
// POST /v1/ingest/cloud-accounts/{id}/node-images.
type backfillNodeImagesRequest struct {
	Images []NodeImage `json:"images"`
}

// HandleBackfillNodeImages — vm-collector scope, bound to the cloud account.
// Backfills nodes.image_id/image_name from the reported node-VM mappings
// (ADR-0040). Vendor-neutral CMDB inventory; no outbound calls.
//
// POST /v1/ingest/cloud-accounts/{id}/node-images
// Response: 200 {"matched":N,"updated":M}.
func HandleBackfillNodeImages(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeVMCollector) {
			return
		}
		caller := auth.CallerFromContext(r.Context())
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		if err := caller.EnforceCloudAccountBinding(id); err != nil {
			writeProblem(w, http.StatusForbidden, "Forbidden", "token not bound to this cloud account")
			return
		}
		var body backfillNodeImagesRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON body")
			return
		}
		matched, updated, err := store.BackfillNodeImages(r.Context(), body.Images)
		if err != nil {
			slog.Error("backfill node images", slog.Any("cloud_account_id", id), slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		if matched > 0 {
			metrics.ObserveNodeImageBackfill("matched")
		} else {
			metrics.ObserveNodeImageBackfill("nomatch")
		}
		writeJSON(w, http.StatusOK, map[string]any{"matched": matched, "updated": updated})
	}
}
