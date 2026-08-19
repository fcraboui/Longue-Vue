package collector

// Unit tests for the pure Kyverno conversion helpers (PR #250 review
// fixes): action classification for non-validate policies, per-rule
// failureActionOverrides, summary-count clamping, JSON-null
// normalisation, and RBAC-forbidden list skipping.

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func kyvernoPolicyObj(spec map[string]interface{}) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"metadata": map[string]interface{}{"name": "p"},
		"spec":     spec,
	}}
}

func TestKyvernoAction_GenerateOnlyPolicyHasNoAction(t *testing.T) {
	// A mutate-/generate-only policy has no validation semantics;
	// reporting the kubebuilder default "audit" fabricates an
	// enforcement posture the policy doesn't have.
	obj := kyvernoPolicyObj(map[string]interface{}{
		"rules": []interface{}{
			map[string]interface{}{
				"name":     "gen-ns-quota",
				"generate": map[string]interface{}{"kind": "ResourceQuota"},
			},
		},
	})
	if got := kyvernoAction(obj); got != nil {
		t.Errorf("action: got %q, want nil for generate-only policy", *got)
	}
}

func TestKyvernoAction_MutateOnlyPolicyHasNoAction(t *testing.T) {
	obj := kyvernoPolicyObj(map[string]interface{}{
		"rules": []interface{}{
			map[string]interface{}{
				"name":   "add-label",
				"mutate": map[string]interface{}{},
			},
		},
	})
	if got := kyvernoAction(obj); got != nil {
		t.Errorf("action: got %q, want nil for mutate-only policy", *got)
	}
}

func TestKyvernoAction_ValidateRuleDefaultsToAudit(t *testing.T) {
	obj := kyvernoPolicyObj(map[string]interface{}{
		"rules": []interface{}{
			map[string]interface{}{
				"name":     "check-labels",
				"validate": map[string]interface{}{"message": "labels required"},
			},
		},
	})
	got := kyvernoAction(obj)
	if got == nil || *got != "audit" {
		t.Errorf("action: got %v, want audit", got)
	}
}

func TestKyvernoAction_PerRuleFailureActionOverridesEnforce(t *testing.T) {
	// Kyverno >=1.13: a rule can enforce solely through
	// validate.failureActionOverrides.
	obj := kyvernoPolicyObj(map[string]interface{}{
		"rules": []interface{}{
			map[string]interface{}{
				"name": "check-labels",
				"validate": map[string]interface{}{
					"failureActionOverrides": []interface{}{
						map[string]interface{}{"action": "Enforce", "namespaces": []interface{}{"prod"}},
					},
				},
			},
		},
	})
	got := kyvernoAction(obj)
	if got == nil || *got != "enforce" {
		t.Errorf("action: got %v, want enforce (per-rule failureActionOverrides)", got)
	}
}

func TestKyvernoAction_SpecLevelEnforceStillWins(t *testing.T) {
	obj := kyvernoPolicyObj(map[string]interface{}{
		"validationFailureAction": "Enforce",
		"rules": []interface{}{
			map[string]interface{}{
				"name":     "check-labels",
				"validate": map[string]interface{}{},
			},
		},
	})
	got := kyvernoAction(obj)
	if got == nil || *got != "enforce" {
		t.Errorf("action: got %v, want enforce", got)
	}
}

func TestJsonInt_ClampsNegative(t *testing.T) {
	if got := jsonInt(map[string]interface{}{"pass": float64(-3)}, "pass"); got != 0 {
		t.Errorf("negative: got %d, want 0", got)
	}
	if got := jsonInt(map[string]interface{}{"pass": -7}, "pass"); got != 0 {
		t.Errorf("negative int: got %d, want 0", got)
	}
}

func TestJsonInt_ClampsHugeFloat(t *testing.T) {
	if got := jsonInt(map[string]interface{}{"pass": 1e300}, "pass"); got != math.MaxInt32 {
		t.Errorf("huge float: got %d, want %d", got, math.MaxInt32)
	}
	if got := jsonInt(map[string]interface{}{"pass": int64(math.MaxInt64)}, "pass"); got != math.MaxInt32 {
		t.Errorf("huge int64: got %d, want %d", got, math.MaxInt32)
	}
}

func TestKyvernoReportToRow_JSONNullResultsBecomesNil(t *testing.T) {
	// json.Marshal of a missing results field yields the literal "null",
	// which is valid JSON and used to sail through the guard into the
	// store as jsonb null. The store maps nil to the schema's [].
	info := &KyvernoPolicyReportInfo{Name: "r", ResultsRaw: []byte("null")}
	row := kyvernoReportToRow(info, uuid.New(), nil)
	if row.ResultsRaw != nil {
		t.Errorf("results_raw: got %q, want nil", row.ResultsRaw)
	}
}

func TestKyvernoPolicyToRow_JSONNullAnnotationsAndSpec(t *testing.T) {
	info := &KyvernoClusterPolicyInfo{
		Name:         "p",
		ResourceType: "ClusterPolicy",
		Scope:        "cluster",
		Annotations:  []byte("null"),
		SpecRaw:      []byte("null"),
	}
	row := kyvernoPolicyToRow(info, uuid.New(), nil)
	if row.Annotations != nil {
		t.Errorf("annotations: got %q, want nil (SQL NULL in the nullable column)", row.Annotations)
	}
	if string(row.SpecRaw) != "{}" {
		t.Errorf("spec_raw: got %q, want {} (column is NOT NULL)", row.SpecRaw)
	}
}

func TestNew_LogsWhenStoreLacksKyvernoSupport(t *testing.T) {
	// Push-mode Kyverno collection is deferred (ADR-0043 NEG-008): the
	// apiclient store implements neither KyvernoStore nor SettingsGetter,
	// so collection silently no-ops. The operator deserves one startup
	// line saying so instead of debugging RBAC and CRDs for nothing.
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	c := New(newFakeStore(), nil, "c1", time.Second, time.Second, false)
	if c.kyvernoStore != nil {
		t.Fatal("fakeStore unexpectedly implements KyvernoStore; test premise broken")
	}
	if !strings.Contains(buf.String(), "kyverno policy collection disabled") {
		t.Errorf("want startup log about kyverno collection disabled, got: %s", buf.String())
	}
}

func TestKyvernoListSkip_NotFoundAndForbiddenSkip(t *testing.T) {
	gr := schema.GroupResource{Group: "kyverno.io", Resource: "clusterpolicies"}
	gvr := gvrClusterPolicy
	if !kyvernoListSkip(apierrors.NewNotFound(gr, ""), gvr) {
		t.Error("NotFound (CRD absent): want skip=true")
	}
	if !kyvernoListSkip(apierrors.NewForbidden(gr, "", errors.New("rbac denied")), gvr) {
		t.Error("Forbidden (RBAC): want skip=true")
	}
	if kyvernoListSkip(fmt.Errorf("connection refused"), gvr) {
		t.Error("generic error: want skip=false (hard list failure)")
	}
}
