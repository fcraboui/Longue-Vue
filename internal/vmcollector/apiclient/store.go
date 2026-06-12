// Package apiclient implements the narrow collector-side store that
// longue-vue-vm-collector uses to talk to longue-vue over HTTPS (ADR-0015).
// Transport, retry, and header plumbing are shared with
// internal/collector/apiclient via internal/httptransport; this package
// only contributes the VM-collector status→error mapping and exposes only
// the methods the VM collector needs.
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/httptransport"
	"github.com/sthalbert/longue-vue/internal/vmcollector/provider"
)

// Sentinel errors.
var (
	// ErrAlreadyKubeNode is returned by UpsertVirtualMachine when longue-vue
	// reports 409 — the VM is already inventoried as a Kubernetes node.
	ErrAlreadyKubeNode = errors.New("already_inventoried_as_kubernetes_node")
	// ErrNotRegistered is returned by FetchCredentialsByName when longue-vue
	// returns 404 (the cloud_account row does not exist yet).
	ErrNotRegistered = errors.New("cloud_account_not_registered")
	// ErrAccountDisabled is returned when longue-vue reports 403 — the
	// account has been disabled by an admin.
	ErrAccountDisabled = errors.New("cloud_account_disabled")
	// ErrHTTPForbidden is returned on 403 responses that are not account-disabled.
	ErrHTTPForbidden = errors.New("http 403 forbidden")
	// ErrHTTPConflict is returned on 409 responses that are not kube-node conflicts.
	ErrHTTPConflict = errors.New("http 409 conflict")
	// ErrHTTPUnauthorized is returned on 401 responses.
	ErrHTTPUnauthorized = errors.New("http 401 unauthorized")
	// ErrHTTPClientError is returned on other 4xx responses.
	ErrHTTPClientError = errors.New("http client error")
	// ErrHTTPServerError is returned on 5xx responses (retriable).
	ErrHTTPServerError = errors.New("http server error")
)

// Config carries the knobs for building the HTTP-backed store.
// See httptransport.Config for the field documentation.
type Config = httptransport.Config

// Store is the HTTP-backed collector store.
type Store struct {
	c *httptransport.Client
}

// NewStore builds a Store from cfg.
//
//nolint:gocritic // hugeParam: stable signature
func NewStore(cfg Config) (*Store, error) {
	c, err := httptransport.New(&cfg)
	if err != nil {
		return nil, err
	}
	return &Store{c: c}, nil
}

// Credentials is the JSON shape returned by /v1/cloud-accounts/.../credentials.
type Credentials struct {
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	Region    string `json:"region"`
	Provider  string `json:"provider"`
}

// CloudAccount mirrors the relevant fields of api.CloudAccount used
// by the collector. Kept as its own type so we don't import the
// longue-vue internal/api package from the collector binary.
type CloudAccount struct {
	ID       uuid.UUID `json:"id"`
	Provider string    `json:"provider"`
	Name     string    `json:"name"`
	Region   string    `json:"region"`
	Status   string    `json:"status"`
}

// FetchCredentialsByName GETs /v1/cloud-accounts/by-name/{name}/credentials.
func (s *Store) FetchCredentialsByName(ctx context.Context, name string) (Credentials, error) {
	var out Credentials
	path := "/v1/cloud-accounts/by-name/" + url.PathEscape(name) + "/credentials"
	if err := s.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return Credentials{}, err
	}
	return out, nil
}

// RegisterCloudAccount POSTs /v1/cloud-accounts to register the
// (provider, name, region) tuple. Idempotent.
func (s *Store) RegisterCloudAccount(ctx context.Context, providerName, name, region string) (CloudAccount, error) {
	body := map[string]string{
		"provider": providerName,
		"name":     name,
		"region":   region,
	}
	var out CloudAccount
	if err := s.doJSON(ctx, http.MethodPost, "/v1/cloud-accounts", body, &out); err != nil {
		return CloudAccount{}, err
	}
	return out, nil
}

// UpdateCloudAccountStatus PATCHes /v1/cloud-accounts/{id}/status.
func (s *Store) UpdateCloudAccountStatus(ctx context.Context, id uuid.UUID, status string, lastSeenAt *time.Time, lastErr *string) error {
	body := map[string]any{}
	if status != "" {
		body["status"] = status
	}
	if lastSeenAt != nil {
		body["last_seen_at"] = lastSeenAt.UTC().Format(time.RFC3339)
		now := time.Now().UTC().Format(time.RFC3339)
		body["last_error_at"] = now
	}
	if lastErr != nil {
		body["last_error"] = *lastErr
	}
	if err := s.doJSON(ctx, http.MethodPatch, "/v1/cloud-accounts/"+id.String()+"/status", body, nil); err != nil {
		return err
	}
	return nil
}

