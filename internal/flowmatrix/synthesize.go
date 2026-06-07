package flowmatrix

// Synthesize is the pure comparator: given already-loaded inputs it produces
// the per-cluster synthesis. No I/O — the handler loads Inputs from the Store.
//
//nolint:gocritic // hugeParam: Inputs passed by value keeps the public read-time comparator API simple; called once per request.
func Synthesize(in Inputs) Synthesis {
	m := NewEndpointGroupMatcher(in.Groups)
	out := Synthesis{ClusterID: in.ClusterID}

	perimRefPorts := map[string][]PortRange{}
	for i := range in.References {
		r := &in.References[i]
		if r.Layer != "perimeter" {
			continue
		}
		zone := r.SrcRef
		if r.DstKind == "endpoint_group" {
			zone = r.DstRef
		}
		key := r.Direction + "|" + zone
		perimRefPorts[key] = append(perimRefPorts[key], r.Port)
	}

	for _, sg := range in.PerimeterRules {
		zone := m.Match(sg.PeerCIDR)
		refs := perimRefPorts[sg.Direction+"|"+zone]
		state := ClassifyActual(sg.Port, refs, sg.WideOpen)
		out.Perimeter = append(out.Perimeter, Flow{
			Layer:     "perimeter",
			Direction: sg.Direction,
			Src:       Endpoint{Kind: "endpoint_group", Name: zone, CIDR: sg.PeerCIDR},
			Dst:       Endpoint{Kind: "namespace", Name: "cluster"},
			Ports:     []PortRange{sg.Port},
			State:     state,
			Sources:   []Source{{Kind: "sg_rule", ID: sg.RuleID, Summary: sg.Summary}},
		})
	}

	intRefPorts := map[string][]PortRange{}
	for i := range in.References {
		r := &in.References[i]
		if r.Layer != "internal" {
			continue
		}
		key := r.Direction + "|" + r.SrcRef + "|" + r.DstRef
		intRefPorts[key] = append(intRefPorts[key], r.Port)
	}

	for i := range in.InternalRules {
		np := &in.InternalRules[i]
		refs := intRefPorts[np.Direction+"|"+np.SrcWorkload+"|"+np.DstWorkload]
		wideOpen := np.WideOpenSide != ""
		state := ClassifyActual(np.Port, refs, wideOpen)
		out.Internal = append(out.Internal, Flow{
			Layer:     "internal",
			Direction: np.Direction,
			Src:       Endpoint{Kind: "workload", Name: np.SrcWorkload},
			Dst:       Endpoint{Kind: "workload", Name: np.DstWorkload},
			Ports:     []PortRange{np.Port},
			State:     state,
			Sources:   []Source{{Kind: "netpol_rule", ID: np.RuleID, Summary: np.Summary}},
		})
	}

	out.Perimeter = appendUnimplemented(out.Perimeter, in.References, "perimeter", perimActuals(&in, m))
	out.Internal = appendUnimplemented(out.Internal, in.References, "internal", intActuals(&in))

	return out
}

func perimActuals(in *Inputs, m *EndpointGroupMatcher) map[string][]PortRange {
	res := map[string][]PortRange{}
	for i := range in.PerimeterRules {
		sg := &in.PerimeterRules[i]
		zone := m.Match(sg.PeerCIDR)
		key := sg.Direction + "|" + zone
		res[key] = append(res[key], sg.Port)
	}
	return res
}

func intActuals(in *Inputs) map[string][]PortRange {
	res := map[string][]PortRange{}
	for i := range in.InternalRules {
		np := &in.InternalRules[i]
		key := np.Direction + "|" + np.SrcWorkload + "|" + np.DstWorkload
		res[key] = append(res[key], np.Port)
	}
	return res
}

func appendUnimplemented(flows []Flow, refs []Reference, layer string, actuals map[string][]PortRange) []Flow {
	for i := range refs {
		r := &refs[i]
		if r.Layer != layer {
			continue
		}
		var key string
		if layer == "perimeter" {
			zone := r.SrcRef
			if r.DstKind == "endpoint_group" {
				zone = r.DstRef
			}
			key = r.Direction + "|" + zone
		} else {
			key = r.Direction + "|" + r.SrcRef + "|" + r.DstRef
		}
		if ClassifyReference(r.Port, actuals[key]) == StateManquant {
			id := r.ID
			flows = append(flows, Flow{
				Layer:       layer,
				Direction:   r.Direction,
				Src:         Endpoint{Kind: r.SrcKind, Name: r.SrcRef},
				Dst:         Endpoint{Kind: r.DstKind, Name: r.DstRef},
				Ports:       []PortRange{r.Port},
				State:       StateManquant,
				ReferenceID: &id,
				Sources:     []Source{{Kind: "reference", ID: r.ID, Summary: "declared but not implemented"}},
			})
		}
	}
	return flows
}
