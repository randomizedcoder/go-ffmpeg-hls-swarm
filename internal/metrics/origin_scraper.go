// Package metrics provides Prometheus metrics collection and export.
package metrics

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/influxdata/tdigest"
	"github.com/prometheus/common/expfmt"
	dto "github.com/prometheus/client_model/go"
)

// OriginMetrics contains scraped metrics from origin server exporters.
type OriginMetrics struct {
	// Node exporter metrics
	CPUPercent float64
	MemUsed    int64
	MemTotal   int64
	MemPercent float64
	NetInRate  float64 // bytes/sec (instantaneous)
	NetOutRate float64 // bytes/sec (instantaneous)

	// Rolling window percentiles (new)
	NetInP50  float64 // P50 (median) over rolling window
	NetInMax  float64 // Max over rolling window
	NetOutP50 float64 // P50 (median) over rolling window
	NetOutMax float64 // Max over rolling window
	NetWindowSeconds int // Window size in seconds (for display)

	// Nginx exporter metrics
	NginxConnections int64
	NginxReqRate     float64 // requests/sec
	NginxReqDuration float64 // avg duration in seconds

	// Metadata
	LastUpdate time.Time
	Healthy    bool
	Error      string
}

// OriginScraper scrapes metrics from node_exporter and nginx_exporter.
// Uses atomic.Value for lock-free metric reads.
type OriginScraper struct {
	nodeExporterURL  string
	nginxExporterURL string
	interval         time.Duration
	logger           *slog.Logger
	httpClient       *http.Client

	// Atomic storage (lock-free reads)
	metrics atomic.Value // *OriginMetrics

	// Rate calculation state (atomic for lock-free updates)
	lastNetIn     atomic.Uint64 // float64 as bits (math.Float64bits)
	lastNetOut    atomic.Uint64 // float64 as bits
	lastNetTime   atomic.Value  // time.Time
	lastNginxReqs atomic.Uint64 // float64 as bits
	lastNginxTime atomic.Value  // time.Time

	// Rolling windows for network metrics (using T-Digest)
	netInDigest  *tdigest.TDigest
	netInSamples []networkSample
	netInMu      sync.Mutex

	netOutDigest  *tdigest.TDigest
	netOutSamples []networkSample
	netOutMu      sync.Mutex

	windowSize time.Duration // Configurable window size
	lastClean  time.Time     // Last cleanup time
}

// networkSample represents a single network rate sample with timestamp.
type networkSample struct {
	value float64
	time  time.Time
}

// NewOriginScraper creates a new origin metrics scraper.
// Returns nil if both URLs are empty (feature disabled).
func NewOriginScraper(nodeExporterURL, nginxExporterURL string, interval, windowSize time.Duration, logger *slog.Logger) *OriginScraper {
	if nodeExporterURL == "" && nginxExporterURL == "" {
		return nil // Feature disabled
	}

	// Clamp window size for safety (validation also done in config.Validate())
	if windowSize < 10*time.Second {
		windowSize = 10 * time.Second
	}
	if windowSize > 300*time.Second {
		windowSize = 300 * time.Second
	}

	scraper := &OriginScraper{
		nodeExporterURL:  nodeExporterURL,
		nginxExporterURL: nginxExporterURL,
		interval:         interval,
		logger:           logger,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		// Initialize T-Digests
		netInDigest:  tdigest.NewWithCompression(100),
		netOutDigest: tdigest.NewWithCompression(100),
		windowSize:   windowSize,
		lastClean:    time.Now(),
	}

	// Initialize with empty metrics
	scraper.metrics.Store(&OriginMetrics{
		Healthy: false,
		Error:   "Not yet scraped",
	})

	return scraper
}

// Run starts the scraper goroutine.
func (s *OriginScraper) Run(ctx context.Context) {
	if s == nil {
		return // Feature disabled
	}

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// Initial scrape
	s.scrapeAll()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scrapeAll()
		}
	}
}

