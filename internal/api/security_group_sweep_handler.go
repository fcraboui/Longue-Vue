package api

// Handler for POST /v1/ingest/cloud-accounts/{id}/security-groups/sweep.
// Wired into the cloud-account mux alongside the other vm-collector endpoints.
// This endpoint is intentionally NOT on the generated ServerInterface — it is
// hand-written (flow-matrix P1, Task 14) and mounted directly on the mux.

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// sweepSGsRequest is the body for POST /v1/ingest/cloud-accounts/{id}/security-groups/sweep.
type sweepSGsRequest struct {
	SeenProviderSGIDs []string `json:"seen_provider_sg_ids"`
}

// HandleSweepSecurityGroups — vm-collector scope, bound to the cloud account.
// Deletes every security group in the account whose provider_sg_id is NOT in
// the request's seen_provider_sg_ids list. Called once per account refresh
// tick after all VM upserts are done.
//
// POST /v1/ingest/cloud-accounts/{id}/security-groups/sweep
// Response: 204 No Content on success.
func HandleSweepSecurityGroups(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		if caller == nil || !caller.HasScope(auth.ScopeVMCollector) {
			writeProblem(w, http.StatusForbidden, "Forbidden", "vm-collector scope required")
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		if err := caller.EnforceCloudAccountBinding(id); err != nil {
			writeProblem(w, http.StatusForbidden, "Forbidden",
				"token not bound to this cloud account")
			return
		}
		var body sweepSGsRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON body")
			return
		}
		if err := store.SweepSecurityGroupsByAccount(r.Context(), id, body.SeenProviderSGIDs); err != nil {
			slog.Error("sweep security groups", "cloud_account_id", id, "err", err)
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
