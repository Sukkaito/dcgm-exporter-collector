package processor

import (
	"bytes"
	"fmt"
	"sort"
	"sync"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/api"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"google.golang.org/protobuf/proto"
)

// MetricEnricher enriches Prometheus metrics with host and VM metadata labels.
type MetricEnricher struct {
	hostName string
}

// NewMetricEnricher creates a new MetricEnricher.
func NewMetricEnricher(hostName string) *MetricEnricher {
	return &MetricEnricher{hostName: hostName}
}

// EnrichTargetMetrics parses Prometheus text exposition format and adds host and VM labels to each metric.
func (e *MetricEnricher) EnrichTargetMetrics(payload []byte, target api.TargetVM) (map[string]*dto.MetricFamily, error) {
	if len(payload) == 0 {
		return make(map[string]*dto.MetricFamily), nil
	}

	parser := expfmt.NewTextParser(model.LegacyValidation)
	mfs, err := parser.TextToMetricFamilies(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("parsing metrics for vm %s: %w", target.VMID, err)
	}

	for _, mf := range mfs {
		for _, m := range mf.Metric {
			e.enrichMetric(m, target)
		}
	}

	return mfs, nil
}

// HostName returns the configured host name.
func (e *MetricEnricher) HostName() string {
	return e.hostName
}

func (e *MetricEnricher) enrichMetric(m *dto.Metric, target api.TargetVM) {
	filtered := make([]*dto.LabelPair, 0, len(m.Label)+4)
	existing := make(map[string]*dto.LabelPair)

	for _, lp := range m.Label {
		name := lp.GetName()
		filtered = append(filtered, lp)
		existing[name] = lp
	}

	setOrAddLabel := func(name, value string) {
		if value == "" {
			return
		}
		if lp, ok := existing[name]; ok {
			// Authoritative override
			lp.Value = proto.String(value)
		} else {
			newLP := &dto.LabelPair{
				Name:  proto.String(name),
				Value: proto.String(value),
			}
			filtered = append(filtered, newLP)
			existing[name] = newLP
		}
	}

	setOrAddLabel("host", e.hostName)
	setOrAddLabel("vm_id", target.VMID)
	setOrAddLabel("vm_name", target.VMName)
	setOrAddLabel("project_id", target.ProjectID)

	m.Label = filtered

	// Sort labels alphabetically to keep Prometheus output deterministic
	sort.Slice(m.Label, func(i, j int) bool {
		return m.Label[i].GetName() < m.Label[j].GetName()
	})
}

// MergeFamilies merges source metric families into a destination map.
func MergeFamilies(dest, src map[string]*dto.MetricFamily) {
	for name, srcMF := range src {
		destMF, exists := dest[name]
		if !exists {
			dest[name] = srcMF
			continue
		}
		// Append metric samples to the existing family
		destMF.Metric = append(destMF.Metric, srcMF.Metric...)
	}
}

var bufferPool = sync.Pool{
	New: func() any {
		return new(bytes.Buffer)
	},
}

// EncodeMetricFamilies converts a map of metric families into Prometheus text format bytes.
func EncodeMetricFamilies(mfs map[string]*dto.MetricFamily) ([]byte, error) {
	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufferPool.Put(buf)

	// Sort metric names for deterministic Prometheus exposition
	names := make([]string, 0, len(mfs))
	for name := range mfs {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		mf := mfs[name]
		if _, err := expfmt.MetricFamilyToText(buf, mf); err != nil {
			return nil, fmt.Errorf("encoding metric family %s: %w", name, err)
		}
	}

	res := make([]byte, buf.Len())
	copy(res, buf.Bytes())
	return res, nil
}
