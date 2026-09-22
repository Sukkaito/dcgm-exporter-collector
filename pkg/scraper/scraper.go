package scraper

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/api"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/transport"
)

// ScrapeResult represents the outcome of scraping a single target.
type ScrapeResult struct {
	Target  api.TargetVM
	Payload []byte
	Error   error
	Latency time.Duration
}

// Scraper coordinates concurrent scraping across all active targets.
type Scraper struct {
	transport      transport.TelemetryTransport
	scrapeTimeout  time.Duration
	mu             sync.RWMutex
	exporterStatus map[string]api.ExporterStatus
}

// NewScraper creates a new Scraper.
func NewScraper(tr transport.TelemetryTransport, scrapeTimeout time.Duration) *Scraper {
	if scrapeTimeout <= 0 {
		scrapeTimeout = 3 * time.Second
	}
	return &Scraper{
		transport:      tr,
		scrapeTimeout:  scrapeTimeout,
		exporterStatus: make(map[string]api.ExporterStatus),
	}
}

// ScrapeTarget fetches metrics from a single target with a dedicated timeout.
func (s *Scraper) ScrapeTarget(ctx context.Context, target api.TargetVM) ScrapeResult {
	start := time.Now()
	timeoutCtx, cancel := context.WithTimeout(ctx, s.scrapeTimeout)
	defer cancel()

	data, err := s.transport.GetMetrics(timeoutCtx, target)
	latency := time.Since(start)

	return ScrapeResult{
		Target:  target,
		Payload: data,
		Error:   err,
		Latency: latency,
	}
}

// ScrapeAll scrapes all provided targets concurrently and updates internal health status.
func (s *Scraper) ScrapeAll(ctx context.Context, targets []api.TargetVM) map[string]ScrapeResult {
	results := make(map[string]ScrapeResult, len(targets))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, t := range targets {
		wg.Add(1)
		go func(target api.TargetVM) {
			defer wg.Done()
			res := s.ScrapeTarget(ctx, target)

			mu.Lock()
			results[target.VMID] = res
			mu.Unlock()

			s.recordStatus(res)
		}(t)
	}

	wg.Wait()
	return results
}

func (s *Scraper) recordStatus(res ScrapeResult) {
	s.mu.Lock()
	defer s.mu.Unlock()

	endpoint := fmt.Sprintf("%s:%d", res.Target.GuestIP, res.Target.Port)
	if res.Target.Port <= 0 {
		endpoint = fmt.Sprintf("%s:9400", res.Target.GuestIP)
	}

	status := api.ExporterStatus{
		VMID:             res.Target.VMID,
		VMName:           res.Target.VMName,
		Endpoint:         endpoint,
		ScrapeDurationMs: res.Latency.Milliseconds(),
	}

	if res.Error == nil {
		status.Healthy = true
		status.LastSuccess = time.Now()
	} else {
		status.Healthy = false
		status.LastError = res.Error.Error()
		// Preserve previous LastSuccess if available
		if prev, ok := s.exporterStatus[res.Target.VMID]; ok {
			status.LastSuccess = prev.LastSuccess
		}
	}

	s.exporterStatus[res.Target.VMID] = status
}

// GetStatus returns the current health report of all known exporters.
func (s *Scraper) GetStatus() api.CollectorStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	exporters := make([]api.ExporterStatus, 0, len(s.exporterStatus))
	healthyCount := 0

	for _, status := range s.exporterStatus {
		exporters = append(exporters, status)
		if status.Healthy {
			healthyCount++
		}
	}

	collectorState := "healthy"
	if len(exporters) > 0 && healthyCount == 0 {
		collectorState = "degraded"
	}

	return api.CollectorStatus{
		CollectorStatus: collectorState,
		Timestamp:       time.Now(),
		TotalTargets:    len(exporters),
		HealthyTargets:  healthyCount,
		Exporters:       exporters,
	}
}
