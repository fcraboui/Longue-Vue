package flowmatrix

import "net/netip"

// Uncategorized is the pseudo-group name for a CIDR contained by no group.
const Uncategorized = "Externe (non catégorisé)"

type matcherEntry struct {
	prefix netip.Prefix
	group  string
}

// EndpointGroupMatcher resolves a rule CIDR to the name of the most-specific
// endpoint group whose CIDR fully contains it.
type EndpointGroupMatcher struct {
	entries []matcherEntry
}

// NewEndpointGroupMatcher flattens groups into prefix entries. Invalid CIDRs
// are skipped (they cannot match anything).
func NewEndpointGroupMatcher(groups []NamedCIDRGroup) *EndpointGroupMatcher {
	m := &EndpointGroupMatcher{}
	for _, g := range groups {
		for _, c := range g.CIDRs {
			pfx, err := netip.ParsePrefix(c)
			if err != nil {
				continue
			}
			m.entries = append(m.entries, matcherEntry{prefix: pfx.Masked(), group: g.Name})
		}
	}
	return m
}

// Match returns the most-specific (longest-prefix) group name containing the
// given CIDR, or Uncategorized if none contains it.
func (m *EndpointGroupMatcher) Match(cidr string) string {
	target, err := netip.ParsePrefix(cidr)
	if err != nil {
		return Uncategorized
	}
	target = target.Masked()
	best := Uncategorized
	bestBits := -1
	for _, e := range m.entries {
		if e.prefix.Addr().Is4() != target.Addr().Is4() {
			continue
		}
		if e.prefix.Bits() <= target.Bits() && e.prefix.Contains(target.Addr()) {
			if e.prefix.Bits() > bestBits {
				bestBits = e.prefix.Bits()
				best = e.group
			}
		}
	}
	return best
}