// upsertVMBody mirrors the longue-vue handler's vmUpsertReq so we don't
// import that type.
type upsertVMBody struct {
	CloudAccountID       uuid.UUID         `json:"cloud_account_id"`
	ProviderVMID         string            `json:"provider_vm_id"`
	Name                 string            `json:"name"`
	Role                 *string           `json:"role,omitempty"`
	PrivateIP            *string           `json:"private_ip,omitempty"`
	PublicIP             *string           `json:"public_ip,omitempty"`
	PrivateDNSName       *string           `json:"private_dns_name,omitempty"`
	VPCID                *string           `json:"vpc_id,omitempty"`
	SubnetID             *string           `json:"subnet_id,omitempty"`
	NICs                 json.RawMessage   `json:"nics,omitempty"`
	SecurityGroups       json.RawMessage   `json:"security_groups,omitempty"`
	InstanceType         *string           `json:"instance_type,omitempty"`
	Architecture         *string           `json:"architecture,omitempty"`
	Zone                 *string           `json:"zone,omitempty"`
	Region               *string           `json:"region,omitempty"`
	ImageID              *string           `json:"image_id,omitempty"`
	ImageName            *string           `json:"image_name,omitempty"`
	KeypairName          *string           `json:"keypair_name,omitempty"`
	BootMode             *string           `json:"boot_mode,omitempty"`
	ProviderAccountID    *string           `json:"provider_account_id,omitempty"`
	ProviderCreationDate *string           `json:"provider_creation_date,omitempty"`
	PowerState           string            `json:"power_state"`
	StateReason          *string           `json:"state_reason,omitempty"`
	Ready                bool              `json:"ready"`
	DeletionProtection   bool              `json:"deletion_protection"`
	CapacityCPU          *string           `json:"capacity_cpu,omitempty"`
	CapacityMemory       *string           `json:"capacity_memory,omitempty"`
	BlockDevices         json.RawMessage   `json:"block_devices,omitempty"`
	RootDeviceType       *string           `json:"root_device_type,omitempty"`
	RootDeviceName       *string           `json:"root_device_name,omitempty"`
	Tags                 map[string]string `json:"tags,omitempty"`
}

// UpsertVirtualMachine POSTs /v1/virtual-machines.
//
//nolint:gocritic // hugeParam: provider.VM matches the CollectorStore interface; copying is acceptable on this path
func (s *Store) UpsertVirtualMachine(ctx context.Context, accountID uuid.UUID, vm provider.VM) error {
	// VMSecurityGroupsPayload is a safe struct; the error is handled below with a fallback.
	sgJSON, err := json.Marshal(vm.SecurityGroups) //nolint:errchkjson // safe struct; error handled with fallback below
	if err != nil {
		// Defensive: VMSecurityGroupsPayload is always marshalable; fall
		// back to a minimal versioned payload rather than dropping the VM.
		sgJSON, _ = json.Marshal(provider.VMSecurityGroupsPayload{Version: provider.SGSchemaVersion})
	}
	body := upsertVMBody{
		CloudAccountID:     accountID,
		ProviderVMID:       vm.ProviderVMID,
		Name:               vm.Name,
		PowerState:         vm.PowerState,
		Ready:              vm.PowerState == "running",
		DeletionProtection: vm.DeletionProtection,
		NICs:               vm.NICs,
		SecurityGroups:     sgJSON,
		BlockDevices:       vm.BlockDevices,
		Tags:               vm.Tags,
	}
	body.Role = stringPtrOrNil(vm.Role)
	body.PrivateIP = stringPtrOrNil(vm.PrivateIP)
	body.PublicIP = stringPtrOrNil(vm.PublicIP)
	body.PrivateDNSName = stringPtrOrNil(vm.PrivateDNSName)
	body.VPCID = stringPtrOrNil(vm.VPCID)
	body.SubnetID = stringPtrOrNil(vm.SubnetID)
	body.InstanceType = stringPtrOrNil(vm.InstanceType)
	body.Architecture = stringPtrOrNil(vm.Architecture)
	body.Zone = stringPtrOrNil(vm.Zone)
	body.Region = stringPtrOrNil(vm.Region)
	body.ImageID = stringPtrOrNil(vm.ImageID)
	body.ImageName = stringPtrOrNil(vm.ImageName)
	body.KeypairName = stringPtrOrNil(vm.KeypairName)
	body.BootMode = stringPtrOrNil(vm.BootMode)
	body.ProviderAccountID = stringPtrOrNil(vm.ProviderAccountID)
	body.StateReason = stringPtrOrNil(vm.StateReason)
	body.CapacityCPU = stringPtrOrNil(vm.CapacityCPU)
	body.CapacityMemory = stringPtrOrNil(vm.CapacityMemory)
	body.RootDeviceType = stringPtrOrNil(vm.RootDeviceType)
	body.RootDeviceName = stringPtrOrNil(vm.RootDeviceName)
	if !vm.ProviderCreationDate.IsZero() {
		s := vm.ProviderCreationDate.UTC().Format(time.RFC3339)
		body.ProviderCreationDate = &s
	}
	if err := s.doJSON(ctx, http.MethodPost, "/v1/virtual-machines", body, nil); err != nil {
		return err
	}
	return nil
}

