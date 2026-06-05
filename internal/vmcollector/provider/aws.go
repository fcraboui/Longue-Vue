package provider

import "context"

// AWSProvider is the AWS implementation of Provider. It is a SKELETON in
// P1: every method panics with "not wired". The type exists so the
// canonical Provider interface stays honest at compile time and the
// AWS-shaped fixture stays in the test tree for later real impl.
type AWSProvider struct{}

// NewAWSProviderSkeleton returns an uninitialised AWSProvider for compile-time
// interface verification. All methods panic — wire a real implementation in P2.
func NewAWSProviderSkeleton() *AWSProvider { return &AWSProvider{} }

// Kind returns the cloud provider identifier "aws".
func (*AWSProvider) Kind() string { return "aws" }

// ListVMs panics — AWS provider is a P1 skeleton; see ADR-0031.
func (*AWSProvider) ListVMs(ctx context.Context) ([]VM, error) {
	panic("aws provider: not wired (P1 skeleton; see ADR-0031)")
}

// GetSecurityGroups panics — AWS provider is a P1 skeleton; see ADR-0031.
func (*AWSProvider) GetSecurityGroups(ctx context.Context) ([]SecurityGroup, error) {
	panic("aws provider: not wired (P1 skeleton; see ADR-0031)")
}
