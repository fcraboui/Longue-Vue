package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalPowerState(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"pending":       "pending",
		"running":       "running",
		"stopping":      "stopping",
		"stopped":       "stopped",
		"shutting-down": "terminating",
		"terminated":    "terminated",
		"":              "unknown",
		"weird":         "weird",
	}
	for input, want := range tests {
		if got := CanonicalPowerState(input); got != want {
			t.Errorf("CanonicalPowerState(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseInstanceTypeCapacity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		wantCPU string
		wantMem string
	}{
		{"tinav7.c4r8p2", "4", "8Gi"},
		{"tinav5.c2r4p1", "2", "4Gi"},
		{"tinav3.c16r32p3", "16", "32Gi"},
		{"m5.large", "", ""},
		{"unknown", "", ""},
		{"", "", ""},
	}
	for _, tc := range tests {
		gotCPU, gotMem := ParseInstanceTypeCapacity(tc.in)
		if gotCPU != tc.wantCPU || gotMem != tc.wantMem {
			t.Errorf("Parse(%q) = (%q, %q), want (%q, %q)",
				tc.in, gotCPU, gotMem, tc.wantCPU, tc.wantMem)
		}
	}
}

func TestDeriveRegionFromZone(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"eu-west-2b":           "eu-west-2",
		"eu-west-2a":           "eu-west-2",
		"us-east-1c":           "us-east-1",
		"cloudgouv-eu-west-1a": "cloudgouv-eu-west-1",
		"":                     "",
		"weird":                "",
		"eu-west-2":            "", // already a region, no zone suffix
	}
	for in, want := range tests {
		if got := DeriveRegionFromZone(in); got != want {
			t.Errorf("DeriveRegionFromZone(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestGetSecurityGroups verifies that GetSecurityGroups calls
// ReadSecurityGroups, marshals the response through
// NormalizeOutscaleSecurityGroups, and returns a canonical []SecurityGroup.
// The fixture is the same outscale_sg_native.json used by the normalize unit test.
func TestGetSecurityGroups(t *testing.T) {
	t.Parallel()
	fixture, err := os.ReadFile(filepath.Join("testdata", "outscale_sg_native.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	// The Outscale API wraps the response in {"SecurityGroups": [...]}
	// (same shape as the fixture). We re-wrap it as a ReadSecurityGroupsResponse
	// by embedding it verbatim — the SDK will decode it into the Go struct.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	p, err := NewOutscale("ak", "sk", "eu-west-2", srv.URL)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	got, err := p.GetSecurityGroups(context.Background())
	if err != nil {
		t.Fatalf("GetSecurityGroups: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 SG, got %d", len(got))
	}
	sg := got[0]
	if sg.ProviderSGID != "sg-12345678" {
		t.Errorf("ProviderSGID = %q, want %q", sg.ProviderSGID, "sg-12345678")
	}
	if len(sg.Ingress) != 2 || len(sg.Egress) != 1 {
		t.Errorf("rule counts: ing=%d eg=%d, want 2/1", len(sg.Ingress), len(sg.Egress))
	}
	// Spot-check the payload is version-stamped when embedded in VMSecurityGroupsPayload.
	payload := VMSecurityGroupsPayload{Version: SGSchemaVersion, Groups: got}
	b, _ := json.Marshal(payload)
	var roundtrip VMSecurityGroupsPayload
	if err := json.Unmarshal(b, &roundtrip); err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	if roundtrip.Version != SGSchemaVersion {
		t.Errorf("version = %d, want %d", roundtrip.Version, SGSchemaVersion)
	}
}
