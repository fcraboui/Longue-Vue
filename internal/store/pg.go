// Package store provides the PostgreSQL implementation of api.Store.
//
// This file holds the PG core (pool, migrations) plus helpers shared
// across entities (cursor codec, scan/marshal utilities, error
// classification). Entity CRUD lives in per-entity pg_*.go files
// (pg_clusters.go, pg_nodes.go, pg_namespaces.go, pg_pods.go,
// pg_workloads.go, pg_services.go, pg_ingresses.go,
// pg_persistent_volumes.go, pg_persistent_volume_claims.go,
// pg_settings.go) alongside the pre-existing domain files
// (pg_auth.go, pg_applications.go, pg_cloud_accounts.go, …).
package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/secrets"
	"github.com/sthalbert/longue-vue/migrations"
)

// errCursorFormatInvalid is returned when a pagination cursor cannot be decoded.
var errCursorFormatInvalid = errors.New("cursor format invalid")

// change type constants for time-travel history capture.
const (
	changeTypeCreate     = "create"
	changeTypeUpdate     = "update"
	changeTypeRestore    = "restore"
	changeTypeSoftDelete = "soft_delete"
)

// PG is a PostgreSQL-backed implementation of api.Store.
type PG struct {
	pool          *pgxpool.Pool
	revokedTokens chan string
	encrypter     *secrets.Encrypter
}

// SetEncrypter wires the AES-GCM encrypter used by encrypted-secret tables
// (currently image_versions_registries auth tokens). Optional — methods
// that require it return an explicit error when it is unset.
func (p *PG) SetEncrypter(enc *secrets.Encrypter) {
	p.encrypter = enc
}

// Encrypter returns the wired encrypter, or nil. Test-only helper.
func (p *PG) Encrypter() *secrets.Encrypter { return p.encrypter }

// Pool returns the underlying pool. Test-only helper.
func (p *PG) Pool() *pgxpool.Pool { return p.pool }

// Open connects to PostgreSQL via the given DSN and verifies the connection.
func Open(ctx context.Context, dsn string) (*PG, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.New: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &PG{pool: pool, revokedTokens: make(chan string, 64)}, nil
}

// RevocationChan returns a channel that emits the prefix of each token that is
// revoked via RevokeAPIToken. The channel is buffered (cap 64); sends are
// non-blocking so a slow consumer never stalls token revocation.
func (p *PG) RevocationChan() <-chan string {
	return p.revokedTokens
}

// Close releases the connection pool.
func (p *PG) Close() {
	p.pool.Close()
}

// Ping checks the database is reachable.
func (p *PG) Ping(ctx context.Context) error {
	if err := p.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	return nil
}

// Migrate applies every pending migration embedded in the migrations package.
func (p *PG) Migrate(ctx context.Context) error {
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("goose dialect: %w", err)
	}

	db := stdlib.OpenDBFromPool(p.pool)
	defer func() { _ = db.Close() }()

	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}

// withTx runs fn inside a transaction: rollback on error or panic,
// commit when fn returns nil. op names the operation in the begin/commit
// error wrapping (e.g. "update cluster").
func (p *PG) withTx(ctx context.Context, op string, fn func(tx pgx.Tx) error) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin %s: %w", op, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s: %w", op, err)
	}
	return nil
}

// classifyOutcome maps the CTE's (inserted, business_changed) tuple to the
// corresponding api.UpsertOutcome — used by every Upsert* impl that computes
// audit-noop detection. See ADR-0024.
func classifyOutcome(inserted, businessChanged bool) api.UpsertOutcome {
	switch {
	case inserted:
		return api.OutcomeInserted
	case businessChanged:
		return api.OutcomeBusinessChanged
	default:
		return api.OutcomeNoChange
	}
}

// scanRowWith wraps a pgx.Row so a scan* helper can be reused while the
// caller threads extra destinations (e.g. the audit CTE's inserted /
// business_changed bool tail) onto the same row.Scan call.
type scanRowWith struct {
	row   pgx.Row
	extra []any
}

// Scan forwards to the wrapped row, appending the extra destinations after
// the caller's so a single Scan call can hydrate both the normal columns
// and the CTE outcome bools.
func (r scanRowWith) Scan(dest ...any) error {
	if err := r.row.Scan(append(dest, r.extra...)...); err != nil {
		return fmt.Errorf("scan row: %w", err)
	}
	return nil
}

func scanUUIDs(rows pgx.Rows) ([]uuid.UUID, error) {
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan uuid: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate uuids: %w", err)
	}
	return ids, nil
}

func marshalPorts(ports *[]map[string]interface{}) ([]byte, error) {
	if ports == nil {
		return []byte("[]"), nil
	}
	b, err := json.Marshal(*ports)
	if err != nil {
		return nil, fmt.Errorf("marshal ports: %w", err)
	}
	return b, nil
}

