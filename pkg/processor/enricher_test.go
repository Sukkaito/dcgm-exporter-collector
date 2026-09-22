package processor

import (
	"strings"
	"testing"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/api"
	dto "github.com/prometheus/client_model/go"
)

func TestMetricEnricher_EnrichTargetMetrics(t *testing.T) {
	raw := `
# HELP DCGM_FI_DEV_GPU_UTIL GPU utilization (in %).
# TYPE DCGM_FI_DEV_GPU_UTIL gauge
DCGM_FI_DEV_GPU_UTIL{gpu="0",UUID="GPU-1111"} 65
DCGM_FI_DEV_GPU_UTIL{gpu="1",UUID="GPU-2222"} 80
`

	target := api.TargetVM{
		VMID:      "inst-uuid-1",
		VMName:    "ai-workload-1",
		ProjectID: "proj-123",
		GuestIP:   "10.0.0.15",
		Port:      9400,
	}

	enricher := NewMetricEnricher("hgx087")
	mfs, err := enricher.EnrichTargetMetrics([]byte(raw), target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mf, ok := mfs["DCGM_FI_DEV_GPU_UTIL"]
	if !ok {
		t.Fatalf("missing DCGM_FI_DEV_GPU_UTIL metric family")
	}

	if len(mf.Metric) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(mf.Metric))
	}

	for _, m := range mf.Metric {
		labelMap := make(map[string]string)
		for _, lp := range m.Label {
			labelMap[lp.GetName()] = lp.GetValue()
		}

		if labelMap["host"] != "hgx087" {
			t.Errorf("expected host=hgx087, got %s", labelMap["host"])
		}
		if labelMap["vm_id"] != "inst-uuid-1" {
			t.Errorf("expected vm_id=inst-uuid-1, got %s", labelMap["vm_id"])
		}
		if labelMap["vm_name"] != "ai-workload-1" {
			t.Errorf("expected vm_name=ai-workload-1, got %s", labelMap["vm_name"])
		}
		if labelMap["project_id"] != "proj-123" {
			t.Errorf("expected project_id=proj-123, got %s", labelMap["project_id"])
		}
		if _, hasGPU := labelMap["gpu"]; !hasGPU {
			t.Errorf("expected gpu label to be preserved")
		}
	}
}

func TestMergeFamiliesAndEncode(t *testing.T) {
	rawVM1 := `
# HELP DCGM_FI_DEV_GPU_UTIL GPU utilization (in %).
# TYPE DCGM_FI_DEV_GPU_UTIL gauge
DCGM_FI_DEV_GPU_UTIL{gpu="0"} 50
`
	rawVM2 := `
# HELP DCGM_FI_DEV_GPU_UTIL GPU utilization (in %).
# TYPE DCGM_FI_DEV_GPU_UTIL gauge
DCGM_FI_DEV_GPU_UTIL{gpu="0"} 75
`
	enricher := NewMetricEnricher("hgx087")

	t1 := api.TargetVM{VMID: "vm-1", VMName: "inst-1"}
	t2 := api.TargetVM{VMID: "vm-2", VMName: "inst-2"}

	mfs1, _ := enricher.EnrichTargetMetrics([]byte(rawVM1), t1)
	mfs2, _ := enricher.EnrichTargetMetrics([]byte(rawVM2), t2)

	combined := make(map[string]*dto.MetricFamily)
	MergeFamilies(combined, mfs1)
	MergeFamilies(combined, mfs2)

	encoded, err := EncodeMetricFamilies(combined)
	if err != nil {
		t.Fatalf("unexpected encode error: %v", err)
	}

	out := string(encoded)
	if !strings.Contains(out, "vm_id=\"vm-1\"") {
		t.Errorf("encoded output missing vm-1: %s", out)
	}
	if !strings.Contains(out, "vm_id=\"vm-2\"") {
		t.Errorf("encoded output missing vm-2: %s", out)
	}
	if !strings.Contains(out, "host=\"hgx087\"") {
		t.Errorf("encoded output missing host label: %s", out)
	}
}