// ReconcileVirtualMachines POSTs /v1/virtual-machines/reconcile.
func (s *Store) ReconcileVirtualMachines(ctx context.Context, accountID uuid.UUID, keep []string) (int64, error) {
	body := map[string]any{
		"cloud_account_id":     accountID,
		"keep_provider_vm_ids": keep,
	}
	var out struct {
		Tombstoned int64 `json:"tombstoned"`
	}
	if err := s.doJSON(ctx, http.MethodPost, "/v1/virtual-machines/reconcile", body, &out); err != nil {
		return 0, err
	}
	return out.Tombstoned, nil
}

// SGAttachment is one VM's SG attachment set sent on the sweep payload.
type SGAttachment struct {
	ProviderVMID  string   `json:"provider_vm_id"`
	ProviderSGIDs []string `json:"provider_sg_ids"`
}

// SweepSecurityGroups POSTs /v1/ingest/cloud-accounts/{id}/security-groups/sweep.
// seenProviderSGIDs drives delete-unseen; groups + attachments are the
// account-wide flow-matrix enrichment (persisted server-side only when the
// flow_matrix_enabled toggle is on).
func (s *Store) SweepSecurityGroups(
	ctx context.Context,
	accountID uuid.UUID,
	seenProviderSGIDs []string,
	groups []provider.SecurityGroup,
	attachments []SGAttachment,
) error {
	body := map[string]any{
		"seen_provider_sg_ids": seenProviderSGIDs,
		"groups":               groups,
		"attachments":          attachments,
	}
	path := "/v1/ingest/cloud-accounts/" + accountID.String() + "/security-groups/sweep"
	return s.doJSON(ctx, http.MethodPost, path, body, nil)
}

func stringPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// --- HTTP plumbing -------------------------------------------------------

func (s *Store) doJSON(ctx context.Context, method, path string, body, dst any) error {
	_, err := s.c.DoJSON(ctx, method, path, body, dst, mapVMError)
	return err
}

// mapVMError maps non-2xx statuses to the VM-collector sentinels,
// sniffing response bodies where one status carries two meanings
// (403 disabled-vs-forbidden, 409 kube-node-vs-generic conflict).
func mapVMError(method, path string, statusCode int, respBody []byte) (httptransport.Disposition, error) {
	truncated := func() string { return httptransport.Truncate(string(respBody), 200) }
	switch {
	case statusCode == http.StatusNotFound:
		return httptransport.Done, ErrNotRegistered
	case statusCode == http.StatusForbidden:
		// Could be account disabled or token mis-bound. Both are
		// terminal — return distinct sentinels so the collector logs
		// useful error text.
		if bytes.Contains(respBody, []byte("Account Disabled")) {
			return httptransport.Done, ErrAccountDisabled
		}
		return httptransport.Done, fmt.Errorf("%s %s: 403: %s: %w", method, path, truncated(), ErrHTTPForbidden)
	case statusCode == http.StatusConflict:
		// On POST /virtual-machines this means already-a-kube-node;
		// surface the dedicated sentinel so the collector can log and continue.
		if strings.HasPrefix(path, "/v1/virtual-machines") && bytes.Contains(respBody, []byte("already_inventoried_as_kubernetes_node")) {
			return httptransport.Done, ErrAlreadyKubeNode
		}
		return httptransport.Done, fmt.Errorf("%s %s: 409: %s: %w", method, path, truncated(), ErrHTTPConflict)
	case statusCode == http.StatusUnauthorized:
		slog.Error("apiclient: unauthorised, not retrying",
			slog.String("method", method), slog.String("path", path))
		return httptransport.Done, fmt.Errorf("%s %s: %w", method, path, ErrHTTPUnauthorized)
	case statusCode >= 500:
		slog.Warn("apiclient: transient 5xx, retrying",
			slog.String("method", method), slog.String("path", path),
			slog.Int("status", statusCode))
		return httptransport.Retry, fmt.Errorf("%s %s: %d %s: %w", method, path, statusCode, truncated(), ErrHTTPServerError)
	default:
		return httptransport.Done, fmt.Errorf("%s %s: %d %s: %w", method, path, statusCode, truncated(), ErrHTTPClientError)
	}
}