// unmarshalContainers decodes a JSONB array into the shared ContainerList
// type. Returns nil when the column is empty or contains an empty array.
//
//nolint:nilnil // nil, nil is the intentional "no data" signal for optional JSONB columns
func unmarshalContainers(b []byte) (*api.ContainerList, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var cs api.ContainerList
	if err := json.Unmarshal(b, &cs); err != nil {
		return nil, fmt.Errorf("unmarshal containers: %w", err)
	}
	if len(cs) == 0 {
		return nil, nil
	}
	return &cs, nil
}

// unmarshalMapArray decodes a JSONB array of objects. Returns nil for
// empty arrays so the pointer semantics match what callers expect
// (nil = absent, &[...] = present).
//
//nolint:nilnil // nil, nil is the intentional "no data" signal for optional JSONB columns
func unmarshalMapArray(b []byte) (*[]map[string]interface{}, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var out []map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("unmarshal map array: %w", err)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out, nil
}

// marshalLabels encodes the optional labels map as JSON, preserving NULL-vs-empty semantics.
func marshalLabels(labels *map[string]string) ([]byte, error) { //nolint:gocritic // ptrToRefParam: callers pass *map from generated API types
	if labels == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(*labels) //nolint:errchkjson // map[string]string is unconditionally JSON-safe
	if err != nil {
		return nil, fmt.Errorf("marshal labels: %w", err)
	}
	return b, nil
}

func nullableString(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}

func encodeCursor(t time.Time, id uuid.UUID) string {
	raw := t.UTC().Format(time.RFC3339Nano) + "|" + id.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(c string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("decode cursor: %w", err)
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, errCursorFormatInvalid
	}
	ts, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("cursor timestamp: %w", err)
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("cursor id: %w", err)
	}
	return ts, id, nil
}

// listCursor is the tagged, versioned pagination cursor (ADR-0039).
// It replaces the positional "<RFC3339Nano>|<uuid>" format: it names
// the sort column and direction it was minted under, so a cursor
// replayed with different sort parameters is rejected instead of
// silently mis-paginating. Val is nil while paginating inside the
// NULLS LAST region of a nullable sort column.
type listCursor struct {
	V   int       `json:"v"`
	Col string    `json:"col"`
	Val *string   `json:"val"`
	ID  uuid.UUID `json:"id"`
	Dir string    `json:"dir"`
}

// encodeListCursor mints the opaque cursor for the row whose sort-column
// value is val (already serialized: lowercased text, or RFC3339Nano time).
func encodeListCursor(col string, val *string, id uuid.UUID, dir string) string {
	raw, _ := json.Marshal(listCursor{V: 1, Col: col, Val: val, ID: id, Dir: dir})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeListCursor validates shape, version, and that the cursor was
// minted under the same resolved (col, dir) as the current request.
// All failures map to api.ErrInvalidCursor so handlers can return 400.
func decodeListCursor(cursor, wantCol, wantDir string) (*string, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, uuid.Nil, fmt.Errorf("%w: %v", api.ErrInvalidCursor, err)
	}
	var c listCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, uuid.Nil, fmt.Errorf("%w: %v", api.ErrInvalidCursor, err)
	}
	if c.V != 1 || c.ID == uuid.Nil {
		return nil, uuid.Nil, api.ErrInvalidCursor
	}
	if c.Col != wantCol || c.Dir != wantDir {
		return nil, uuid.Nil, fmt.Errorf("%w: cursor sort mismatch", api.ErrInvalidCursor)
	}
	return c.Val, c.ID, nil
}

// isUniqueViolation reports whether err is a PostgreSQL unique-constraint
// violation (SQLSTATE 23505). Insert/update paths use it to map the error
// to api.ErrConflict with an entity-specific message.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// escapeLike escapes LIKE/ILIKE metacharacters in s (backslash, percent,
// underscore) so callers can safely build `%<user-input>%` patterns. The
// surrounding SQL must use `ESCAPE '\'` to honor the backslash escape.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// namePattern builds the LIKE pattern for the uniform `name=` filter
// (spec 2026-07-10). The term is lowercased and LIKE-escaped; each `*`
// then becomes a `%` wildcard. A term without `*` is wrapped in `%…%`
// (substring semantics); a term with `*` is used as an anchored glob
// (`du*` = starts-with, `*du` = ends-with). The surrounding SQL must
// compare against LOWER(col) and carry `ESCAPE '\'`.
func namePattern(term string) string {
	escaped := escapeLike(strings.ToLower(term))
	if !strings.Contains(term, "*") {
		return "%" + escaped + "%"
	}
	return strings.ReplaceAll(escaped, "*", "%")
}
