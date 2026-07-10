package store

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

func TestNamePattern(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain term wraps as substring", "du", "%du%"},
		{"uppercase folds to lower", "Du", "%du%"},
		{"trailing star anchors prefix", "du*", "du%"},
		{"leading star anchors suffix", "*du", "%du"},
		{"middle star anchors both ends", "prod-*-db", "prod-%-db"},
		{"underscore is literal", "my_app", `%my\_app%`},
		{"percent is literal", "50%", `%50\%%`},
		{"backslash is literal", `a\b`, `%a\\b%`},
		{"star with metachars", "my_app*", `my\_app%`},
		{"lone star matches everything", "*", "%"},
	}
	for _, tc := range cases {
		if got := namePattern(tc.in); got != tc.want {
			t.Errorf("%s: namePattern(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestListCursorRoundTrip(t *testing.T) {
	id := uuid.New()
	val := "widget-7"
	enc := encodeListCursor("name", &val, id, "asc")

	gotVal, gotID, err := decodeListCursor(enc, "name", "asc")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if gotVal == nil || *gotVal != val {
		t.Errorf("val = %v, want %q", gotVal, val)
	}
	if gotID != id {
		t.Errorf("id = %v, want %v", gotID, id)
	}
}

func TestListCursorNullVal(t *testing.T) {
	id := uuid.New()
	enc := encodeListCursor("owner", nil, id, "asc")
	gotVal, gotID, err := decodeListCursor(enc, "owner", "asc")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if gotVal != nil {
		t.Errorf("val = %q, want nil", *gotVal)
	}
	if gotID != id {
		t.Errorf("id = %v, want %v", gotID, id)
	}
}

func TestListCursorRejectsMismatchAndGarbage(t *testing.T) {
	id := uuid.New()
	val := "x"
	enc := encodeListCursor("name", &val, id, "asc")

	cases := []struct {
		name          string
		cursor        string
		wantCol, wantDir string
	}{
		{"different sort column", enc, "created_at", "asc"},
		{"different direction", enc, "name", "desc"},
		{"garbage", "not-base64!!", "name", "asc"},
		{"legacy pipe cursor", encodeCursor(timeNowFixed(t), id), "created_at", "desc"},
		{"valid b64, not json", "aGVsbG8", "name", "asc"},
	}
	for _, tc := range cases {
		_, _, err := decodeListCursor(tc.cursor, tc.wantCol, tc.wantDir)
		if !errors.Is(err, api.ErrInvalidCursor) {
			t.Errorf("%s: err = %v, want api.ErrInvalidCursor", tc.name, err)
		}
	}
}

// timeNowFixed returns a fixed timestamp for legacy-cursor construction.
func timeNowFixed(t *testing.T) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, "2026-01-02T03:04:05Z")
	if err != nil {
		t.Fatal(err)
	}
	return ts
}
