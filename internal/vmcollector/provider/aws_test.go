package provider_test

import (
	"context"
	"testing"

	"github.com/sthalbert/longue-vue/internal/vmcollector/provider"
)

func TestAWSProviderSkeletonPanicsOnGetSecurityGroups(t *testing.T) {
	p := provider.NewAWSProviderSkeleton()
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic from skeleton, got nil")
		}
	}()
	_, _ = p.GetSecurityGroups(context.Background())
}
