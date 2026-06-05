package provider

import "context"

// AWSProvider is the AWS implementation of Provider. It is a SKELETON in
// P1: every method panics with "not wired". The type exists so the
// canonical Provider interface stays honest at compile time and the
// AWS-shaped fixture stays in the test tree for later real impl.
type AWSProvider struct{}

func NewAWSProviderSkeleton() *AWSProvider { return &AWSProvider{} }

func (*AWSProvider) Kind() string { return "aws" }

func (*AWSProvider) ListVMs(ctx context.Context) ([]VM, error) {
	panic("aws provider: not wired (P1 skeleton; see ADR-0031)")
}

func (*AWSProvider) GetSecurityGroups(ctx context.Context) ([]SecurityGroup, error) {
	panic("aws provider: not wired (P1 skeleton; see ADR-0031)")
}