// GetMetrics returns the current metrics (thread-safe, lock-free).
func (s *OriginScraper) GetMetrics() *OriginMetrics {
	if s == nil {
		return nil // Feature disabled
	}

	ptr := s.metrics.Load()
	if ptr == nil {
		return nil
	}

	// Return a copy to avoid race conditions
	m := ptr.(*OriginMetrics)
	now := time.Now()

	// Calculate percentiles from T-Digest (following debug_events.go pattern)
	// Net In
	s.netInMu.Lock()
	s.cleanupNetworkWindow(&s.netInSamples, s.netInDigest, now)
	var netInP50, netInMax float64
	if len(s.netInSamples) > 0 {
		netInP50 = s.netInDigest.Quantile(0.50)
		// Calculate max from samples
		netInMax = s.netInSamples[0].value
		for _, sample := range s.netInSamples {
			if sample.value > netInMax {
				netInMax = sample.value
			}
		}
	}
	s.netInMu.Unlock()

	// Net Out
	s.netOutMu.Lock()
	s.cleanupNetworkWindow(&s.netOutSamples, s.netOutDigest, now)
	var netOutP50, netOutMax float64
	if len(s.netOutSamples) > 0 {
		netOutP50 = s.netOutDigest.Quantile(0.50)
		// Calculate max from samples
		netOutMax = s.netOutSamples[0].value
		for _, sample := range s.netOutSamples {
			if sample.value > netOutMax {
				netOutMax = sample.value
			}
		}
	}
	s.netOutMu.Unlock()

	return &OriginMetrics{
		CPUPercent:      m.CPUPercent,
		MemUsed:         m.MemUsed,
		MemTotal:        m.MemTotal,
		MemPercent:      m.MemPercent,
		NetInRate:       m.NetInRate,
		NetOutRate:      m.NetOutRate,
		NetInP50:        netInP50,
		NetInMax:        netInMax,
		NetOutP50:       netOutP50,
		NetOutMax:       netOutMax,
		NetWindowSeconds: int(s.windowSize.Seconds()),
		NginxConnections: m.NginxConnections,
		NginxReqRate:    m.NginxReqRate,
		NginxReqDuration: m.NginxReqDuration,
		LastUpdate:      m.LastUpdate,
		Healthy:         m.Healthy,
		Error:           m.Error,
	}
}

// scrapeAll scrapes both node_exporter and nginx_exporter.
func (s *OriginScraper) scrapeAll() {
	now := time.Now()
	healthy := true
	var errors []string

	// Get current metrics to preserve values if scrape fails
	current := s.metrics.Load()
	var lastMetrics *OriginMetrics
	if current != nil {
		lastMetrics = current.(*OriginMetrics)
	} else {
		lastMetrics = &OriginMetrics{}
	}

	// Create new metrics struct
	newMetrics := &OriginMetrics{
		CPUPercent:      lastMetrics.CPUPercent, // Preserve last values
		MemUsed:         lastMetrics.MemUsed,
		MemTotal:        lastMetrics.MemTotal,
		MemPercent:      lastMetrics.MemPercent,
		NetInRate:       lastMetrics.NetInRate,
		NetOutRate:      lastMetrics.NetOutRate,
		NginxConnections: lastMetrics.NginxConnections,
		NginxReqRate:    lastMetrics.NginxReqRate,
		NginxReqDuration: lastMetrics.NginxReqDuration,
		LastUpdate:      now,
	}

	// Scrape node_exporter
	if s.nodeExporterURL != "" {
		if err := s.scrapeNodeExporter(newMetrics); err != nil {
			healthy = false
			errors = append(errors, fmt.Sprintf("node_exporter: %v", err))
			if s.logger != nil {
				s.logger.Debug("node_exporter_scrape_error", "error", err)
			}
		}
	}

	// Scrape nginx_exporter
	if s.nginxExporterURL != "" {
		if err := s.scrapeNginxExporter(newMetrics); err != nil {
			healthy = false
			errors = append(errors, fmt.Sprintf("nginx_exporter: %v", err))
			if s.logger != nil {
				s.logger.Debug("nginx_exporter_scrape_error", "error", err)
			}
		}
	}

	// Update metadata
	newMetrics.Healthy = healthy
	if len(errors) > 0 {
		newMetrics.Error = strings.Join(errors, "; ")
	} else {
		newMetrics.Error = ""
	}

	// Atomic store (lock-free write)
	s.metrics.Store(newMetrics)
}

// scrapeNodeExporter scrapes metrics from node_exporter.
func (s *OriginScraper) scrapeNodeExporter(metrics *OriginMetrics) error {
	resp, err := s.httpClient.Get(s.nodeExporterURL)
	if err != nil {
		return fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http status %d", resp.StatusCode)
	}

	// Parse Prometheus text format
	decoder := expfmt.NewDecoder(resp.Body, expfmt.FmtText)
	parsedMetrics := make(map[string]*dto.MetricFamily)

	for {
		var mf dto.MetricFamily
		if err := decoder.Decode(&mf); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("decode error: %w", err)
		}
		parsedMetrics[mf.GetName()] = &mf
	}

	// Extract CPU usage
	metrics.CPUPercent = s.extractCPUUsage(parsedMetrics)

	// Extract memory
	metrics.MemUsed, metrics.MemTotal, metrics.MemPercent = s.extractMemory(parsedMetrics)

	// Extract network (and calculate rate)
	metrics.NetInRate, metrics.NetOutRate = s.extractNetwork(parsedMetrics)

	return nil
}

