// Package httptransport provides the HTTP client plumbing shared by the
// push-mode collector apiclients (internal/collector/apiclient and
// internal/vmcollector/apiclient): TLS/mTLS transport construction from
// PEM file paths, bearer-token + extra-header injection, and a JSON
// round-trip helper with exponential-backoff retries.
//
// Response-status interpretation deliberately stays with each caller via
// the ErrorMapper callback — the two collectors map the same statuses to
// different sentinel errors (e.g. 404 is api.ErrNotFound for the K8s
// collector but ErrNotRegistered for the VM collector).
package httptransport

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Sentinel errors shared by the HTTP-backed stores.
var (
	// ErrNoCACerts is returned when the configured CA bundle parses to zero
	// certificates.
	ErrNoCACerts = errors.New("CA cert file contains no valid certificates")
	// ErrMaxRetries wraps the last attempt's error once the retry budget is
	// exhausted.
	ErrMaxRetries = errors.New("max retries exceeded")

	errBadTransportType = errors.New("unexpected default transport type")
)

// Config carries the knobs for building an HTTP-backed store.
type Config struct {
	// ServerURL is the longue-vue base URL, e.g. "https://longue-vue.internal:8080"
	// or "https://gw:443/lv". A trailing path is prepended to every
	// request so gateway path-prefix rewrite works transparently.
	ServerURL string

	// Token is the bearer token (PAT) injected into every request.
	Token string

	// CACert is the path to a PEM-encoded CA bundle for server TLS
	// verification. Empty uses the system pool.
	CACert string

	// ClientCert and ClientKey are paths to a PEM-encoded client
	// certificate and key for mTLS. Both must be set or both empty.
	ClientCert string
	ClientKey  string

	// ExtraHeaders are injected into every outbound request. Typical
	// use: gateway routing headers (X-Tenant-Id, X-Route-Key).
	ExtraHeaders map[string]string
}

// Client is an http.Client wrapper that injects auth headers and retries
// transient failures on JSON round-trips.
type Client struct {
	hc           *http.Client
	baseURL      string // scheme + host + optional path prefix, no trailing slash
	token        string
	extraHeaders map[string]string
}

// New builds a Client from cfg, wiring the CA / mTLS transport.
func New(cfg *Config) (*Client, error) {
	u, err := url.Parse(cfg.ServerURL)
	if err != nil {
		return nil, fmt.Errorf("parse server URL: %w", err)
	}
	baseURL := strings.TrimRight(u.String(), "/")

	transport, err := buildTransport(cfg)
	if err != nil {
		return nil, err
	}

	return &Client{
		hc:           &http.Client{Transport: transport, Timeout: 30 * time.Second},
		baseURL:      baseURL,
		token:        cfg.Token,
		extraHeaders: cfg.ExtraHeaders,
	}, nil
}

// buildTransport constructs an http.Transport with the TLS settings from cfg.
func buildTransport(cfg *Config) (*http.Transport, error) {
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errBadTransportType
	}
	transport := defaultTransport.Clone()

	if cfg.CACert != "" {
		pem, err := os.ReadFile(cfg.CACert)
		if err != nil {
			return nil, fmt.Errorf("read CA cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, ErrNoCACerts
		}
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		transport.TLSClientConfig.RootCAs = pool
	}

	if cfg.ClientCert != "" && cfg.ClientKey != "" {
		cert, err := tls.LoadX509KeyPair(cfg.ClientCert, cfg.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("load client cert/key: %w", err)
		}
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		transport.TLSClientConfig.Certificates = []tls.Certificate{cert}
	}

	return transport, nil
}

// Disposition tells DoJSON whether a mapped error is terminal or worth
// another attempt.
type Disposition int

const (
	// Done — terminal outcome; DoJSON returns the mapped error immediately.
	Done Disposition = iota
	// Retry — transient failure; DoJSON backs off and retries.
	Retry
)

// ErrorMapper interprets a non-2xx response. path is the logical request
// path handed to DoJSON (without the configured base-URL prefix).
type ErrorMapper func(method, path string, status int, body []byte) (Disposition, error)

const (
	maxRetries    = 3
	retryBaseWait = 1 * time.Second
)

// DoJSON marshals body (when non-nil), sends method+path with the bearer
// token and extra headers, and decodes the 2xx JSON response into dst
// (when non-nil). Non-2xx responses are delegated to mapErr; transport
// and read errors are retried with exponential backoff. The returned int
// is the HTTP status of the final attempt (0 when the request never
// reached the server).
func (c *Client) DoJSON(ctx context.Context, method, path string, body, dst any, mapErr ErrorMapper) (int, error) {
	var marshaledBody []byte
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("marshal request body: %w", err)
		}
		marshaledBody = buf
	}

	fullURL := c.baseURL + path

	var lastErr error
	var lastStatus int
	for attempt := range maxRetries {
		status, result, err := c.doOnce(ctx, method, path, fullURL, marshaledBody, dst, mapErr)
		if err != nil {
			lastErr = err
			lastStatus = status
			if result == Done || ctx.Err() != nil {
				return lastStatus, lastErr
			}
			backoff(ctx, attempt)
			continue
		}
		return status, nil
	}

	return lastStatus, fmt.Errorf("%w: %w", ErrMaxRetries, lastErr)
}

// doOnce performs a single HTTP round-trip for DoJSON. The first return
// value is the HTTP status code of the response (0 when the request never
// reached the server, e.g. on a transport error).
func (c *Client) doOnce(
	ctx context.Context, method, path, fullURL string, marshaledBody []byte, dst any, mapErr ErrorMapper,
) (int, Disposition, error) {
	var bodyReader io.Reader
	if marshaledBody != nil {
		bodyReader = bytes.NewReader(marshaledBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return 0, Done, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if marshaledBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range c.extraHeaders {
		req.Header.Set(k, v)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, Retry, fmt.Errorf("%s %s: %w", method, path, err)
	}

	respBody, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return resp.StatusCode, Retry, fmt.Errorf("%s %s: read response: %w", method, path, readErr)
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if dst != nil && len(respBody) > 0 {
			if err := json.Unmarshal(respBody, dst); err != nil {
				return resp.StatusCode, Done, fmt.Errorf("%s %s: decode response: %w", method, path, err)
			}
		}
		return resp.StatusCode, Done, nil
	}

	result, err := mapErr(method, path, resp.StatusCode, respBody)
	return resp.StatusCode, result, err
}

// backoff sleeps with exponential delay, respecting context cancellation.
func backoff(ctx context.Context, attempt int) {
	wait := time.Duration(math.Pow(2, float64(attempt))) * retryBaseWait
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// Truncate limits s to n bytes for log and error messages.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
