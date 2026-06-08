package api

// Hand-written CRUD + YAML import/export handlers for the cluster reference
// flow matrix (ADR-0036, R2 Task 8). Routes are wired in a later task; this
// file only defines the http.HandlerFunc factories and the YAML document
// shape. Import is replace-all (OQ-1): the posted document fully supersedes
// the cluster's existing reference rows. Validation reuses
// FlowReferenceInput.Validate (perimeter ⇒ exactly one endpoint_group side;
// internal ⇒ none; justification required), surfaced as RFC 7807 409s.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// importBodyLimit caps the YAML import payload to keep a single request from
// pinning memory; the matrix is operator-curated and small in practice.
const importBodyLimit = 1 << 20 // 1 MiB

// flowRefYAMLDoc is the import/export wire shape: a top-level `references`
// list of plain FlowReferenceInput rows (json+yaml tagged on the type).
type flowRefYAMLDoc struct {
	References []FlowReferenceInput `yaml:"references"`
}

// callerUserID returns the current caller's user id as a *uuid.UUID suitable
// for the flow reference `created_by` column. Both human sessions and machine
// tokens populate Caller.UserID (tokens carry their creator's id); a missing
// or zero id yields nil so the column is left NULL rather than a zero UUID.
func callerUserID(r *http.Request) *uuid.UUID {
	c := auth.CallerFromContext(r.Context())
	if c == nil || c.UserID == uuid.Nil {
		return nil
	}
	id := c.UserID
	return &id
}

// writeFlowRefStoreError maps store sentinels to RFC 7807 responses. There is
// no package-wide store-error helper, so the handlers that can hit a
// not-found/conflict path inline this mapping (matching the per-handler
// errors.Is style used elsewhere in this package).
func writeFlowRefStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeProblem(w, http.StatusNotFound, "flow reference not found", err.Error())
	case errors.Is(err, ErrConflict):
		writeProblem(w, http.StatusConflict, "invalid flow reference", err.Error())
	default:
		writeProblem(w, http.StatusInternalServerError, "flow reference store error", err.Error())
	}
}

// HandleListFlowReferences — read scope. GET /v1/clusters/{id}/flow-references.
func HandleListFlowReferences(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clusterID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid cluster id", err.Error())
			return
		}
		refs, err := store.ListFlowReferences(r.Context(), clusterID)
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "list flow references", err.Error())
			return
		}
		if refs == nil {
			refs = []FlowReference{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": refs})
	}
}

// HandleCreateFlowReference — editor scope. POST /v1/clusters/{id}/flow-references.
func HandleCreateFlowReference(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clusterID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid cluster id", err.Error())
			return
		}
		var in FlowReferenceInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid body", err.Error())
			return
		}
		if err := in.Validate(); err != nil {
			writeProblem(w, http.StatusConflict, "invalid flow reference", err.Error())
			return
		}
		ref, err := store.CreateFlowReference(r.Context(), clusterID, in, callerUserID(r))
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "create flow reference", err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, ref)
	}
}

// HandleUpdateFlowReference — editor scope. PATCH /v1/flow-references/{id}.
func HandleUpdateFlowReference(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid id", err.Error())
			return
		}
		var in FlowReferenceInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid body", err.Error())
			return
		}
		if err := in.Validate(); err != nil {
			writeProblem(w, http.StatusConflict, "invalid flow reference", err.Error())
			return
		}
		ref, err := store.UpdateFlowReference(r.Context(), id, in)
		if err != nil {
			writeFlowRefStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, ref)
	}
}

// HandleDeleteFlowReference — editor scope. DELETE /v1/flow-references/{id}.
func HandleDeleteFlowReference(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid id", err.Error())
			return
		}
		if err := store.DeleteFlowReference(r.Context(), id); err != nil {
			writeFlowRefStoreError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// HandleExportFlowReferences — read scope. GET /v1/clusters/{id}/flow-references/export.
// Emits a text/yaml document of the cluster's reference rows, suitable for
// version control and round-tripping through the import endpoint.
func HandleExportFlowReferences(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clusterID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid cluster id", err.Error())
			return
		}
		refs, err := store.ListFlowReferences(r.Context(), clusterID)
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "list flow references", err.Error())
			return
		}
		doc := flowRefYAMLDoc{}
		for i := range refs {
			ref := &refs[i]
			doc.References = append(doc.References, FlowReferenceInput{
				Layer:         ref.Layer,
				Direction:     ref.Direction,
				SrcKind:       ref.SrcKind,
				SrcRef:        ref.SrcRef,
				DstKind:       ref.DstKind,
				DstRef:        ref.DstRef,
				Protocol:      ref.Protocol,
				FromPort:      ref.FromPort,
				ToPort:        ref.ToPort,
				Justification: ref.Justification,
			})
		}
		out, err := yaml.Marshal(doc)
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "marshal yaml", err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/yaml")
		_, _ = w.Write(out)
	}
}

// HandleImportFlowReferences — editor scope. POST /v1/clusters/{id}/flow-references/import.
// Replace-all (OQ-1): the posted YAML document fully supersedes the cluster's
// existing reference rows. Every entry is validated before any write; a single
// invalid row fails the whole import with a 409 naming the offending index.
func HandleImportFlowReferences(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clusterID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid cluster id", err.Error())
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, importBodyLimit))
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "read body", err.Error())
			return
		}
		var doc flowRefYAMLDoc
		if err := yaml.Unmarshal(body, &doc); err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid yaml", err.Error())
			return
		}
		for i := range doc.References {
			if err := doc.References[i].Validate(); err != nil {
				writeProblem(w, http.StatusConflict, "invalid flow reference",
					"row "+strconv.Itoa(i)+": "+err.Error())
				return
			}
		}
		refs, err := store.ReplaceFlowReferences(r.Context(), clusterID, doc.References, callerUserID(r))
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "import flow references", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": refs})
	}
}
