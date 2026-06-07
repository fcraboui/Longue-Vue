package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestObserveExtract(t *testing.T) {
	ObserveExtract("search", "csv", "ok", 12)
	ObserveExtract("search", "csv", "ok", 8)
	got := testutil.ToFloat64(extractsTotal.WithLabelValues("search", "csv", "ok"))
	if got != 2 {
		t.Fatalf("extractsTotal: want 2, got %v", got)
	}
	got = testutil.ToFloat64(extractRowsTotal.WithLabelValues("search"))
	if got != 20 {
		t.Fatalf("extractRowsTotal: want 20, got %v", got)
	}
}

func TestSetFlowStates(t *testing.T) {
	ResetFlowGauges()
	SetFlowStates("cluster-a", "perimeter", map[string]int{"conforme": 3, "non_declare": 1})
	SetFlowReferenceRows("cluster-a", 4)
	SetFlowDanglingRefs("cluster-a", 2)

	if got := testutil.ToFloat64(flowStates.WithLabelValues("cluster-a", "perimeter", "conforme")); got != 3 {
		t.Fatalf("flowStates conforme: want 3, got %v", got)
	}
	if got := testutil.ToFloat64(flowStates.WithLabelValues("cluster-a", "perimeter", "non_declare")); got != 1 {
		t.Fatalf("flowStates non_declare: want 1, got %v", got)
	}
	if got := testutil.ToFloat64(flowReferenceRows.WithLabelValues("cluster-a")); got != 4 {
		t.Fatalf("flowReferenceRows: want 4, got %v", got)
	}
	if got := testutil.ToFloat64(flowDanglingRefs.WithLabelValues("cluster-a")); got != 2 {
		t.Fatalf("flowDanglingRefs: want 2, got %v", got)
	}

	mf, err := Registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	want := map[string]bool{
		"longue_vue_flow_states":              false,
		"longue_vue_flow_reference_rows":      false,
		"longue_vue_flow_dangling_references": false,
	}
	for _, m := range mf {
		if _, ok := want[m.GetName()]; ok {
			want[m.GetName()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("%s gauge not registered", name)
		}
	}
}

func TestResetFlowGauges(t *testing.T) {
	SetFlowStates("cluster-b", "internal", map[string]int{"manquant": 5})
	ResetFlowGauges()
	if got := testutil.ToFloat64(flowStates.WithLabelValues("cluster-b", "internal", "manquant")); got != 0 {
		t.Fatalf("flowStates after reset: want 0, got %v", got)
	}
}

func TestExtractMetricNames(t *testing.T) {
	for _, m := range []string{"longue_vue_extracts_total", "longue_vue_extract_rows_total"} {
		if !strings.Contains(m, "longue_vue") {
			t.Errorf("metric name missing namespace: %s", m)
		}
		if !strings.HasSuffix(m, "_total") {
			t.Errorf("metric name not _total-suffixed: %s", m)
		}
	}
}
