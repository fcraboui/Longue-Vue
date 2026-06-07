package flowmatrix

import "testing"

func TestMatchEndpointGroup_MostSpecificWins(t *testing.T) {
	groups := []NamedCIDRGroup{
		{Name: "Internet", CIDRs: []string{"0.0.0.0/0"}},
		{Name: "Corp-LAN", CIDRs: []string{"10.0.0.0/8"}},
		{Name: "Admin-subnet", CIDRs: []string{"10.1.2.0/24"}},
	}
	m := NewEndpointGroupMatcher(groups)

	cases := []struct {
		cidr string
		want string
	}{
		{"10.1.2.5/32", "Admin-subnet"},
		{"10.9.0.0/16", "Corp-LAN"},
		{"203.0.113.7/32", "Internet"},
	}
	for _, c := range cases {
		if got := m.Match(c.cidr); got != c.want {
			t.Errorf("Match(%s) = %q, want %q", c.cidr, got, c.want)
		}
	}

	m2 := NewEndpointGroupMatcher([]NamedCIDRGroup{{Name: "Corp-LAN", CIDRs: []string{"10.0.0.0/8"}}})
	if got := m2.Match("8.8.8.8/32"); got != Uncategorized {
		t.Errorf("Match(8.8.8.8/32) = %q, want %q", got, Uncategorized)
	}
}

func TestMatchEndpointGroup_SameFamilyNotContained(t *testing.T) {
	// A query in the same IPv4 family as the group but outside its range must
	// fall through to Uncategorized rather than spuriously matching.
	m := NewEndpointGroupMatcher([]NamedCIDRGroup{
		{Name: "Corp-LAN", CIDRs: []string{"192.168.0.0/16"}},
	})
	if got := m.Match("10.0.0.0/8"); got != Uncategorized {
		t.Errorf("Match(10.0.0.0/8) = %q, want %q", got, Uncategorized)
	}

	// Cross-family query (IPv6) against an IPv4-only group is skipped by family.
	if got := m.Match("2001:db8::/32"); got != Uncategorized {
		t.Errorf("Match(2001:db8::/32) = %q, want %q", got, Uncategorized)
	}
}

func TestMatchEndpointGroup_InvalidInputs(t *testing.T) {
	// Invalid CIDR in a group is skipped; invalid query CIDR returns Uncategorized.
	m := NewEndpointGroupMatcher([]NamedCIDRGroup{
		{Name: "Bad", CIDRs: []string{"not-a-cidr", ""}},
		{Name: "Corp-LAN", CIDRs: []string{"10.0.0.0/8"}},
	})
	if got := m.Match("not-a-cidr"); got != Uncategorized {
		t.Errorf("Match(invalid) = %q, want %q", got, Uncategorized)
	}
	if got := m.Match("10.0.0.5/32"); got != "Corp-LAN" {
		t.Errorf("Match(10.0.0.5/32) = %q, want Corp-LAN", got)
	}
}
