package flowmatrix

import "testing"

func p(n int) *int { return &n }

func TestPortRangeCovers(t *testing.T) {
	cases := []struct {
		name   string
		ref    PortRange
		actual PortRange
		want   bool
	}{
		{"exact", PortRange{"tcp", p(443), p(443)}, PortRange{"tcp", p(443), p(443)}, true},
		{"ref-wider", PortRange{"tcp", p(1), p(65535)}, PortRange{"tcp", p(443), p(443)}, true},
		{"ref-narrower", PortRange{"tcp", p(443), p(443)}, PortRange{"tcp", p(1), p(65535)}, false},
		{"proto-mismatch", PortRange{"tcp", p(443), p(443)}, PortRange{"udp", p(443), p(443)}, false},
		{"any-proto-ref", PortRange{"any", nil, nil}, PortRange{"tcp", p(443), p(443)}, true},
		{"any-port-ref", PortRange{"tcp", nil, nil}, PortRange{"tcp", p(443), p(443)}, true},
	}
	for _, c := range cases {
		if got := refCovers(c.ref, c.actual); got != c.want {
			t.Errorf("%s: refCovers = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestClassifyActual(t *testing.T) {
	ref := []PortRange{{"tcp", p(443), p(443)}}
	if got := ClassifyActual(PortRange{"tcp", p(443), p(443)}, ref, false); got != StateConforme {
		t.Errorf("covered actual = %q, want conforme", got)
	}
	if got := ClassifyActual(PortRange{"tcp", p(8080), p(8080)}, ref, false); got != StateNonDeclare {
		t.Errorf("uncovered actual = %q, want non_declare", got)
	}
	if got := ClassifyActual(PortRange{"tcp", p(443), p(443)}, ref, true); got != StateLargeOuvert {
		t.Errorf("wide-open actual = %q, want large_ouvert", got)
	}
}

func TestClassifyReferenceUnimplemented(t *testing.T) {
	if got := ClassifyReference(PortRange{"tcp", p(443), p(443)}, nil); got != StateManquant {
		t.Errorf("unimplemented reference = %q, want manquant", got)
	}
	actual := []PortRange{{"tcp", p(443), p(443)}}
	if got := ClassifyReference(PortRange{"tcp", p(443), p(443)}, actual); got != StateConforme {
		t.Errorf("implemented reference = %q, want conforme", got)
	}
}

func TestRefCovers_ActualOpenEndUncovered(t *testing.T) {
	// Reference has a bounded range; actual has an unbounded (nil) endpoint -> not covered.
	if refCovers(PortRange{"tcp", p(1), p(100)}, PortRange{"tcp", nil, p(100)}) {
		t.Errorf("actual with nil From should not be covered by bounded ref")
	}
}
