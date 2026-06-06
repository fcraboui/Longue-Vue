package api

// Handler for POST /v1/ingest/cloud-accounts/{id}/security-groups/sweep.
// Wired into the cloud-account mux alongside the other vm-collector endpoints.
// This endpoint is intentionally NOT on the generated ServerInterface — it is
// hand-written (flow-matrix P1, Task 14) and mounted directly on the mux.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// sweepSGsRequest is the body for POST /v1/ingest/cloud-accounts/{id}/security-groups/sweep.
//
// SeenProviderSGIDs is the legacy field every collector still sends; the server
// always deletes account SGs not in this list. Groups and Attachments are the
// flow-matrix enrichment payload — the full account-wide canonical SG set and
// the per-VM SG attachment tuples — persisted only when flow_matrix_enabled is
// on (see persistAccountSGEnrichment). Older collectors omit them.
type sweepSGsRequest struct {
	SeenProviderSGIDs []string                `json:"seen_provider_sg_ids"`
	Groups            json.RawMessage         `json:"groups,omitempty"` // canonical []sgWireGroup
	Attachments       []sgAttachmentWireEntry `json:"attachments,omitempty"`
}

// sgAttachmentWireEntry maps one VM to the set of SGs attached to it.
type sgAttachmentWireEntry struct {
	ProviderVMID  string   `json:"provider_vm_id"`
	ProviderSGIDs []string `json:"provider_sg_ids"`
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
			slog.Error("sweep security groups", slog.Any("cloud_account_id", id), slog.Any("err", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		// Flow-matrix enrichment (account-wide SG set + per-VM attachments) is
		// persisted only when the feature is enabled. When off, behave exactly
		// like the legacy sweep and ignore any groups/attachments on the body.
		if settings, err := store.GetSettings(r.Context()); err == nil && settings.FlowMatrixEnabled {
			persistAccountSGEnrichment(r.Context(), store, id, body)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// persistAccountSGEnrichment upserts the account-wide canonical SG set and the
// per-VM attachment tuples carried on a sweep request, then sweeps the
// attachment table to the set seen this tick. Best-effort: failures are logged,
// never 5xx (mirrors persistSGsFromVMIngest).
func persistAccountSGEnrichment(ctx context.Context, store Store, accountID uuid.UUID, body sweepSGsRequest) {
	if len(body.Groups) > 0 {
		var groups []sgWireGroup
		if err := json.Unmarshal(body.Groups, &groups); err != nil {
			slog.Warn("sg sweep: decode groups failed",
				slog.Any("cloud_account_id", accountID), slog.Any("error", err))
		} else {
			for _, g := range groups {
				if err := upsertCanonicalSG(ctx, store, accountID, g); err != nil {
					slog.Warn("sg sweep: upsert SG failed",
						slog.Any("cloud_account_id", accountID), slog.Any("error", err))
				}
			}
		}
	}
	seen := make([]VMSecurityGroupAttachment, 0, len(body.Attachments))
	for _, e := range body.Attachments {
		for _, sgID := range e.ProviderSGIDs {
			a := VMSecurityGroupAttachment{
				CloudAccountID: accountID,
				ProviderVMID:   e.ProviderVMID,
				ProviderSGID:   sgID,
			}
			if err := store.UpsertVMSecurityGroupAttachment(ctx, a); err != nil {
				slog.Warn("sg sweep: upsert attachment failed",
					slog.Any("cloud_account_id", accountID), slog.Any("error", err))
				continue
			}
			seen = append(seen, a)
		}
	}
	if err := store.SweepVMSecurityGroupAttachments(ctx, accountID, seen); err != nil {
		slog.Warn("sg sweep: attachment sweep failed",
			slog.Any("cloud_account_id", accountID), slog.Any("error", err))
	}
}
