package provider_test

import (
	"encoding/json"
	"testing"

	"github.com/sthalbert/longue-vue/internal/vmcollector/provider"
)

func TestSecurityGroupRoundTripJSON(t *testing.T) {
	one := 5432
	want := provider.SecurityGroup{
		ProviderSGID: "sg-abc",
		Name:         "pg-primary",
		VPCID:        "vpc-1",
		Tags:         map[string]string{"env": "prod"},
		Ingress: []provider.SecurityGroupRule{
			{
				Direction:   "ingress",
				Protocol:    "tcp",
				FromPort:    &one,
				ToPort:      &one,
				Peer:        provider.SGPeer{Kind: "cidr", CIDR: "10.0.0.0/16"},
				Description: "app tier",
			},
		},
	}
	buf, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got provider.SecurityGroup
	if err := json.Unmarshal(buf, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ProviderSGID != want.ProviderSGID ||
		len(got.Ingress) != 1 ||
		got.Ingress[0].Peer.Kind != "cidr" ||
		got.Ingress[0].FromPort == nil || *got.Ingress[0].FromPort != 5432 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestSchemaVersionConstant(t *testing.T) {
	if provider.SGSchemaVersion != 1 {
		t.Fatalf("expected SGSchemaVersion=1, got %d", provider.SGSchemaVersion)
	}
}

func TestVMSecurityGroupsPayloadRoundTrip(t *testing.T) {
	p := provider.VMSecurityGroupsPayload{
		Version: provider.SGSchemaVersion,
		Groups: []provider.SecurityGroup{{
			ProviderSGID: "sg-1",
			Name:         "n",
			Ingress:      []provider.SecurityGroupRule{},
			Egress:       []provider.SecurityGroupRule{},
		}},
	}
	buf, _ := json.Marshal(p)
	var got provider.VMSecurityGroupsPayload
	if err := json.Unmarshal(buf, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Version != 1 || len(got.Groups) != 1 || got.Groups[0].ProviderSGID != "sg-1" {
		t.Fatalf("payload: %+v", got)
	}
}
