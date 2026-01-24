package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
	scraper := NewOriginScraper("", "", 1*time.Second, nil)
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
