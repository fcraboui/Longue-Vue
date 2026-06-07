package flowmatrix

// refCovers reports whether reference port-range `ref` fully authorizes actual
// port-range `actual`. "any" protocol on the reference matches any protocol;
// nil From/To on the reference means "any port".
func refCovers(ref, actual PortRange) bool {
	if ref.Protocol != "any" && ref.Protocol != actual.Protocol {
		return false
	}
	if ref.From == nil || ref.To == nil {
		return true
	}
	if actual.From == nil || actual.To == nil {
		return false
	}
	return *ref.From <= *actual.From && *actual.To <= *ref.To
}

// ClassifyActual classifies a discovered (actual) flow. wideOpen forces
// large_ouvert (K8s default-allow or a 0.0.0.0/0 SG peer). Otherwise the flow
// is conforme when some reference covers it, else non_declare.
func ClassifyActual(actual PortRange, references []PortRange, wideOpen bool) State {
	if wideOpen {
		return StateLargeOuvert
	}
	for _, r := range references {
		if refCovers(r, actual) {
			return StateConforme
		}
	}
	return StateNonDeclare
}

// ClassifyReference classifies a declared reference row against the actual
// flows for the same endpoint pair. If any actual flow satisfies the reference,
// it is conforme (the actual side already emitted the row); otherwise manquant.
func ClassifyReference(ref PortRange, actuals []PortRange) State {
	for _, a := range actuals {
		if refCovers(ref, a) {
			return StateConforme
		}
	}
	return StateManquant
}
