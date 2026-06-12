// Package apiclient implements collector.CmdbStore over the longue-vue REST API.
// It is the write-path for the push-mode collector (ADR-0009): every store
// method maps to one HTTP call against a remote longue-vue instance.
// Transport, retry, and header plumbing live in internal/httptransport;
// this package only contributes the CMDB status→error mapping.
package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/httptransport"
	"github.com/sthalbert/longue-vue/internal/store"
)

// errHTTPRequest marks a non-2xx response that maps to no dedicated sentinel.
var errHTTPRequest = errors.New("HTTP request error")

// Config carries the knobs for building an HTTP-backed store.
// See httptransport.Config for the field documentation.
type Config = httptransport.Config

// Store implements collector.CmdbStore by calling the longue-vue REST API.
type Store struct {
	c *httptransport.Client
}

// NewStore builds an HTTP-backed store from cfg.
//
//nolint:gocritic // hugeParam: keeping value receiver for backward compatibility with external callers.
func NewStore(cfg Config) (*Store, error) {
	c, err := httptransport.New(&cfg)
	if err != nil {
		return nil, err
	}
	return &Store{c: c}, nil
}

// ── collector.CmdbStore implementation ──────────────────────────────

// EnsureCluster registers a cluster in the CMDB if no row with the same name
// exists, or returns the existing row unchanged when one does. The returned
// bool is true when a new row was inserted (server response 201), false when
// an existing row was returned (server response 200).
//
// EnsureCluster is the only cluster-bootstrap entry point on the apiclient
// store: a push-mode collector behind the strict-write-only DMZ ingest
// gateway (ADR-0016) cannot reach GET /v1/clusters, so the previous
// GET-then-POST pattern is replaced by a single idempotent POST.
//
//nolint:gocritic // hugeParam: signature matches CmdbStore interface
func (s *Store) EnsureCluster(ctx context.Context, in api.ClusterCreate) (api.Cluster, bool, error) {
	var out api.Cluster
	status, err := s.doJSONStatus(ctx, http.MethodPost, "/v1/clusters", in, &out)
	if err != nil {
		return api.Cluster{}, false, fmt.Errorf("ensure cluster: %w", err)
	}
	return out, status == http.StatusCreated, nil
}

// UpdateCluster applies a partial update to the cluster identified by id.
//
//nolint:gocritic // hugeParam: signature matches CmdbStore interface.
func (s *Store) UpdateCluster(ctx context.Context, id uuid.UUID, in api.ClusterUpdate) (api.Cluster, error) {
	var out api.Cluster
	if err := s.doJSON(ctx, http.MethodPatch, "/v1/clusters/"+id.String(), in, &out); err != nil {
		return api.Cluster{}, fmt.Errorf("update cluster: %w", err)
	}
	return out, nil
}

// UpsertNode creates or updates a node record.
//
//nolint:gocritic // hugeParam: signature matches CmdbStore interface.
func (s *Store) UpsertNode(ctx context.Context, in api.NodeCreate) (api.Node, api.UpsertOutcome, error) {
	var out api.Node
	if err := s.doJSON(ctx, http.MethodPost, "/v1/nodes", in, &out); err != nil {
		return api.Node{}, api.OutcomeBusinessChanged, fmt.Errorf("upsert node: %w", err)
	}
	return out, api.OutcomeBusinessChanged, nil
}

// DeleteNodesNotIn removes nodes not in the keepNames list for the given cluster.
func (s *Store) DeleteNodesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
	return s.reconcileClusterScoped(ctx, "/v1/nodes/reconcile", clusterID, keepNames)
}

// UpsertNamespace creates or updates a namespace record.
//
//nolint:gocritic // hugeParam: signature matches CmdbStore interface.
func (s *Store) UpsertNamespace(ctx context.Context, in api.NamespaceCreate) (api.Namespace, api.UpsertOutcome, error) {
	var out api.Namespace
	if err := s.doJSON(ctx, http.MethodPost, "/v1/namespaces", in, &out); err != nil {
		return api.Namespace{}, api.OutcomeBusinessChanged, fmt.Errorf("upsert namespace: %w", err)
	}
	return out, api.OutcomeBusinessChanged, nil
}

// DeleteNamespacesNotIn removes namespaces not in the keepNames list for the given cluster.
func (s *Store) DeleteNamespacesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
	return s.reconcileClusterScoped(ctx, "/v1/namespaces/reconcile", clusterID, keepNames)
}

