package provider

import (
	"encoding/json"
	"fmt"
)

// outscaleNativeSG mirrors the subset of Outscale's ReadSecurityGroups
// response we consume. Keep this struct private — it is an implementation
// detail of the normalizer, not part of the canonical contract.
type outscaleNativeSG struct {
	SecurityGroupId   string               `json:"SecurityGroupId"`
	SecurityGroupName string               `json:"SecurityGroupName"`
	NetId             string               `json:"NetId"`
	Description       string               `json:"Description"`
	Tags              []outscaleNativeTag  `json:"Tags"`
	InboundRules      []outscaleNativeRule `json:"InboundRules"`
	OutboundRules     []outscaleNativeRule `json:"OutboundRules"`
}

type outscaleNativeTag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type outscaleNativeRule struct {
	FromPortRange         int                      `json:"FromPortRange"`
	ToPortRange           int                      `json:"ToPortRange"`
	IpProtocol            string                   `json:"IpProtocol"`
	IpRanges              []string                 `json:"IpRanges"`
	SecurityGroupsMembers []outscaleNativeSGMember `json:"SecurityGroupsMembers"`
}

type outscaleNativeSGMember struct {
	SecurityGroupId string `json:"SecurityGroupId"`
}

// NormalizeOutscaleSecurityGroups converts Outscale's native SG JSON into
// the provider-neutral canonical form. Inputs are the raw JSON bodies of
// individual SecurityGroup entries (already split by the caller).
//
// Outscale conventions handled here:
//   - IpProtocol "-1" or "" → canonical "any"
//   - FromPortRange == -1 → canonical FromPort == nil (any port)
//   - A rule with both an IpRange and a SecurityGroupsMembers entry is
//     split into two canonical rules (CIDR and sg_ref) — the canonical
//     SGPeer is single-kind.
func NormalizeOutscaleSecurityGroups(raw []json.RawMessage) ([]SecurityGroup, error) {
	out := make([]SecurityGroup, 0, len(raw))
	for i, r := range raw {
		var native outscaleNativeSG
		if err := json.Unmarshal(r, &native); err != nil {
			return nil, fmt.Errorf("outscale sg[%d]: %w", i, err)
		}
		sg := SecurityGroup{
			ProviderSGID: native.SecurityGroupId,
			Name:         native.SecurityGroupName,
			VPCID:        native.NetId,
			Ingress:      normalizeOutscaleRules(native.InboundRules, "ingress"),
			Egress:       normalizeOutscaleRules(native.OutboundRules, "egress"),
		}
		if len(native.Tags) > 0 {
			sg.Tags = make(map[string]string, len(native.Tags))
			for _, t := range native.Tags {
				sg.Tags[t.Key] = t.Value
			}
		}
		out = append(out, sg)
	}
	return out, nil
}

func normalizeOutscaleRules(in []outscaleNativeRule, dir string) []SecurityGroupRule {
	var out []SecurityGroupRule
	for _, r := range in {
		proto := canonicalOutscaleProtocol(r.IpProtocol)
		from, to := canonicalOutscalePortRange(r.FromPortRange, r.ToPortRange)
		for _, cidr := range r.IpRanges {
			out = append(out, SecurityGroupRule{
				Direction: dir,
				Protocol:  proto,
				FromPort:  from,
				ToPort:    to,
				Peer:      SGPeer{Kind: SGPeerKindCIDR, CIDR: cidr},
			})
		}
		for _, m := range r.SecurityGroupsMembers {
			out = append(out, SecurityGroupRule{
				Direction: dir,
				Protocol:  proto,
				FromPort:  from,
				ToPort:    to,
				Peer:      SGPeer{Kind: SGPeerKindSGRef, ProviderSGID: m.SecurityGroupId},
			})
		}
	}
	return out
}

func canonicalOutscaleProtocol(p string) string {
	switch p {
	case "-1", "":
		return "any"
	default:
		return p
	}
}

func canonicalOutscalePortRange(from, to int) (*int, *int) {
	if from < 0 && to < 0 {
		return nil, nil
	}
	f := from
	t := to
	return &f, &t
}
