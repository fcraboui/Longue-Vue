package api

// Push-collector write handlers for NetworkPolicies (ADR-0038).
//
// The GET handlers live in network_policy_handlers.go (ADR-0034); these are
// the writes that close the gap left by "Read-only API surface" for clusters
// collected by cmd/longue-vue-collector through the DMZ ingest GW.

import (
	"context"
	"fmt"
)

// CreateNetworkPolicy is the strict-server impl for POST /v1/network-policies.
// Real implementation lands in Task 3 — this stub returns a non-nil error so
// the build compiles. The strict-server runtime surfaces the error as a 500.
func (s *Server) CreateNetworkPolicy(
	_ context.Context, _ CreateNetworkPolicyRequestObject,
) (CreateNetworkPolicyResponseObject, error) {
	return nil, fmt.Errorf("netpol push handler not yet implemented (Task 3)")
}

// ReconcileNetworkPolicies is the strict-server impl for POST
// /v1/network-policies/reconcile. Real implementation lands in Task 4.
func (s *Server) ReconcileNetworkPolicies(
	_ context.Context, _ ReconcileNetworkPoliciesRequestObject,
) (ReconcileNetworkPoliciesResponseObject, error) {
	return nil, fmt.Errorf("netpol reconcile handler not yet implemented (Task 4)")
}
