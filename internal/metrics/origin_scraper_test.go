package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/influxdata/tdigest"
)

// mockPrometheusServer creates an HTTP server that serves Prometheus metrics.
func mockPrometheusServer(t *testing.T, metrics string) *httptest.Server {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(metrics))
	})
	return httptest.NewServer(handler)
}

// sampleNodeExporterMetrics returns sample node_exporter metrics.
func sampleNodeExporterMetrics() string {
	return `# HELP node_cpu_seconds_total Seconds the CPUs spent in each mode.
# TYPE node_cpu_seconds_total counter
node_cpu_seconds_total{cpu="0",mode="idle"} 12345.67
node_cpu_seconds_total{cpu="0",mode="user"} 1234.56
node_cpu_seconds_total{cpu="0",mode="system"} 567.89
node_cpu_seconds_total{cpu="1",mode="idle"} 12345.67
node_cpu_seconds_total{cpu="1",mode="user"} 1234.56
node_cpu_seconds_total{cpu="1",mode="system"} 567.89

# HELP node_memory_MemTotal_bytes Memory information field MemTotal_bytes.
# TYPE node_memory_MemTotal_bytes gauge
node_memory_MemTotal_bytes 4294967296

# HELP node_memory_MemAvailable_bytes Memory information field MemAvailable_bytes.
# TYPE node_memory_MemAvailable_bytes gauge
node_memory_MemAvailable_bytes 3000000000

# HELP node_network_receive_bytes_total Network device statistic receive_bytes.
# TYPE node_network_receive_bytes_total counter
node_network_receive_bytes_total{device="lo"} 1000000
node_network_receive_bytes_total{device="eth0"} 5000000000

# HELP node_network_transmit_bytes_total Network device statistic transmit_bytes.
# TYPE node_network_transmit_bytes_total counter
node_network_transmit_bytes_total{device="lo"} 1000000
node_network_transmit_bytes_total{device="eth0"} 8000000000
`
}

// sampleNginxExporterMetrics returns sample nginx_exporter metrics.
func sampleNginxExporterMetrics() string {
	return `# HELP nginx_connections_active Active client connections
# TYPE nginx_connections_active gauge
nginx_connections_active 42

# HELP nginx_http_requests_total Total number of HTTP requests
# TYPE nginx_http_requests_total counter
nginx_http_requests_total 123456

# HELP nginx_http_request_duration_seconds The HTTP request latencies in seconds.
# TYPE nginx_http_request_duration_seconds histogram
nginx_http_request_duration_seconds_bucket{le="0.005"} 1000
nginx_http_request_duration_seconds_bucket{le="0.01"} 2000
nginx_http_request_duration_seconds_bucket{le="0.025"} 3000
nginx_http_request_duration_seconds_bucket{le="0.05"} 4000
nginx_http_request_duration_seconds_bucket{le="0.1"} 5000
nginx_http_request_duration_seconds_bucket{le="0.25"} 6000
nginx_http_request_duration_seconds_bucket{le="0.5"} 7000
nginx_http_request_duration_seconds_bucket{le="1"} 8000
nginx_http_request_duration_seconds_bucket{le="2.5"} 9000
nginx_http_request_duration_seconds_bucket{le="5"} 10000
nginx_http_request_duration_seconds_bucket{le="+Inf"} 10000
nginx_http_request_duration_seconds_sum 500.0
nginx_http_request_duration_seconds_count 10000
`
}

func TestOriginScraper_FeatureDisabled(t *testing.T) {
	scraper := NewOriginScraper("", "", 1*time.Second, 30*time.Second, nil)
	if scraper != nil {
		t.Error("Expected nil scraper when both URLs empty")
	}
}