// UpsertPod creates or updates a pod record.
//
//nolint:gocritic // hugeParam: signature matches CmdbStore interface.
func (s *Store) UpsertPod(ctx context.Context, in api.PodCreate) (api.Pod, api.UpsertOutcome, error) {
	var out api.Pod
	if err := s.doJSON(ctx, http.MethodPost, "/v1/pods", in, &out); err != nil {
		return api.Pod{}, api.OutcomeBusinessChanged, fmt.Errorf("upsert pod: %w", err)
	}
	return out, api.OutcomeBusinessChanged, nil
}

// DeletePodsNotIn removes pods not in the keepNames list for the given namespace.
func (s *Store) DeletePodsNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	return s.reconcileNamespaceScoped(ctx, "/v1/pods/reconcile", namespaceID, keepNames)
}

// UpsertWorkload creates or updates a workload record.
//
//nolint:gocritic // hugeParam: signature matches CmdbStore interface.
func (s *Store) UpsertWorkload(ctx context.Context, in api.WorkloadCreate) (api.Workload, api.UpsertOutcome, error) {
	var out api.Workload
	if err := s.doJSON(ctx, http.MethodPost, "/v1/workloads", in, &out); err != nil {
		return api.Workload{}, api.OutcomeBusinessChanged, fmt.Errorf("upsert workload: %w", err)
	}
	return out, api.OutcomeBusinessChanged, nil
}

// DeleteWorkloadsNotIn removes workloads not in the keep lists for the given namespace.
func (s *Store) DeleteWorkloadsNotIn(ctx context.Context, namespaceID uuid.UUID, keepKinds, keepNames []string) (int64, error) {
	body := reconcileWorkloadsBody{
		NamespaceID: namespaceID,
		KeepKinds:   keepKinds,
		KeepNames:   keepNames,
	}
	var result reconcileResultBody
	if err := s.doJSON(ctx, http.MethodPost, "/v1/workloads/reconcile", body, &result); err != nil {
		return 0, fmt.Errorf("reconcile workloads: %w", err)
	}
	return result.Deleted, nil
}

// UpsertService creates or updates a service record.
//
//nolint:gocritic // hugeParam: signature matches CmdbStore interface.
func (s *Store) UpsertService(ctx context.Context, in api.ServiceCreate) (api.Service, api.UpsertOutcome, error) {
	var out api.Service
	if err := s.doJSON(ctx, http.MethodPost, "/v1/services", in, &out); err != nil {
		return api.Service{}, api.OutcomeBusinessChanged, fmt.Errorf("upsert service: %w", err)
	}
	return out, api.OutcomeBusinessChanged, nil
}

// DeleteServicesNotIn removes services not in the keepNames list for the given namespace.
func (s *Store) DeleteServicesNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	return s.reconcileNamespaceScoped(ctx, "/v1/services/reconcile", namespaceID, keepNames)
}

// UpsertIngress creates or updates an ingress record.
func (s *Store) UpsertIngress(ctx context.Context, in api.IngressCreate) (api.Ingress, api.UpsertOutcome, error) {
	var out api.Ingress
	if err := s.doJSON(ctx, http.MethodPost, "/v1/ingresses", in, &out); err != nil {
		return api.Ingress{}, api.OutcomeBusinessChanged, fmt.Errorf("upsert ingress: %w", err)
	}
	return out, api.OutcomeBusinessChanged, nil
}

// DeleteIngressesNotIn removes ingresses not in the keepNames list for the given namespace.
func (s *Store) DeleteIngressesNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	return s.reconcileNamespaceScoped(ctx, "/v1/ingresses/reconcile", namespaceID, keepNames)
}

// UpsertPersistentVolume creates or updates a persistent volume record.
//
//nolint:gocritic // hugeParam: signature matches CmdbStore interface.
func (s *Store) UpsertPersistentVolume(ctx context.Context, in api.PersistentVolumeCreate) (api.PersistentVolume, api.UpsertOutcome, error) {
	var out api.PersistentVolume
	if err := s.doJSON(ctx, http.MethodPost, "/v1/persistentvolumes", in, &out); err != nil {
		return api.PersistentVolume{}, api.OutcomeBusinessChanged, fmt.Errorf("upsert persistent volume: %w", err)
	}
	return out, api.OutcomeBusinessChanged, nil
}