// scrapeNginxExporter scrapes metrics from nginx_exporter.
func (s *OriginScraper) scrapeNginxExporter(metrics *OriginMetrics) error {
	resp, err := s.httpClient.Get(s.nginxExporterURL)
	if err != nil {
		return fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http status %d", resp.StatusCode)
	}

	// Parse Prometheus text format
	decoder := expfmt.NewDecoder(resp.Body, expfmt.FmtText)
	parsedMetrics := make(map[string]*dto.MetricFamily)

	for {
		var mf dto.MetricFamily
		if err := decoder.Decode(&mf); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("decode error: %w", err)
		}
		parsedMetrics[mf.GetName()] = &mf
	}

	// Extract nginx metrics
	metrics.NginxConnections = s.extractNginxConnections(parsedMetrics)
	metrics.NginxReqRate, metrics.NginxReqDuration = s.extractNginxRequests(parsedMetrics)

	return nil
}

// extractCPUUsage extracts CPU usage percentage from node_cpu_seconds_total.
// Calculates: (1 - idle/total) * 100
func (s *OriginScraper) extractCPUUsage(metrics map[string]*dto.MetricFamily) float64 {
	mf, ok := metrics["node_cpu_seconds_total"]
	if !ok {
		return 0
	}

	var totalCPU, idleCPU float64
	for _, metric := range mf.GetMetric() {
		for _, label := range metric.GetLabel() {
			if label.GetName() == "mode" {
				mode := label.GetValue()
				value := metric.GetCounter().GetValue()
				if mode == "idle" {
					idleCPU += value
				}
				totalCPU += value
			}
		}
	}

	if totalCPU == 0 {
		return 0
	}

	// Calculate usage: (1 - idle/total) * 100
	usage := (1 - idleCPU/totalCPU) * 100
	return usage
}

// extractMemory extracts memory metrics from node_memory_*.
func (s *OriginScraper) extractMemory(metrics map[string]*dto.MetricFamily) (used, total int64, percent float64) {
	memTotalMF, ok := metrics["node_memory_MemTotal_bytes"]
	if !ok {
		return 0, 0, 0
	}

	memAvailableMF, ok := metrics["node_memory_MemAvailable_bytes"]
	if !ok {
		// Fallback to MemFree if MemAvailable not available
		memAvailableMF, ok = metrics["node_memory_MemFree_bytes"]
		if !ok {
			return 0, 0, 0
		}
	}

	var totalBytes, availableBytes float64
	if len(memTotalMF.GetMetric()) > 0 {
		totalBytes = memTotalMF.GetMetric()[0].GetGauge().GetValue()
	}
	if len(memAvailableMF.GetMetric()) > 0 {
		availableBytes = memAvailableMF.GetMetric()[0].GetGauge().GetValue()
	}

	total = int64(totalBytes)
	used = int64(totalBytes - availableBytes)
	if total > 0 {
		percent = float64(used) / float64(total) * 100
	}

	return used, total, percent
}

// extractNetwork extracts network metrics and calculates rates.
func (s *OriginScraper) extractNetwork(metrics map[string]*dto.MetricFamily) (inRate, outRate float64) {
	now := time.Now()

	// Find the primary network interface (usually eth0, ens*, or first non-lo)
	var netInTotal, netOutTotal float64
	netInMF, ok := metrics["node_network_receive_bytes_total"]
	if ok {
		for _, metric := range netInMF.GetMetric() {
			// Skip loopback
			isLoopback := false
			for _, label := range metric.GetLabel() {
				if label.GetName() == "device" {
					device := label.GetValue()
					if device == "lo" {
						isLoopback = true
						break
					}
				}
			}
			if !isLoopback {
				netInTotal += metric.GetCounter().GetValue()
			}
		}
	}

	netOutMF, ok := metrics["node_network_transmit_bytes_total"]
	if ok {
		for _, metric := range netOutMF.GetMetric() {
			// Skip loopback
			isLoopback := false
			for _, label := range metric.GetLabel() {
				if label.GetName() == "device" {
					device := label.GetValue()
					if device == "lo" {
						isLoopback = true
						break
					}
				}
			}
			if !isLoopback {
				netOutTotal += metric.GetCounter().GetValue()
			}
		}
	}

	// Calculate rates (atomic reads)
	lastNetIn := loadFloat64(&s.lastNetIn)
	lastNetOut := loadFloat64(&s.lastNetOut)
	lastNetTimeVal := s.lastNetTime.Load()

	if lastNetTimeVal != nil {
		lastNetTime := lastNetTimeVal.(time.Time)
		if !lastNetTime.IsZero() {
			deltaTime := now.Sub(lastNetTime).Seconds()
			if deltaTime > 0 {
				inRate = (netInTotal - lastNetIn) / deltaTime
				outRate = (netOutTotal - lastNetOut) / deltaTime
			}
		}
	}

	// Atomic writes
	storeFloat64(&s.lastNetIn, netInTotal)
	storeFloat64(&s.lastNetOut, netOutTotal)
	s.lastNetTime.Store(now)

	// Add to rolling windows (following debug_events.go pattern)
	// Net In
	s.netInMu.Lock()
	s.netInDigest.Add(inRate, 1)
	s.netInSamples = append(s.netInSamples, networkSample{value: inRate, time: now})
	// Trigger cleanup if needed (every 10s or when >20 samples)
	if len(s.netInSamples) > 20 || time.Since(s.lastClean) > 10*time.Second {
		s.cleanupNetworkWindow(&s.netInSamples, s.netInDigest, now)
	}
	s.netInMu.Unlock()

	// Net Out
	s.netOutMu.Lock()
	s.netOutDigest.Add(outRate, 1)
	s.netOutSamples = append(s.netOutSamples, networkSample{value: outRate, time: now})
	// Trigger cleanup if needed
	if len(s.netOutSamples) > 20 || time.Since(s.lastClean) > 10*time.Second {
		s.cleanupNetworkWindow(&s.netOutSamples, s.netOutDigest, now)
	}
	s.netOutMu.Unlock()

	s.lastClean = now

	return inRate, outRate
}