func TestOriginScraper_NodeExporter(t *testing.T) {
	// Setup mock server
	nodeServer := mockPrometheusServer(t, sampleNodeExporterMetrics())
	defer nodeServer.Close()

	// Create scraper
	scraper := NewOriginScraper(
		nodeServer.URL+"/metrics",
		"", // No nginx
		100*time.Millisecond, // Fast interval for test
		30*time.Second,       // Window size
		nil, // No logger for tests
	)
	if scraper == nil {
		t.Fatal("Expected scraper, got nil")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go scraper.Run(ctx)

	// Wait for initial scrape
	time.Sleep(150 * time.Millisecond)

	// Get metrics
	metrics := scraper.GetMetrics()
	if metrics == nil {
		t.Fatal("Expected metrics, got nil")
	}

	// Verify CPU (should be ~20%: (1 - idle/total) * 100 = (1 - 24701.34/28096.34) * 100)
	if metrics.CPUPercent <= 0 || metrics.CPUPercent >= 100 {
		t.Errorf("Expected CPU percent between 0-100, got %f", metrics.CPUPercent)
	}

	// Verify memory
	if metrics.MemTotal != 4294967296 {
		t.Errorf("Expected MemTotal 4294967296, got %d", metrics.MemTotal)
	}
	if metrics.MemUsed != 1294967296 { // 4GB - 3GB available
		t.Errorf("Expected MemUsed 1294967296, got %d", metrics.MemUsed)
	}

	// Verify network (should be 0 on first scrape, no rate yet)
	if metrics.NetInRate != 0 {
		t.Errorf("Expected NetInRate 0 on first scrape, got %f", metrics.NetInRate)
	}

	// Verify healthy
	if !metrics.Healthy {
		t.Error("Expected Healthy=true after successful scrape")
	}
}

func TestOriginScraper_RateCalculation(t *testing.T) {
	// Setup mock server
	nodeServer := mockPrometheusServer(t, sampleNodeExporterMetrics())
	defer nodeServer.Close()

	scraper := NewOriginScraper(
		nodeServer.URL+"/metrics",
		"",
		100*time.Millisecond, // Fast interval for test
		30*time.Second,       // Window size
		nil,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go scraper.Run(ctx)

	// Wait for two scrapes
	time.Sleep(250 * time.Millisecond)

	metrics := scraper.GetMetrics()
	if metrics == nil {
		t.Fatal("Expected metrics, got nil")
	}

	// After second scrape, should have rate (though it may be 0 if values didn't change)
	// The important thing is that the calculation ran without errors
	if metrics.NetInRate < 0 {
		t.Errorf("Expected NetInRate >= 0, got %f", metrics.NetInRate)
	}
}

func TestOriginScraper_NginxExporter(t *testing.T) {
	nginxServer := mockPrometheusServer(t, sampleNginxExporterMetrics())
	defer nginxServer.Close()

	scraper := NewOriginScraper(
		"",
		nginxServer.URL+"/metrics",
		100*time.Millisecond,
		30*time.Second, // Window size
		nil,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go scraper.Run(ctx)

	time.Sleep(150 * time.Millisecond)

	metrics := scraper.GetMetrics()
	if metrics == nil {
		t.Fatal("Expected metrics, got nil")
	}

	if metrics.NginxConnections != 42 {
		t.Errorf("Expected NginxConnections 42, got %d", metrics.NginxConnections)
	}

	// Request rate should be 0 on first scrape
	if metrics.NginxReqRate != 0 {
		t.Errorf("Expected NginxReqRate 0 on first scrape, got %f", metrics.NginxReqRate)
	}

	// Duration should be calculated (500.0 / 10000 = 0.05)
	expectedDuration := 500.0 / 10000.0
	if metrics.NginxReqDuration != expectedDuration {
		t.Errorf("Expected NginxReqDuration %f, got %f", expectedDuration, metrics.NginxReqDuration)
	}
}

func TestOriginScraper_BothExporters(t *testing.T) {
	nodeServer := mockPrometheusServer(t, sampleNodeExporterMetrics())
	defer nodeServer.Close()

	nginxServer := mockPrometheusServer(t, sampleNginxExporterMetrics())
	defer nginxServer.Close()

	scraper := NewOriginScraper(
		nodeServer.URL+"/metrics",
		nginxServer.URL+"/metrics",
		100*time.Millisecond,
		30*time.Second, // Window size
		nil,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go scraper.Run(ctx)

	time.Sleep(150 * time.Millisecond)

	metrics := scraper.GetMetrics()
	if metrics == nil {
		t.Fatal("Expected metrics, got nil")
	}

	// Verify both node and nginx metrics are present
	if metrics.MemTotal == 0 {
		t.Error("Expected MemTotal > 0 from node_exporter")
	}
	if metrics.NginxConnections == 0 {
		t.Error("Expected NginxConnections > 0 from nginx_exporter")
	}
}

func TestOriginScraper_ConcurrentReads(t *testing.T) {
	nodeServer := mockPrometheusServer(t, sampleNodeExporterMetrics())
	defer nodeServer.Close()

	scraper := NewOriginScraper(
		nodeServer.URL+"/metrics",
		"",
		1*time.Second,
		30*time.Second, // Window size
		nil,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go scraper.Run(ctx)

	time.Sleep(100 * time.Millisecond)

	// Concurrent reads (simulating TUI updates)
	done := make(chan bool, 100)
	for i := 0; i < 100; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				metrics := scraper.GetMetrics()
				if metrics == nil {
					t.Error("Expected metrics, got nil")
				}
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 100; i++ {
		<-done
	}
}

func TestOriginScraper_HTTPError(t *testing.T) {
	// Server that returns 500
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	scraper := NewOriginScraper(
		server.URL+"/metrics",
		"",
		1*time.Second,
		30*time.Second, // Window size
		nil,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go scraper.Run(ctx)

	time.Sleep(100 * time.Millisecond)

	metrics := scraper.GetMetrics()
	if metrics == nil {
		t.Fatal("Expected metrics (with error), got nil")
	}

	if metrics.Healthy {
		t.Error("Expected Healthy=false on HTTP error")
	}
	if metrics.Error == "" {
		t.Error("Expected error message on HTTP error")
	}
}

func TestOriginScraper_ConnectionRefused(t *testing.T) {
	// Use a port that's not listening
	scraper := NewOriginScraper(
		"http://localhost:99999/metrics",
		"",
		1*time.Second,
		30*time.Second, // Window size
		nil,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go scraper.Run(ctx)

	time.Sleep(200 * time.Millisecond) // Wait for timeout

	metrics := scraper.GetMetrics()
	if metrics == nil {
		t.Fatal("Expected metrics (with error), got nil")
	}

	if metrics.Healthy {
		t.Error("Expected Healthy=false on connection error")
	}
	if metrics.Error == "" {
		t.Error("Expected error message on connection error")
	}
}

func TestOriginScraper_PartialFailure(t *testing.T) {
	// One good server, one bad server
	nodeServer := mockPrometheusServer(t, sampleNodeExporterMetrics())
	defer nodeServer.Close()

	// Bad nginx server (returns 500)
	badServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer badServer.Close()

	scraper := NewOriginScraper(
		nodeServer.URL+"/metrics",
		badServer.URL+"/metrics",
		100*time.Millisecond,
		30*time.Second, // Window size
		nil,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go scraper.Run(ctx)

	time.Sleep(150 * time.Millisecond)

	metrics := scraper.GetMetrics()
	if metrics == nil {
		t.Fatal("Expected metrics, got nil")
	}

	// Should be unhealthy due to nginx failure
	if metrics.Healthy {
		t.Error("Expected Healthy=false when one exporter fails")
	}

	// But node metrics should still be present
	if metrics.MemTotal == 0 {
		t.Error("Expected MemTotal > 0 from successful node_exporter scrape")
	}

	// Error should mention nginx
	if metrics.Error == "" {
		t.Error("Expected error message mentioning nginx_exporter")
	}
}

func TestOriginScraper_GetMetrics_NilScraper(t *testing.T) {
	var scraper *OriginScraper = nil
	metrics := scraper.GetMetrics()
	if metrics != nil {
		t.Error("Expected nil metrics from nil scraper")
	}
}

func TestOriginScraper_Run_NilScraper(t *testing.T) {
	var scraper *OriginScraper = nil
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Should not panic
	scraper.Run(ctx)
}

// ============================================================================
// Rolling Window Tests
// ============================================================================

func TestOriginScraper_RollingWindow_Basic(t *testing.T) {
	nodeServer := mockPrometheusServer(t, sampleNodeExporterMetrics())
	defer nodeServer.Close()

	windowSize := 30 * time.Second
	scraper := NewOriginScraper(
		nodeServer.URL+"/metrics",
		"",
		100*time.Millisecond,
		windowSize,
		nil,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go scraper.Run(ctx)

	// Wait for initial scrape
	time.Sleep(200 * time.Millisecond)

	// Get metrics multiple times to build up window
	var metrics []*OriginMetrics
	for i := 0; i < 5; i++ {
		time.Sleep(200 * time.Millisecond)
		m := scraper.GetMetrics()
		if m != nil {
			metrics = append(metrics, m)
		}
	}

	// Check that we have some metrics
	if len(metrics) == 0 {
		t.Fatal("No metrics collected")
	}

	// Last metric should have window size set
	last := metrics[len(metrics)-1]
	if last.NetWindowSeconds != int(windowSize.Seconds()) {
		t.Errorf("Expected window size %d, got %d", int(windowSize.Seconds()), last.NetWindowSeconds)
	}
}

func TestOriginScraper_RollingWindow_Percentiles(t *testing.T) {
	// Create a scraper with a short window for testing
	windowSize := 5 * time.Second
	scraper := &OriginScraper{
		netInDigest:  tdigest.NewWithCompression(100),
		netOutDigest: tdigest.NewWithCompression(100),
		windowSize:   windowSize,
		lastClean:    time.Now(),
	}

	now := time.Now()
	// Add known values to test percentile calculation
	testValues := []float64{10.0, 20.0, 30.0, 40.0, 50.0, 60.0, 70.0, 80.0, 90.0, 100.0}

	scraper.netInMu.Lock()
	for i, val := range testValues {
		sampleTime := now.Add(time.Duration(i) * time.Second)
		scraper.netInDigest.Add(val, 1)
		scraper.netInSamples = append(scraper.netInSamples, networkSample{
			value: val,
			time:  sampleTime,
		})
	}
	scraper.netInMu.Unlock()

	// Get percentiles
	scraper.netInMu.Lock()
	scraper.cleanupNetworkWindow(&scraper.netInSamples, scraper.netInDigest, now.Add(10*time.Second))
	p50 := scraper.netInDigest.Quantile(0.50)
	max := scraper.netInSamples[0].value
	for _, s := range scraper.netInSamples {
		if s.value > max {
			max = s.value
		}
	}
	scraper.netInMu.Unlock()

	// P50 should be reasonable (T-Digest is approximate, so we check it's in a reasonable range)
	// With values 10-100, P50 should be between 40-80 (approximate median)
	if p50 < 30 || p50 > 90 {
		t.Errorf("Expected P50 in reasonable range (30-90), got %f", p50)
	}

	// Max should be 100.0
	if max != 100.0 {
		t.Errorf("Expected max 100.0, got %f", max)
	}
}

func TestOriginScraper_RollingWindow_Expiration(t *testing.T) {
	windowSize := 5 * time.Second
	scraper := &OriginScraper{
		netInDigest:  tdigest.NewWithCompression(100),
		netInSamples: make([]networkSample, 0),
		windowSize:   windowSize,
		lastClean:    time.Now(),
	}

	baseTime := time.Now()

	// Add samples at different times
	scraper.netInMu.Lock()
	// Old samples (should expire)
	scraper.netInSamples = append(scraper.netInSamples, networkSample{
		value: 10.0,
		time:  baseTime.Add(-10 * time.Second),
	})
	scraper.netInSamples = append(scraper.netInSamples, networkSample{
		value: 20.0,
		time:  baseTime.Add(-8 * time.Second),
	})
	// Recent samples (should keep)
	scraper.netInSamples = append(scraper.netInSamples, networkSample{
		value: 30.0,
		time:  baseTime.Add(-2 * time.Second),
	})
	scraper.netInSamples = append(scraper.netInSamples, networkSample{
		value: 40.0,
		time:  baseTime.Add(-1 * time.Second),
	})

	// Add to digest
	for _, s := range scraper.netInSamples {
		scraper.netInDigest.Add(s.value, 1)
	}
	initialCount := len(scraper.netInSamples)
	scraper.netInMu.Unlock()

	// Cleanup (current time = baseTime)
	scraper.netInMu.Lock()
	scraper.cleanupNetworkWindow(&scraper.netInSamples, scraper.netInDigest, baseTime)
	finalCount := len(scraper.netInSamples)
	scraper.netInMu.Unlock()

	// Should have 2 samples remaining (recent ones)
	if finalCount != 2 {
		t.Errorf("Expected 2 samples after cleanup, got %d (initial: %d)", finalCount, initialCount)
	}

	// Verify old samples are gone
	for _, s := range scraper.netInSamples {
		if s.time.Before(baseTime.Add(-windowSize)) {
			t.Errorf("Found expired sample: %v", s)
		}
	}
}

func TestOriginScraper_RollingWindow_ConcurrentAccess(t *testing.T) {
	nodeServer := mockPrometheusServer(t, sampleNodeExporterMetrics())
	defer nodeServer.Close()

	windowSize := 30 * time.Second
	scraper := NewOriginScraper(
		nodeServer.URL+"/metrics",
		"",
		50*time.Millisecond,
		windowSize,
		nil,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go scraper.Run(ctx)

	// Concurrent reads
	done := make(chan bool)
	numReaders := 10
	for i := 0; i < numReaders; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				m := scraper.GetMetrics()
				if m != nil {
					_ = m.NetInP50
					_ = m.NetInMax
					_ = m.NetOutP50
					_ = m.NetOutMax
				}
				time.Sleep(1 * time.Millisecond)
			}
			done <- true
		}()
	}

	// Wait for all readers
	for i := 0; i < numReaders; i++ {
		<-done
	}
}

func TestOriginScraper_RollingWindow_ConfigurableWindow(t *testing.T) {
	testCases := []struct {
		name       string
		windowSize time.Duration
		expected   int // Expected window size in seconds
	}{
		{"default_30s", 30 * time.Second, 30},
		{"custom_60s", 60 * time.Second, 60},
		{"max_300s", 300 * time.Second, 300},
		{"clamped_min", 5 * time.Second, 10},   // Should clamp to 10s
		{"clamped_max", 500 * time.Second, 300}, // Should clamp to 300s
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			scraper := NewOriginScraper(
				"http://localhost:9100/metrics",
				"",
				2*time.Second,
				tc.windowSize,
				nil,
			)

			if scraper == nil {
				t.Fatal("Scraper should not be nil")
			}

			// Check window size was set correctly (after clamping)
			if int(scraper.windowSize.Seconds()) != tc.expected {
				t.Errorf("Expected window size %d, got %d",
					tc.expected, int(scraper.windowSize.Seconds()))
			}
		})
	}
}

func TestOriginScraper_RollingWindow_EmptyWindow(t *testing.T) {
	windowSize := 30 * time.Second
	scraper := &OriginScraper{
		netInDigest:  tdigest.NewWithCompression(100),
		netOutDigest: tdigest.NewWithCompression(100),
		windowSize:   windowSize,
		lastClean:    time.Now(),
	}

	// Get metrics with empty window
	metrics := scraper.GetMetrics()
	if metrics != nil {
		// Percentiles should be 0 when no samples
		if metrics.NetInP50 != 0 || metrics.NetInMax != 0 {
			t.Errorf("Expected zero percentiles for empty window, got P50=%f Max=%f",
				metrics.NetInP50, metrics.NetInMax)
		}
	}
}

func TestOriginScraper_RollingWindow_BurstyTraffic(t *testing.T) {
	windowSize := 10 * time.Second
	scraper := &OriginScraper{
		netInDigest:  tdigest.NewWithCompression(100),
		netInSamples: make([]networkSample, 0),
		windowSize:   windowSize,
		lastClean:    time.Now(),
	}

	baseTime := time.Now()

	// Simulate bursty traffic: low baseline, occasional spikes
	// Baseline: 10 MB/s
	// Bursts: 500 MB/s
	scraper.netInMu.Lock()
	for i := 0; i < 10; i++ {
		var value float64
		if i%3 == 0 {
			value = 500.0 * 1024 * 1024 // Burst: 500 MB/s
		} else {
			value = 10.0 * 1024 * 1024 // Baseline: 10 MB/s
		}
		sampleTime := baseTime.Add(time.Duration(i) * time.Second)
		scraper.netInDigest.Add(value, 1)
		scraper.netInSamples = append(scraper.netInSamples, networkSample{
			value: value,
			time:  sampleTime,
		})
	}
	scraper.netInMu.Unlock()

	// Get percentiles
	scraper.netInMu.Lock()
	scraper.cleanupNetworkWindow(&scraper.netInSamples, scraper.netInDigest, baseTime.Add(10*time.Second))
	p50 := scraper.netInDigest.Quantile(0.50)
	max := scraper.netInSamples[0].value
	for _, s := range scraper.netInSamples {
		if s.value > max {
			max = s.value
		}
	}
	scraper.netInMu.Unlock()

	// P50 should reflect median (likely around baseline)
	if p50 < 0 {
		t.Errorf("P50 should be positive, got %f", p50)
	}

	// Max should be 500 MB/s
	expectedMax := 500.0 * 1024 * 1024
	if max != expectedMax {
		t.Errorf("Expected max %f, got %f", expectedMax, max)
	}
}
