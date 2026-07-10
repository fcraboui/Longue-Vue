package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/auth"
)

// ── Users ─────────────────────────────────────────────────────────────────────

//nolint:gocyclo // test-only paging loop with dedup and order checks
func TestListUsersSortByUsername(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	// Seed users in non-alphabetical order.
	for _, name := range []string{"charlie", "alice", "eve", "bob", "diana"} {
		seedUser(t, pg, name+"-"+uuid.New().String()[:6], auth.RoleViewer)
	}

	// Collect all usernames across pages sorted by username asc.
	var got []string
	page := api.ListPage{Limit: 2, Sort: "username"}
	for {
		items, next, err := pg.ListUsers(ctx, api.UserListFilter{}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, u := range items {
			got = append(got, u.Username)
		}
		if next == "" {
			break
		}
		page.Cursor = next
	}
	if len(got) < 5 {
		t.Fatalf("got %d items, want at least 5", len(got))
	}
	// Verify ascending order.
	for i := 1; i < len(got); i++ {
		if got[i] < got[i-1] {
			t.Fatalf("not sorted asc at index %d: %q > %q", i, got[i-1], got[i])
		}
	}

	// Descending should flip order.
	items, _, err := pg.ListUsers(ctx, api.UserListFilter{}, api.ListPage{Limit: 50, Sort: "username", Order: "desc"})
	if err != nil {
		t.Fatalf("desc: %v", err)
	}
	for i := 1; i < len(items); i++ {
		if items[i].Username > items[i-1].Username {
			t.Fatalf("not sorted desc at index %d: %q < %q", i, items[i-1].Username, items[i].Username)
		}
	}
}

func TestListUsersNameFilter(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	suffix := uuid.New().String()[:6]
	seedUser(t, pg, "admin-"+suffix, auth.RoleAdmin)
	seedUser(t, pg, "editor-"+suffix, auth.RoleEditor)
	seedUser(t, pg, "viewer-"+suffix, auth.RoleViewer)
	seedUser(t, pg, "my_user-"+suffix, auth.RoleViewer)

	cases := []struct {
		term    string
		wantMin int
	}{
		{"admin", 1},       // substring
		{"*-" + suffix, 4}, // prefix glob
		{"my_user", 1},     // literal underscore (must not match myxuser)
		{"EDITOR", 1},      // case-insensitive
	}
	for _, tc := range cases {
		term := tc.term
		items, _, err := pg.ListUsers(ctx, api.UserListFilter{Name: &term}, api.ListPage{Limit: 50})
		if err != nil {
			t.Fatalf("%q: %v", tc.term, err)
		}
		if len(items) < tc.wantMin {
			t.Errorf("name=%q: got %d items, want at least %d", tc.term, len(items), tc.wantMin)
		}
	}
}

func TestListUsersRejectsBadSortAndMismatchedCursor(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	seedUser(t, pg, "u1-"+uuid.New().String()[:6], auth.RoleViewer)
	seedUser(t, pg, "u2-"+uuid.New().String()[:6], auth.RoleViewer)
	seedUser(t, pg, "u3-"+uuid.New().String()[:6], auth.RoleViewer)

	if _, _, err := pg.ListUsers(ctx, api.UserListFilter{}, api.ListPage{Sort: "bogus"}); !errors.Is(err, api.ErrInvalidSort) {
		t.Errorf("bogus sort: %v, want ErrInvalidSort", err)
	}

	_, next, err := pg.ListUsers(ctx, api.UserListFilter{}, api.ListPage{Limit: 1, Sort: "username"})
	if err != nil || next == "" {
		t.Fatalf("seed cursor: next=%q err=%v", next, err)
	}
	// Replay username cursor under created_at (default) → invalid.
	if _, _, err := pg.ListUsers(ctx, api.UserListFilter{}, api.ListPage{Limit: 1, Cursor: next}); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("mismatched cursor: %v, want ErrInvalidCursor", err)
	}
	// Legacy pipe cursor → invalid.
	legacy := encodeCursor(timeNowFixed(t), uuid.New())
	if _, _, err := pg.ListUsers(ctx, api.UserListFilter{}, api.ListPage{Cursor: legacy}); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("legacy cursor: %v, want ErrInvalidCursor", err)
	}
}

