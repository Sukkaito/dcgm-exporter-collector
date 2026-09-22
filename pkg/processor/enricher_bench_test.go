package processor

import (
	"testing"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/api"
	dto "github.com/prometheus/client_model/go"
)

var benchRawDCGM = []byte(`# HELP DCGM_FI_DEV_GPU_UTIL GPU utilization (in %).
# TYPE DCGM_FI_DEV_GPU_UTIL gauge
DCGM_FI_DEV_GPU_UTIL{DCGM_FI_DRIVER_VERSION="610.57.04",UUID="GPU-68800a35",device="nvidia0",gpu="0",hostname="ducnm10-sv13-ht-3",modelName="NVIDIA GeForce RTX 4090",pci_bus_id="00000000:06:00.0"} 75
# HELP DCGM_FI_DEV_MEM_COPY_UTIL Memory utilization (in %).
# TYPE DCGM_FI_DEV_MEM_COPY_UTIL gauge
DCGM_FI_DEV_MEM_COPY_UTIL{DCGM_FI_DRIVER_VERSION="610.57.04",UUID="GPU-68800a35",device="nvidia0",gpu="0",hostname="ducnm10-sv13-ht-3",modelName="NVIDIA GeForce RTX 4090",pci_bus_id="00000000:06:00.0"} 35
# HELP DCGM_FI_DEV_GPU_TEMP GPU temperature (in C).
# TYPE DCGM_FI_DEV_GPU_TEMP gauge
DCGM_FI_DEV_GPU_TEMP{DCGM_FI_DRIVER_VERSION="610.57.04",UUID="GPU-68800a35",device="nvidia0",gpu="0",hostname="ducnm10-sv13-ht-3",modelName="NVIDIA GeForce RTX 4090",pci_bus_id="00000000:06:00.0"} 69
# HELP DCGM_FI_DEV_FB_USED Framebuffer memory used (in MiB).
# TYPE DCGM_FI_DEV_FB_USED gauge
DCGM_FI_DEV_FB_USED{DCGM_FI_DRIVER_VERSION="610.57.04",UUID="GPU-68800a35",device="nvidia0",gpu="0",hostname="ducnm10-sv13-ht-3",modelName="NVIDIA GeForce RTX 4090",pci_bus_id="00000000:06:00.0"} 793
`)

func BenchmarkMetricEnricher_EnrichTargetMetrics(b *testing.B) {
	enricher := NewMetricEnricher("ubuntu-sv13")
	target := api.TargetVM{
		VMID:      "296828dc-d725-40fe-9632-eba892cecb1e",
		VMName:    "ducnm10-sv13-ht-3",
		ProjectID: "570d760d12de41149e13471007cd76a6",
		GuestIP:   "10.0.0.12",
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := enricher.EnrichTargetMetrics(benchRawDCGM, target)
		if err != nil {
			b.Fatalf("enrich error: %v", err)
		}
	}
}

func BenchmarkEncodeMetricFamilies(b *testing.B) {
	enricher := NewMetricEnricher("ubuntu-sv13")
	target := api.TargetVM{
		VMID:      "296828dc-d725-40fe-9632-eba892cecb1e",
		VMName:    "ducnm10-sv13-ht-3",
		ProjectID: "570d760d12de41149e13471007cd76a6",
		GuestIP:   "10.0.0.12",
	}

	mfs, err := enricher.EnrichTargetMetrics(benchRawDCGM, target)
	if err != nil {
		b.Fatalf("setup error: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := EncodeMetricFamilies(mfs)
		if err != nil {
			b.Fatalf("encode error: %v", err)
		}
	}
}

func BenchmarkEndToEndPipeline(b *testing.B) {
	enricher := NewMetricEnricher("ubuntu-sv13")
	target1 := api.TargetVM{
		VMID:      "vm-1",
		VMName:    "ducnm10-sv13-ht-3",
		ProjectID: "proj-1",
		GuestIP:   "10.0.0.12",
	}
	target2 := api.TargetVM{
		VMID:      "vm-2",
		VMName:    "phongtn5-testvm-6",
		ProjectID: "proj-1",
		GuestIP:   "10.0.0.13",
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		mfs1, _ := enricher.EnrichTargetMetrics(benchRawDCGM, target1)
		mfs2, _ := enricher.EnrichTargetMetrics(benchRawDCGM, target2)
		combined := make(map[string]*dto.MetricFamily)
		MergeFamilies(combined, mfs1)
		MergeFamilies(combined, mfs2)
		_, err := EncodeMetricFamilies(combined)
		if err != nil {
			b.Fatalf("pipeline error: %v", err)
		}
	}
}
