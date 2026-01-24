// Package metrics provides Prometheus metrics collection and export.
package metrics

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

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
	NetInRate  float64 // bytes/sec
	NetOutRate float64 // bytes/sec

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
type OriginScraper struct {
	nodeExporterURL string
	nginxExporterURL string
	interval        time.Duration
	metrics         *OriginMetrics
	mu              sync.RWMutex
	logger          *slog.Logger
	httpClient      *http.Client

	// For rate calculations
	lastNetIn     float64
	lastNetOut    float64
	lastNetTime   time.Time
	lastNginxReqs float64
	lastNginxTime time.Time
}

// NewOriginScraper creates a new origin metrics scraper.
func NewOriginScraper(nodeExporterURL, nginxExporterURL string, interval time.Duration, logger *slog.Logger) *OriginScraper {
	return &OriginScraper{
		nodeExporterURL:  nodeExporterURL,
		nginxExporterURL: nginxExporterURL,
		interval:         interval,
		metrics:          &OriginMetrics{},
		logger:           logger,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// Run starts the scraper goroutine.
func (s *OriginScraper) Run(ctx context.Context) {
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

// GetMetrics returns the current metrics (thread-safe).
func (s *OriginScraper) GetMetrics() *OriginMetrics {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return a copy to avoid race conditions
	return &OriginMetrics{
		CPUPercent:      s.metrics.CPUPercent,
		MemUsed:         s.metrics.MemUsed,
		MemTotal:        s.metrics.MemTotal,
		MemPercent:      s.metrics.MemPercent,
		NetInRate:       s.metrics.NetInRate,
		NetOutRate:      s.metrics.NetOutRate,
		NginxConnections: s.metrics.NginxConnections,
		NginxReqRate:    s.metrics.NginxReqRate,
		NginxReqDuration: s.metrics.NginxReqDuration,
		LastUpdate:      s.metrics.LastUpdate,
		Healthy:         s.metrics.Healthy,
		Error:           s.metrics.Error,
	}
}

// scrapeAll scrapes both node_exporter and nginx_exporter.
func (s *OriginScraper) scrapeAll() {
	now := time.Now()
	healthy := true
	var errors []string

	// Scrape node_exporter
	if s.nodeExporterURL != "" {
		if err := s.scrapeNodeExporter(); err != nil {
			healthy = false
			errors = append(errors, fmt.Sprintf("node_exporter: %v", err))
			s.logger.Debug("node_exporter_scrape_error", "error", err)
		}
	}

	// Scrape nginx_exporter
	if s.nginxExporterURL != "" {
		if err := s.scrapeNginxExporter(); err != nil {
			healthy = false
			errors = append(errors, fmt.Sprintf("nginx_exporter: %v", err))
			s.logger.Debug("nginx_exporter_scrape_error", "error", err)
		}
	}

	s.mu.Lock()
	s.metrics.LastUpdate = now
	s.metrics.Healthy = healthy
	if len(errors) > 0 {
		s.metrics.Error = strings.Join(errors, "; ")
	} else {
		s.metrics.Error = ""
	}
	s.mu.Unlock()
}

// scrapeNodeExporter scrapes metrics from node_exporter.
func (s *OriginScraper) scrapeNodeExporter() error {
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
	metrics := make(map[string]*dto.MetricFamily)

	for {
		var mf dto.MetricFamily
		if err := decoder.Decode(&mf); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("decode error: %w", err)
		}
		metrics[mf.GetName()] = &mf
	}

	// Extract CPU usage
	cpuPercent := s.extractCPUUsage(metrics)

	// Extract memory
	memUsed, memTotal, memPercent := s.extractMemory(metrics)

	// Extract network (and calculate rate)
	netIn, netOut := s.extractNetwork(metrics)

	s.mu.Lock()
	s.metrics.CPUPercent = cpuPercent
	s.metrics.MemUsed = memUsed
	s.metrics.MemTotal = memTotal
	s.metrics.MemPercent = memPercent
	s.metrics.NetInRate = netIn
	s.metrics.NetOutRate = netOut
	s.mu.Unlock()

	return nil
}

// scrapeNginxExporter scrapes metrics from nginx_exporter.
func (s *OriginScraper) scrapeNginxExporter() error {
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
	metrics := make(map[string]*dto.MetricFamily)

	for {
		var mf dto.MetricFamily
		if err := decoder.Decode(&mf); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("decode error: %w", err)
		}
		metrics[mf.GetName()] = &mf
	}

	// Extract nginx metrics
	connections := s.extractNginxConnections(metrics)
	reqRate, duration := s.extractNginxRequests(metrics)

	s.mu.Lock()
	s.metrics.NginxConnections = connections
	s.metrics.NginxReqRate = reqRate
	s.metrics.NginxReqDuration = duration
	s.mu.Unlock()

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

	// Calculate rates
	if !s.lastNetTime.IsZero() {
		deltaTime := now.Sub(s.lastNetTime).Seconds()
		if deltaTime > 0 {
			inRate = (netInTotal - s.lastNetIn) / deltaTime
			outRate = (netOutTotal - s.lastNetOut) / deltaTime
		}
	}

	s.lastNetIn = netInTotal
	s.lastNetOut = netOutTotal
	s.lastNetTime = now

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

	// Calculate request rate
	if !s.lastNginxTime.IsZero() {
		deltaTime := now.Sub(s.lastNginxTime).Seconds()
		if deltaTime > 0 {
			reqRate = (reqTotal - s.lastNginxReqs) / deltaTime
		}
	}
	s.lastNginxReqs = reqTotal
	s.lastNginxTime = now

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
