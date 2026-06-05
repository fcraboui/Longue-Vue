package provider

import "context"

// ScalewayProvider is the Scaleway implementation of Provider. It is a
// SKELETON in P1: every method panics with "not wired". The type exists so
// the canonical Provider interface stays honest at compile time and the
// Scaleway-shaped fixture stays in the test tree for later real impl.
type ScalewayProvider struct{}

// NewScalewayProviderSkeleton returns an uninitialised ScalewayProvider for compile-time
// interface verification. All methods panic — wire a real implementation in P2.
func NewScalewayProviderSkeleton() *ScalewayProvider { return &ScalewayProvider{} }

// Kind returns the cloud provider identifier "scaleway".
func (*ScalewayProvider) Kind() string { return "scaleway" }

// ListVMs panics — Scaleway provider is a P1 skeleton; see ADR-0031.
func (*ScalewayProvider) ListVMs(ctx context.Context) ([]VM, error) {
	panic("scaleway provider: not wired (P1 skeleton; see ADR-0031)")
}

// GetSecurityGroups panics — Scaleway provider is a P1 skeleton; see ADR-0031.
func (*ScalewayProvider) GetSecurityGroups(ctx context.Context) ([]SecurityGroup, error) {
	panic("scaleway provider: not wired (P1 skeleton; see ADR-0031)")
}
