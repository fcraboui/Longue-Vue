package apiclient_test

import (
	"github.com/sthalbert/longue-vue/internal/collector"
	"github.com/sthalbert/longue-vue/internal/collector/apiclient"
)

// Compile-time assertion only — no test body needed.
// If this fails to compile, the push collector silently regressed to
// no-op netpol collection (the type assertion in collector.go would
// fail and ingestNetworkPolicies would return early). ADR-0038.
var _ collector.NetPolStore = (*apiclient.Store)(nil)
