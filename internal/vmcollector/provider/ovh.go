package provider

import "context"

// OVHProvider is the OVH implementation of Provider. It is a SKELETON in
// P1: every method panics with "not wired". The type exists so the
// canonical Provider interface stays honest at compile time and the
// OVH-shaped fixture stays in the test tree for later real impl.
type OVHProvider struct{}

func NewOVHProviderSkeleton() *OVHProvider { return &OVHProvider{} }

func (*OVHProvider) Kind() string { return "ovh" }

func (*OVHProvider) ListVMs(ctx context.Context) ([]VM, error) {
	panic("ovh provider: not wired (P1 skeleton; see ADR-0031)")
}

func (*OVHProvider) GetSecurityGroups(ctx context.Context) ([]SecurityGroup, error) {
	panic("ovh provider: not wired (P1 skeleton; see ADR-0031)")
}
