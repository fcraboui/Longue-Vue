package flowmatrix

import "testing"

func TestSynthesize_PerimeterConformeAndDrift(t *testing.T) {
	in := Inputs{
		ClusterID: "c1",
		Groups: []NamedCIDRGroup{
			{Name: "Internet", CIDRs: []string{"0.0.0.0/0"}},
			{Name: "Corp-LAN", CIDRs: []string{"10.0.0.0/8"}},
		},
		PerimeterRules: []SGRule{
			{RuleID: "sg1", Direction: "ingress", Port: PortRange{"tcp", p(443), p(443)}, PeerCIDR: "0.0.0.0/0", WideOpen: true, Summary: "sg ingress tcp/443 from 0.0.0.0/0"},
			{RuleID: "sg2", Direction: "ingress", Port: PortRange{"tcp", p(22), p(22)}, PeerCIDR: "10.0.0.0/8", Summary: "sg ingress tcp/22 from 10.0.0.0/8"},
		},
		References: []Reference{
			{ID: "r1", Layer: "perimeter", Direction: "ingress", SrcKind: "endpoint_group", SrcRef: "Internet", DstKind: "service", DstRef: "ingress-nginx", Port: PortRange{"tcp", p(22), p(22)}},
		},
	}

	out := Synthesize(in)

	if len(out.Perimeter) == 0 {
		t.Fatalf("expected perimeter flows")
	}
	if st := findState(out.Perimeter, "sg1"); st != StateLargeOuvert {
		t.Errorf("sg1 state = %q, want large_ouvert", st)
	}
	if st := findState(out.Perimeter, "sg2"); st != StateNonDeclare {
		t.Errorf("sg2 state = %q, want non_declare", st)
	}
}

func TestSynthesize_InternalConformeAndManquant(t *testing.T) {
	in := Inputs{
		ClusterID: "c1",
		InternalRules: []NetPolRule{
			// Matches reference r-int1 -> conforme on the actual side.
			{RuleID: "np1", Direction: "ingress", Port: PortRange{"tcp", p(8080), p(8080)}, SrcWorkload: "frontend", DstWorkload: "backend", Summary: "np ingress tcp/8080 frontend->backend"},
		},
		References: []Reference{
			// Implemented by np1 -> conforme on the actual row, no manquant.
			{ID: "r-int1", Layer: "internal", Direction: "ingress", SrcKind: "workload", SrcRef: "frontend", DstKind: "workload", DstRef: "backend", Port: PortRange{"tcp", p(8080), p(8080)}},
			// No matching actual NetPol rule -> emits a manquant row.
			{ID: "r-int2", Layer: "internal", Direction: "ingress", SrcKind: "workload", SrcRef: "frontend", DstKind: "workload", DstRef: "database", Port: PortRange{"tcp", p(5432), p(5432)}},
		},
	}

	out := Synthesize(in)

	// The actual np1 flow should be conforme (covered by r-int1).
	if st := findState(out.Internal, "np1"); st != StateConforme {
		t.Errorf("np1 state = %q, want conforme", st)
	}

	// r-int2 has no actual implementation -> a manquant reference flow.
	var found bool
	for _, f := range out.Internal {
		if f.ReferenceID != nil && *f.ReferenceID == "r-int2" {
			found = true
			if f.State != StateManquant {
				t.Errorf("r-int2 state = %q, want manquant", f.State)
			}
		}
		// r-int1 is implemented, so it must NOT produce a manquant reference row.
		if f.ReferenceID != nil && *f.ReferenceID == "r-int1" {
			t.Errorf("r-int1 should not emit an unimplemented reference flow")
		}
	}
	if !found {
		t.Errorf("expected a manquant reference flow for r-int2")
	}
}

func findState(flows []Flow, sourceID string) State {
	for _, f := range flows {
		for _, s := range f.Sources {
			if s.ID == sourceID {
				return f.State
			}
		}
	}
	return ""
}