func TestListUsersSortTieBreakAcrossPages(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	suffix := uuid.New().String()[:6]
	// Seed 2 admins + 3 viewers so role has two tie groups.
	seedUser(t, pg, "adm1-"+suffix, auth.RoleAdmin)
	seedUser(t, pg, "adm2-"+suffix, auth.RoleAdmin)
	seedUser(t, pg, "vie1-"+suffix, auth.RoleViewer)
	seedUser(t, pg, "vie2-"+suffix, auth.RoleViewer)
	seedUser(t, pg, "vie3-"+suffix, auth.RoleViewer)

	seen := map[string]bool{}
	var roles []string
	page := api.ListPage{Limit: 2, Sort: "role"}
	for {
		items, next, err := pg.ListUsers(ctx, api.UserListFilter{}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for i := range items {
			u := &items[i]
			if seen[u.Id.String()] {
				t.Fatalf("user %s duplicated across pages (tiebreaker broken)", u.Id)
			}
			seen[u.Id.String()] = true
			roles = append(roles, string(u.Role))
		}
		if next == "" {
			break
		}
		page.Cursor = next
	}
	if len(roles) < 5 {
		t.Fatalf("total=%d want at least 5", len(roles))
	}
	// Within our seed set, admins sort before viewers (asc).
	var prevRole string
	for _, r := range roles {
		if prevRole != "" && r < prevRole {
			t.Fatalf("role order broken: %q after %q", r, prevRole)
		}
		prevRole = r
	}
}

func TestListUsersDefaultOrderUnchanged(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	suffix := uuid.New().String()[:6]
	for i := range 5 {
		seedUser(t, pg, "du-user-"+suffix+"-"+string(rune('a'+i)), auth.RoleViewer)
	}

	seen := map[string]bool{}
	page := api.ListPage{Limit: 2}
	total := 0
	for {
		items, next, err := pg.ListUsers(ctx, api.UserListFilter{}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, u := range items {
			if seen[u.Id.String()] {
				t.Fatalf("duplicate %s across pages", u.Id)
			}
			seen[u.Id.String()] = true
		}
		total += len(items)
		if next == "" {
			break
		}
		page.Cursor = next
	}
	if total < 5 {
		t.Fatalf("total=%d want at least 5", total)
	}
}

// ── API Tokens ────────────────────────────────────────────────────────────────

// seedTokenUser creates a user to own test tokens.
func seedTokenUser(t *testing.T, pg *PG) api.User {
	t.Helper()
	return seedUser(t, pg, "token-owner-"+uuid.New().String()[:8], auth.RoleAdmin)
}

// seedToken creates an API token owned by uid with the given name.
func seedToken(t *testing.T, pg *PG, uid uuid.UUID, name string) api.ApiToken {
	t.Helper()
	tok, err := pg.CreateAPIToken(context.Background(), api.APITokenInsert{
		ID:              uuid.New(),
		Name:            name,
		Prefix:          uuid.New().String()[:8],
		Hash:            "x",
		Scopes:          []string{"read"},
		CreatedByUserID: uid,
	})
	if err != nil {
		t.Fatalf("create token %q: %v", name, err)
	}
	return tok
}

//nolint:gocyclo // test-only paging loop with dedup and order checks
func TestListAPITokensSortByName(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	u := seedTokenUser(t, pg)
	for _, name := range []string{"charlie", "alice", "eve", "bob", "diana"} {
		seedToken(t, pg, *u.Id, name+"-"+uuid.New().String()[:6])
	}

	var got []string
	page := api.ListPage{Limit: 2, Sort: "name"}
	for {
		items, next, err := pg.ListAPITokens(ctx, api.APITokenListFilter{}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, tok := range items {
			got = append(got, tok.Name)
		}
		if next == "" {
			break
		}
		page.Cursor = next
	}
	if len(got) < 5 {
		t.Fatalf("got %d items, want at least 5", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i] < got[i-1] {
			t.Fatalf("not sorted asc at index %d: %q > %q", i, got[i-1], got[i])
		}
	}

	// Descending should flip order.
	items, _, err := pg.ListAPITokens(ctx, api.APITokenListFilter{}, api.ListPage{Limit: 50, Sort: "name", Order: "desc"})
	if err != nil {
		t.Fatalf("desc: %v", err)
	}
	for i := 1; i < len(items); i++ {
		if items[i].Name > items[i-1].Name {
			t.Fatalf("not sorted desc at index %d: %q < %q", i, items[i-1].Name, items[i].Name)
		}
	}
}

func TestListAPITokensNameFilter(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	u := seedTokenUser(t, pg)
	suffix := uuid.New().String()[:6]
	seedToken(t, pg, *u.Id, "ci-deploy-"+suffix)
	seedToken(t, pg, *u.Id, "ci-test-"+suffix)
	seedToken(t, pg, *u.Id, "prod-reader-"+suffix)
	seedToken(t, pg, *u.Id, "my_token-"+suffix)

	cases := []struct {
		term    string
		wantMin int
	}{
		{"ci", 2},                 // substring
		{"ci-*", 2},               // prefix glob
		{"*-" + suffix, 4},        // suffix glob
		{"my_token-" + suffix, 1}, // literal underscore
		{"CI", 2},                 // case-insensitive
	}
	for _, tc := range cases {
		term := tc.term
		items, _, err := pg.ListAPITokens(ctx, api.APITokenListFilter{Name: &term}, api.ListPage{Limit: 50})
		if err != nil {
			t.Fatalf("%q: %v", tc.term, err)
		}
		if len(items) < tc.wantMin {
			t.Errorf("name=%q: got %d items, want at least %d", tc.term, len(items), tc.wantMin)
		}
	}
}

func TestListAPITokensRejectsBadSortAndMismatchedCursor(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	u := seedTokenUser(t, pg)
	seedToken(t, pg, *u.Id, "tok-a-"+uuid.New().String()[:6])
	seedToken(t, pg, *u.Id, "tok-b-"+uuid.New().String()[:6])
	seedToken(t, pg, *u.Id, "tok-c-"+uuid.New().String()[:6])

	if _, _, err := pg.ListAPITokens(ctx, api.APITokenListFilter{}, api.ListPage{Sort: "bogus"}); !errors.Is(err, api.ErrInvalidSort) {
		t.Errorf("bogus sort: %v, want ErrInvalidSort", err)
	}

	_, next, err := pg.ListAPITokens(ctx, api.APITokenListFilter{}, api.ListPage{Limit: 1, Sort: "name"})
	if err != nil || next == "" {
		t.Fatalf("seed cursor: next=%q err=%v", next, err)
	}
	// Replay name cursor under created_at (default) → invalid.
	if _, _, err := pg.ListAPITokens(ctx, api.APITokenListFilter{}, api.ListPage{Limit: 1, Cursor: next}); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("mismatched cursor: %v, want ErrInvalidCursor", err)
	}
	// Legacy pipe cursor → invalid.
	legacy := encodeCursor(timeNowFixed(t), uuid.New())
	if _, _, err := pg.ListAPITokens(ctx, api.APITokenListFilter{}, api.ListPage{Cursor: legacy}); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("legacy cursor: %v, want ErrInvalidCursor", err)
	}
}

func TestListAPITokensSortTieBreakAcrossPages(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	u := seedTokenUser(t, pg)
	suffix := uuid.New().String()[:6]
	tok1 := seedToken(t, pg, *u.Id, "tok1-"+suffix)
	tok2 := seedToken(t, pg, *u.Id, "tok2-"+suffix)
	tok3 := seedToken(t, pg, *u.Id, "tok3-"+suffix)
	tok4 := seedToken(t, pg, *u.Id, "tok4-"+suffix)
	tok5 := seedToken(t, pg, *u.Id, "tok5-"+suffix) // no expires_at
	_ = tok5

	// Give tok1+tok2 the same expires_at and tok3+tok4 another; tok5 stays NULL.
	expA := timeNowFixed(t)
	expB := expA.Add(24 * 3600 * 1e9)
	if _, err := pg.pool.Exec(ctx,
		`UPDATE api_tokens SET expires_at = $3 WHERE id IN ($1,$2)`,
		tok1.Id, tok2.Id, expA,
	); err != nil {
		t.Fatalf("set expires_at A: %v", err)
	}
	if _, err := pg.pool.Exec(ctx,
		`UPDATE api_tokens SET expires_at = $3 WHERE id IN ($1,$2)`,
		tok3.Id, tok4.Id, expB,
	); err != nil {
		t.Fatalf("set expires_at B: %v", err)
	}

	seen := map[string]bool{}
	page := api.ListPage{Limit: 2, Sort: "expires_at"}
	total := 0
	for {
		items, next, err := pg.ListAPITokens(ctx, api.APITokenListFilter{}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for i := range items {
			tok := &items[i]
			if seen[tok.Id.String()] {
				t.Fatalf("token %s duplicated across pages (tiebreaker broken)", tok.Id)
			}
			seen[tok.Id.String()] = true
		}
		total += len(items)
		if next == "" {
			break
		}
		page.Cursor = next
	}
	if total < 5 {
		t.Fatalf("total=%d want at least 5 (row skipped at tied page boundary)", total)
	}
}

func TestListAPITokensDefaultOrderUnchanged(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	u := seedTokenUser(t, pg)
	suffix := uuid.New().String()[:6]
	for i := range 5 {
		seedToken(t, pg, *u.Id, "do-tok-"+suffix+"-"+string(rune('a'+i)))
	}

	seen := map[string]bool{}
	page := api.ListPage{Limit: 2}
	total := 0
	for {
		items, next, err := pg.ListAPITokens(ctx, api.APITokenListFilter{}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, tok := range items {
			if seen[tok.Id.String()] {
				t.Fatalf("duplicate %s across pages", tok.Id)
			}
			seen[tok.Id.String()] = true
		}
		total += len(items)
		if next == "" {
			break
		}
		page.Cursor = next
	}
	if total < 5 {
		t.Fatalf("total=%d want at least 5", total)
	}
}

// ── Sessions ──────────────────────────────────────────────────────────────────

// seedSession creates a session for the given user.
// ExpiresAt is set 24 h in the future (real clock) so the active-session
// filter (expires_at > NOW()) does not suppress the test row.
func seedSession(t *testing.T, pg *PG, userID uuid.UUID) {
	t.Helper()
	sid := uuid.New().String()
	now := time.Now().UTC()
	err := pg.CreateSession(t.Context(), api.SessionInsert{
		ID:        sid,
		UserID:    userID,
		CreatedAt: now,
		ExpiresAt: now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
}

// TestListSessionsPaginatesAcrossCreatedAtTies is the regression test for the
// cursor-mint bug: the old code paired items[limit-1].CreatedAt with the
// loop-trailing lastPublicID (the PEEK row's id), causing a skip or duplicate
// at a page boundary when adjacent sessions share created_at.
func TestListSessionsPaginatesAcrossCreatedAtTies(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	// Seed one user + 4 sessions.
	u := seedUser(t, pg, "tie-user-"+uuid.New().String()[:6], auth.RoleViewer)
	for range 4 {
		seedSession(t, pg, *u.Id)
	}

	// Force identical created_at on ALL sessions so the tiebreaker is exercised
	// on every page boundary.
	_, err := pg.pool.Exec(ctx,
		`UPDATE sessions SET created_at = '2026-01-02T03:04:05Z'`,
	)
	if err != nil {
		t.Fatalf("force created_at: %v", err)
	}

	seen := map[string]bool{}
	page := api.ListPage{Limit: 2}
	total := 0
	for {
		items, next, err := pg.ListSessions(ctx, api.SessionListFilter{}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, s := range items {
			if seen[s.Id] {
				t.Fatalf("session %s duplicated across pages (tie bug)", s.Id)
			}
			seen[s.Id] = true
		}
		total += len(items)
		if next == "" {
			break
		}
		page.Cursor = next
	}
	if total != 4 {
		t.Fatalf("total=%d want 4 (boundary row skipped — tie bug)", total)
	}
}

func TestListSessionsNameFiltersUsername(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	suffix := uuid.New().String()[:6]
	alice := seedUser(t, pg, "alice-"+suffix, auth.RoleViewer)
	bob := seedUser(t, pg, "bob-"+suffix, auth.RoleViewer)
	seedSession(t, pg, *alice.Id)
	seedSession(t, pg, *bob.Id)

	term := "alice"
	items, _, err := pg.ListSessions(ctx, api.SessionListFilter{Name: &term}, api.ListPage{Limit: 50})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1 (alice only)", len(items))
	}
	if items[0].Username == nil || *items[0].Username != "alice-"+suffix {
		t.Fatalf("got username %v, want alice-%s", items[0].Username, suffix)
	}
}

func TestListSessionsSortByUsername(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	suffix := uuid.New().String()[:6]
	for _, name := range []string{"charlie", "alice", "eve", "bob", "diana"} {
		u := seedUser(t, pg, name+"-"+suffix, auth.RoleViewer)
		seedSession(t, pg, *u.Id)
	}

	var got []string
	page := api.ListPage{Limit: 2, Sort: "username"}
	for {
		items, next, err := pg.ListSessions(ctx, api.SessionListFilter{}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, s := range items {
			if s.Username != nil {
				got = append(got, *s.Username)
			}
		}
		if next == "" {
			break
		}
		page.Cursor = next
	}
	if len(got) < 5 {
		t.Fatalf("got %d items, want at least 5", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i] < got[i-1] {
			t.Fatalf("not sorted asc at index %d: %q > %q", i, got[i-1], got[i])
		}
	}
}

func TestListSessionsRejectsBadSortAndMismatchedCursor(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	u := seedUser(t, pg, "sorttest-"+uuid.New().String()[:6], auth.RoleViewer)
	seedSession(t, pg, *u.Id)
	seedSession(t, pg, *u.Id)
	seedSession(t, pg, *u.Id)

	if _, _, err := pg.ListSessions(ctx, api.SessionListFilter{}, api.ListPage{Sort: "bogus"}); !errors.Is(err, api.ErrInvalidSort) {
		t.Errorf("bogus sort: %v, want ErrInvalidSort", err)
	}

	_, next, err := pg.ListSessions(ctx, api.SessionListFilter{}, api.ListPage{Limit: 1, Sort: "username"})
	if err != nil || next == "" {
		t.Fatalf("seed cursor: next=%q err=%v", next, err)
	}
	// Replay username cursor under created_at (default) → invalid.
	if _, _, err := pg.ListSessions(ctx, api.SessionListFilter{}, api.ListPage{Limit: 1, Cursor: next}); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("mismatched cursor: %v, want ErrInvalidCursor", err)
	}
	// Legacy pipe cursor → invalid.
	legacy := encodeCursor(timeNowFixed(t), uuid.New())
	if _, _, err := pg.ListSessions(ctx, api.SessionListFilter{}, api.ListPage{Cursor: legacy}); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("legacy cursor: %v, want ErrInvalidCursor", err)
	}
}

func TestListSessionsDefaultOrderUnchanged(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	u := seedUser(t, pg, "do-sess-"+uuid.New().String()[:6], auth.RoleViewer)
	for range 5 {
		seedSession(t, pg, *u.Id)
	}

	seen := map[string]bool{}
	page := api.ListPage{Limit: 2}
	total := 0
	for {
		items, next, err := pg.ListSessions(ctx, api.SessionListFilter{}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, s := range items {
			if seen[s.Id] {
				t.Fatalf("duplicate %s across pages", s.Id)
			}
			seen[s.Id] = true
		}
		total += len(items)
		if next == "" {
			break
		}
		page.Cursor = next
	}
	if total < 5 {
		t.Fatalf("total=%d want at least 5", total)
	}
}
