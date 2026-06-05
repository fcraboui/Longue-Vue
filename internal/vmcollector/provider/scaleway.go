package provider

import "context"

// ScalewayProvider is the Scaleway implementation of Provider. It is a
// SKELETON in P1: every method panics with "not wired". The type exists so
// the canonical Provider interface stays honest at compile time and the
// Scaleway-shaped fixture stays in the test tree for later real impl.
type ScalewayProvider struct{}

func NewScalewayProviderSkeleton() *ScalewayProvider { return &ScalewayProvider{} }

func (*ScalewayProvider) Kind() string { return "scaleway" }

func (*ScalewayProvider) ListVMs(ctx context.Context) ([]VM, error) {
	panic("scaleway provider: not wired (P1 skeleton; see ADR-0031)")
}

func (*ScalewayProvider) GetSecurityGroups(ctx context.Context) ([]SecurityGroup, error) {
	panic("scaleway provider: not wired (P1 skeleton; see ADR-0031)")
}
