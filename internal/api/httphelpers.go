package api

// Shared plumbing for the hand-written handlers (applications,
// application-blocks, cloud-accounts, virtual-machines, extracts, …):
// JSON/problem+json writers, scope guard, and common query/path parsing.
// The oapi-codegen handlers in server.go have their own equivalents
// generated for them and do not use these.

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// writeJSON writes an application/json response with the given status.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Warn("writeJSON: encode error", slog.Any("error", err))
	}
}

// writeProblem writes a minimal RFC 7807 application/problem+json response.
func writeProblem(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	body := map[string]any{"type": "about:blank", "title": title, "status": status}
	if detail != "" {
		body["detail"] = detail
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Warn("writeProblem: encode error", slog.Any("error", err))
	}
}

// requireScope is the shared scope guard for hand-written handlers.
// Returns false (and writes a 403) when the caller is missing or lacks
// the named scope.
func requireScope(w http.ResponseWriter, r *http.Request, scope string) bool {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil || !caller.HasScope(scope) {
		writeProblem(w, http.StatusForbidden, "Forbidden", scope+" scope required")
		return false
	}
	return true
}

// pathUUID extracts a UUID from a router path value. Returns (uuid, true)
// on success or writes a 400 and returns (_, false).
//
//nolint:unparam // name is kept for error message clarity; future routes may use different parameter names
func pathUUID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	raw := r.PathValue(name)
	if raw == "" {
		writeProblem(w, http.StatusBadRequest, "Bad Request", "missing path parameter "+name)
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid UUID")
		return uuid.Nil, false
	}
	return id, true
}

// parseLimit pulls an optional `limit` from query string. Falls back to
// the caller-supplied default when missing or unparseable. The store
// itself clamps to a hard upper bound (200).
func parseLimit(raw string, def int) int {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// parseListPage extracts the uniform limit/cursor/sort/order controls
// for hand-written list handlers. Validation of sort/order happens in
// the store (allowlist) — unknown values surface as ErrInvalidSort.
func parseListPage(r *http.Request, defLimit int) ListPage {
	q := r.URL.Query()
	return ListPage{
		Limit:  parseLimit(q.Get("limit"), defLimit),
		Cursor: q.Get("cursor"),
		Sort:   q.Get("sort"),
		Order:  q.Get("order"),
	}
}

// writeListError maps List* store errors: invalid cursor/sort → 400
// problem+json; anything else → logged 500 (matches the existing
// hand-written handler behavior).
func writeListError(w http.ResponseWriter, op string, err error) {
	if errors.Is(err, ErrInvalidCursor) || errors.Is(err, ErrInvalidSort) {
		writeProblem(w, http.StatusBadRequest, "Bad Request", err.Error())
		return
	}
	slog.Error(op, slog.Any("error", err))
	writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
}
