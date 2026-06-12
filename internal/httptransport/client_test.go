package httptransport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNewRejectsInvalidCABundle(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(&Config{ServerURL: "https://example.invalid", CACert: caPath})
	if !errors.Is(err, ErrNoCACerts) {
		t.Fatalf("want ErrNoCACerts, got %v", err)
	}
}

func TestNewRejectsMissingCAFile(t *testing.T) {
	_, err := New(&Config{ServerURL: "https://example.invalid", CACert: "/does/not/exist.pem"})
	if err == nil {
		t.Fatal("want error for missing CA file")
	}
}

func TestNewRejectsBrokenClientKeyPair(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "client.pem")
	keyPath := filepath.Join(dir, "client.key")
	for _, p := range []string{certPath, keyPath} {
		if err := os.WriteFile(p, []byte("garbage"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := New(&Config{ServerURL: "https://example.invalid", ClientCert: certPath, ClientKey: keyPath})
	if err == nil {
		t.Fatal("want error for unparseable client cert/key")
	}
}

func TestDoJSONInjectsAuthAndExtraHeaders(t *testing.T) {
	var gotAuth, gotExtra string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotExtra = r.Header.Get("X-Route-Key")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c, err := New(&Config{
		ServerURL:    srv.URL,
		Token:        "tok",
		ExtraHeaders: map[string]string{"X-Route-Key": "abc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := c.DoJSON(context.Background(), http.MethodGet, "/ping", nil, nil, failMapper(t))
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusNoContent {
		t.Fatalf("want 204, got %d", status)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("want bearer header, got %q", gotAuth)
	}
	if gotExtra != "abc" {
		t.Fatalf("want extra header forwarded, got %q", gotExtra)
	}
}

func TestDoJSONExhaustsRetriesOnRetryDisposition(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c, err := New(&Config{ServerURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("transient")
	_, err = c.DoJSON(context.Background(), http.MethodGet, "/x", nil, nil,
		func(_, _ string, _ int, _ []byte) (Disposition, error) {
			return Retry, sentinel
		})
	if !errors.Is(err, ErrMaxRetries) || !errors.Is(err, sentinel) {
		t.Fatalf("want ErrMaxRetries wrapping the mapped error, got %v", err)
	}
	if attempts != 3 {
		t.Fatalf("want 3 attempts, got %d", attempts)
	}
}

func TestDoJSONStopsImmediatelyOnDoneDisposition(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c, err := New(&Config{ServerURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("terminal")
	status, err := c.DoJSON(context.Background(), http.MethodGet, "/x", nil, nil,
		func(_, _ string, _ int, _ []byte) (Disposition, error) {
			return Done, sentinel
		})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want mapped error, got %v", err)
	}
	if status != http.StatusForbidden {
		t.Fatalf("want 403, got %d", status)
	}
	if attempts != 1 {
		t.Fatalf("want a single attempt, got %d", attempts)
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("abcdef", 4); got != "abcd..." {
		t.Fatalf("got %q", got)
	}
	if got := Truncate("abc", 4); got != "abc" {
		t.Fatalf("got %q", got)
	}
}

// failMapper fails the test if the error mapper is invoked at all.
func failMapper(t *testing.T) ErrorMapper {
	t.Helper()
	return func(method, path string, status int, _ []byte) (Disposition, error) {
		t.Fatalf("unexpected error mapping for %s %s (%d)", method, path, status)
		return Done, nil
	}
}
