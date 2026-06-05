package provider_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sthalbert/longue-vue/internal/vmcollector/provider"
)

func TestNormalizeOutscaleSecurityGroups(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "outscale_sg_native.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var native struct {
		SecurityGroups []json.RawMessage `json:"SecurityGroups"`
	}
	if err := json.Unmarshal(raw, &native); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got, err := provider.NormalizeOutscaleSecurityGroups(native.SecurityGroups)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 SG, got %d", len(got))
	}
	sg := got[0]
	if sg.ProviderSGID != "sg-12345678" || sg.Name != "pg-primary" || sg.VPCID != "vpc-aaa" {
		t.Fatalf("bad metadata: %+v", sg)
	}
	if len(sg.Ingress) != 2 || len(sg.Egress) != 1 {
		t.Fatalf("rule counts: ing=%d eg=%d", len(sg.Ingress), len(sg.Egress))
	}
	// Ingress[0]: tcp 5432 from 10.0.0.0/16
	r0 := sg.Ingress[0]
	if r0.Protocol != "tcp" || r0.FromPort == nil || *r0.FromPort != 5432 ||
		r0.Peer.Kind != "cidr" || r0.Peer.CIDR != "10.0.0.0/16" {
		t.Fatalf("rule[0]: %+v", r0)
	}
	// Ingress[1]: icmp from sg-87654321 (sg_ref); ports nil (FromPortRange = -1 → "any")
	r1 := sg.Ingress[1]
	if r1.Protocol != "icmp" || r1.FromPort != nil || r1.Peer.Kind != "sg_ref" ||
		r1.Peer.ProviderSGID != "sg-87654321" {
		t.Fatalf("rule[1]: %+v", r1)
	}
	// Egress[0]: "-1" protocol → "any"; CIDR 0.0.0.0/0
	r2 := sg.Egress[0]
	if r2.Protocol != "any" || r2.Peer.Kind != "cidr" || r2.Peer.CIDR != "0.0.0.0/0" {
		t.Fatalf("rule[2]: %+v", r2)
	}
	if sg.Tags["env"] != "prod" {
		t.Fatalf("tags: %+v", sg.Tags)
	}
}