// DeletePersistentVolumesNotIn removes PVs not in the keepNames list for the given cluster.
func (s *Store) DeletePersistentVolumesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
	return s.reconcileClusterScoped(ctx, "/v1/persistentvolumes/reconcile", clusterID, keepNames)
}

// UpsertPersistentVolumeClaim creates or updates a PVC record.
//
//nolint:gocritic // hugeParam: signature matches CmdbStore interface.
func (s *Store) UpsertPersistentVolumeClaim(
	ctx context.Context,
	in api.PersistentVolumeClaimCreate,
) (api.PersistentVolumeClaim, api.UpsertOutcome, error) {
	var out api.PersistentVolumeClaim
	if err := s.doJSON(ctx, http.MethodPost, "/v1/persistentvolumeclaims", in, &out); err != nil {
		return api.PersistentVolumeClaim{}, api.OutcomeBusinessChanged, fmt.Errorf("upsert persistent volume claim: %w", err)
	}
	return out, api.OutcomeBusinessChanged, nil
}

// DeletePersistentVolumeClaimsNotIn removes PVCs not in the keepNames list for the given namespace.
func (s *Store) DeletePersistentVolumeClaimsNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	return s.reconcileNamespaceScoped(ctx, "/v1/persistentvolumeclaims/reconcile", namespaceID, keepNames)
}

// UpsertNetworkPolicy upserts a NetworkPolicy and its rules atomically via
// POST /v1/network-policies (ADR-0038). Push-collector replacement for the
// in-process *store.PG.UpsertNetworkPolicy method.
//
//nolint:gocritic // hugeParam: signature mirrors the NetPolStore interface
func (s *Store) UpsertNetworkPolicy(
	ctx context.Context, np store.NetworkPolicy, rules []store.NetworkPolicyRule,
) (uuid.UUID, error) {
	in := api.NetworkPolicyCreate{
		ClusterId:   np.ClusterID,
		NamespaceId: np.NamespaceID,
		Name:        np.Name,
		PodSelector: rawToMap(np.PodSelector),
		PolicyTypes: np.PolicyTypes,
		SpecRaw:     rawToMap(np.SpecRaw),
		Rules:       toAPINetworkPolicyRules(rules),
	}
	var out api.NetworkPolicy
	if err := s.doJSON(ctx, http.MethodPost, "/v1/network-policies", in, &out); err != nil {
		return uuid.Nil, fmt.Errorf("upsert network policy: %w", err)
	}
	return out.Id, nil
}

// SweepNetworkPoliciesByNamespace deletes every NetworkPolicy in the given
// namespace whose name is not in seen. POST /v1/network-policies/reconcile
// (ADR-0038). Mirror of the existing namespace-scoped sweeps for pods,
// services, etc. — reuses the shared reconcileNamespaceScoped helper.
func (s *Store) SweepNetworkPoliciesByNamespace(
	ctx context.Context, nsID uuid.UUID, seen []string,
) error {
	_, err := s.reconcileNamespaceScoped(ctx, "/v1/network-policies/reconcile", nsID, seen)
	return err
}

func toAPINetworkPolicyRules(in []store.NetworkPolicyRule) []api.NetworkPolicyRuleInput {
	out := make([]api.NetworkPolicyRuleInput, 0, len(in))
	for _, r := range in { //nolint:gocritic // rangeValCopy: NetworkPolicyRuleRow contains JSONB slices
		e := api.NetworkPolicyRuleInput{
			Direction: api.NetworkPolicyRuleInputDirection(r.Direction),
			PeerKind:  api.NetworkPolicyRuleInputPeerKind(r.PeerKind),
			Ports:     rawToMapSlice(r.Ports),
		}
		if len(r.PeerPodSelector) > 0 {
			m := rawToMap(r.PeerPodSelector)
			e.PeerPodSelector = &m
		}
		if len(r.PeerNamespaceSelector) > 0 {
			m := rawToMap(r.PeerNamespaceSelector)
			e.PeerNamespaceSelector = &m
		}
		if r.PeerIPBlockCIDR != "" {
			c := r.PeerIPBlockCIDR
			e.PeerIpBlockCidr = &c
		}
		if len(r.PeerIPBlockExcept) > 0 {
			xs := rawToStringSlice(r.PeerIPBlockExcept)
			e.PeerIpBlockExcept = &xs
		}
		out = append(out, e)
	}
	return out
}

// jsonNullLiteral is the wire form of a JSON null — used by the rawTo* helpers
// to distinguish "no value" from a real empty container.
const jsonNullLiteral = "null"

func rawToMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 || string(raw) == jsonNullLiteral {
		return map[string]any{}
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func rawToMapSlice(raw json.RawMessage) []map[string]any {
	if len(raw) == 0 || string(raw) == jsonNullLiteral {
		return []map[string]any{}
	}
	var s []map[string]any
	_ = json.Unmarshal(raw, &s)
	if s == nil {
		return []map[string]any{}
	}
	return s
}

func rawToStringSlice(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == jsonNullLiteral {
		return nil
	}
	var s []string
	_ = json.Unmarshal(raw, &s)
	return s
}

// ── HTTP helpers ────────────────────────────────────────────────────

// reconcile body types -- lightweight JSON carriers matching the OpenAPI
// schemas without importing the generated types (avoids a circular dep).

type reconcileClusterScopedBody struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	KeepNames []string  `json:"keep_names"`
}

type reconcileNamespaceScopedBody struct {
	NamespaceID uuid.UUID `json:"namespace_id"`
	KeepNames   []string  `json:"keep_names"`
}

type reconcileWorkloadsBody struct {
	NamespaceID uuid.UUID `json:"namespace_id"`
	KeepKinds   []string  `json:"keep_kinds"`
	KeepNames   []string  `json:"keep_names"`
}

type reconcileResultBody struct {
	Deleted int64 `json:"deleted"`
}

func (s *Store) reconcileClusterScoped(ctx context.Context, path string, clusterID uuid.UUID, keepNames []string) (int64, error) {
	body := reconcileClusterScopedBody{
		ClusterID: clusterID,
		KeepNames: keepNames,
	}
	var result reconcileResultBody
	if err := s.doJSON(ctx, http.MethodPost, path, body, &result); err != nil {
		return 0, fmt.Errorf("reconcile %s: %w", path, err)
	}
	return result.Deleted, nil
}

func (s *Store) reconcileNamespaceScoped(ctx context.Context, path string, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	body := reconcileNamespaceScopedBody{
		NamespaceID: namespaceID,
		KeepNames:   keepNames,
	}
	var result reconcileResultBody
	if err := s.doJSON(ctx, http.MethodPost, path, body, &result); err != nil {
		return 0, fmt.Errorf("reconcile %s: %w", path, err)
	}
	return result.Deleted, nil
}

// doJSON sends an HTTP request with optional JSON body and decodes the
// JSON response into dst. Retries transient 5xx errors with exponential
// backoff; returns immediately on 401/403.
func (s *Store) doJSON(ctx context.Context, method, path string, body, dst any) error {
	_, err := s.doJSONStatus(ctx, method, path, body, dst)
	return err
}

// doJSONStatus is doJSON, but additionally returns the HTTP status code of
// the final (successful or terminal) response. Callers that need to
// distinguish between 200 and 201 (e.g. EnsureCluster) use this; everyone
// else uses doJSON.
func (s *Store) doJSONStatus(ctx context.Context, method, path string, body, dst any) (int, error) {
	return s.c.DoJSON(ctx, method, path, body, dst, mapCmdbError)
}

// mapCmdbError maps non-2xx statuses to the CMDB store contract: 404 →
// api.ErrNotFound, 409 → api.ErrConflict, 401/403 terminal (the token is
// wrong; retrying cannot help), 5xx transient.
func mapCmdbError(method, path string, statusCode int, respBody []byte) (httptransport.Disposition, error) {
	httpErr := func() error {
		return fmt.Errorf("%s %s: %w: %d %s", method, path, errHTTPRequest, statusCode, httptransport.Truncate(string(respBody), 200))
	}

	switch {
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		slog.Error("apiclient: auth error (not retrying)",
			slog.String("method", method), slog.String("path", path),
			slog.Int("status", statusCode), slog.String("body", httptransport.Truncate(string(respBody), 500)))
		return httptransport.Done, httpErr()
	case statusCode == http.StatusNotFound:
		return httptransport.Done, api.ErrNotFound
	case statusCode == http.StatusConflict:
		return httptransport.Done, api.ErrConflict
	case statusCode >= 500:
		slog.Warn("apiclient: transient error, retrying",
			slog.String("method", method), slog.String("path", path),
			slog.Int("status", statusCode), slog.String("body", httptransport.Truncate(string(respBody), 500)))
		return httptransport.Retry, httpErr()
	default:
		return httptransport.Done, httpErr()
	}
}