// extractNginxConnections extracts active connections from nginx_exporter.
func (s *OriginScraper) extractNginxConnections(metrics map[string]*dto.MetricFamily) int64 {
	mf, ok := metrics["nginx_connections_active"]
	if !ok {
		return 0
	}

	if len(mf.GetMetric()) > 0 {
		return int64(mf.GetMetric()[0].GetGauge().GetValue())
	}
	return 0
}

// extractNginxRequests extracts request metrics and calculates rate.
func (s *OriginScraper) extractNginxRequests(metrics map[string]*dto.MetricFamily) (reqRate, duration float64) {
	now := time.Now()

	// Extract total requests
	reqTotalMF, ok := metrics["nginx_http_requests_total"]
	var reqTotal float64
	if ok {
		for _, metric := range reqTotalMF.GetMetric() {
			reqTotal += metric.GetCounter().GetValue()
		}
	}

	// Calculate request rate (atomic reads)
	lastNginxReqs := loadFloat64(&s.lastNginxReqs)
	lastNginxTimeVal := s.lastNginxTime.Load()

	if lastNginxTimeVal != nil {
		lastNginxTime := lastNginxTimeVal.(time.Time)
		if !lastNginxTime.IsZero() {
			deltaTime := now.Sub(lastNginxTime).Seconds()
			if deltaTime > 0 {
				reqRate = (reqTotal - lastNginxReqs) / deltaTime
			}
		}
	}

	// Atomic writes
	storeFloat64(&s.lastNginxReqs, reqTotal)
	s.lastNginxTime.Store(now)

	// Extract average request duration
	durationMF, ok := metrics["nginx_http_request_duration_seconds"]
	if ok {
		// Use sum/count for average, or bucket estimate
		var sum, count float64
		for _, metric := range durationMF.GetMetric() {
			hist := metric.GetHistogram()
			if hist != nil {
				sum += hist.GetSampleSum()
				count += float64(hist.GetSampleCount())
			}
		}
		if count > 0 {
			duration = sum / count
		}
	}

	return reqRate, duration
}

// Helper functions for atomic float64 operations

// storeFloat64 stores a float64 value atomically using math.Float64bits.
func storeFloat64(addr *atomic.Uint64, val float64) {
	addr.Store(math.Float64bits(val))
}

// loadFloat64 loads a float64 value atomically using math.Float64frombits.
func loadFloat64(addr *atomic.Uint64) float64 {
	return math.Float64frombits(addr.Load())
}

// cleanupNetworkWindow removes samples older than window and rebuilds T-Digest.
// Optimized: Only rebuilds T-Digest when samples actually expire.
func (s *OriginScraper) cleanupNetworkWindow(samples *[]networkSample, digest *tdigest.TDigest, now time.Time) {
	cutoff := now.Add(-s.windowSize)

	// Filter valid samples (keep only those within window)
	valid := make([]networkSample, 0, len(*samples))
	expiredCount := 0
	for _, sample := range *samples {
		if sample.time.After(cutoff) {
			valid = append(valid, sample)
		} else {
			expiredCount++
		}
	}

	// Only rebuild T-Digest if samples expired
	if expiredCount > 0 {
		*digest = *tdigest.NewWithCompression(100)
		for _, sample := range valid {
			digest.Add(sample.value, 1)
		}
	}

	*samples = valid
}

// GetOriginHostname extracts hostname from URL for Prometheus labels.
func GetOriginHostname(urlStr string) string {
	if urlStr == "" {
		return "unknown"
	}
	u, err := url.Parse(urlStr)
	if err != nil {
		return "unknown"
	}
	return u.Hostname()
}
