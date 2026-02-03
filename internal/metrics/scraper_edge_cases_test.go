package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Segment Scraper Edge Cases

func TestSegmentScraper_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"invalid json`)) // Malformed JSON
	}))
	defer server.Close()

	scraper := NewSegmentScraper(server.URL, 100*time.Millisecond, 50*time.Millisecond, 30, nil)

	err := scraper.scrape()
	if err == nil {
		t.Error("Expected error for malformed JSON")
	}

	// Cache should be empty
	if scraper.CacheSize() != 0 {
		t.Errorf("Cache should be empty after failed scrape, got %d", scraper.CacheSize())
	}
}

func TestSegmentScraper_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`)) // Empty array
	}))
	defer server.Close()

	scraper := NewSegmentScraper(server.URL, 100*time.Millisecond, 50*time.Millisecond, 30, nil)

	err := scraper.scrape()
	if err != nil {
		t.Errorf("Empty response should not error: %v", err)
	}

	// Cache should be empty (no segments)
	if scraper.CacheSize() != 0 {
		t.Errorf("Cache should be empty for empty response, got %d", scraper.CacheSize())
	}
}

func TestSegmentScraper_HTTP500Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	scraper := NewSegmentScraper(server.URL, 100*time.Millisecond, 50*time.Millisecond, 30, nil)

	err := scraper.scrape()
	if err == nil {
		t.Error("Expected error for HTTP 500")
	}
}

func TestSegmentScraper_HTTP404Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	scraper := NewSegmentScraper(server.URL, 100*time.Millisecond, 50*time.Millisecond, 30, nil)

	err := scraper.scrape()
	if err == nil {
		t.Error("Expected error for HTTP 404")
	}
}

func TestSegmentScraper_ConnectionRefused(t *testing.T) {
	// Use a port that's unlikely to be listening
	scraper := NewSegmentScraper("http://127.0.0.1:59999/files/json/", 100*time.Millisecond, 50*time.Millisecond, 30, nil)

	err := scraper.scrape()
	if err == nil {
		t.Error("Expected error for connection refused")
	}
}

func TestSegmentScraper_NegativeSegmentSize(t *testing.T) {
	// Server returns negative size - should be handled gracefully
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"name":"seg00001.ts","type":"file","size":-1}]`))
	}))
	defer server.Close()

	scraper := NewSegmentScraper(server.URL, 100*time.Millisecond, 50*time.Millisecond, 30, nil)

	err := scraper.scrape()
	if err != nil {
		t.Errorf("Negative size should not error: %v", err)
	}

	// Should still be cached (even with negative size)
	size, ok := scraper.GetSegmentSize("seg00001.ts")
	if !ok {
		t.Error("Segment should be in cache")
	}
	if size != -1 {
		t.Errorf("Size = %d, want -1", size)
	}
}

func TestSegmentScraper_VeryLargeSegmentSize(t *testing.T) {
	// Server returns very large size - test for overflow
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"name":"seg00001.ts","type":"file","size":9223372036854775807}]`)) // Max int64
	}))
	defer server.Close()

	scraper := NewSegmentScraper(server.URL, 100*time.Millisecond, 50*time.Millisecond, 30, nil)

	err := scraper.scrape()
	if err != nil {
		t.Errorf("Large size should not error: %v", err)
	}

	size, ok := scraper.GetSegmentSize("seg00001.ts")
	if !ok {
		t.Error("Segment should be in cache")
	}
	if size != 9223372036854775807 {
		t.Errorf("Size = %d, want max int64", size)
	}
}

func TestSegmentScraper_SpecialCharactersInFilename(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{"name":"seg 001.ts","type":"file","size":1000},
			{"name":"seg-special_chars.ts","type":"file","size":2000},
			{"name":"seg.multiple.dots.ts","type":"file","size":3000}
		]`))
	}))
	defer server.Close()

	scraper := NewSegmentScraper(server.URL, 100*time.Millisecond, 50*time.Millisecond, 30, nil)

	err := scraper.scrape()
	if err != nil {
		t.Errorf("Special chars should not error: %v", err)
	}

	// All should be cached
	if scraper.CacheSize() != 3 {
		t.Errorf("CacheSize = %d, want 3", scraper.CacheSize())
	}
}

func TestSegmentScraper_EmptyFilename(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"name":"","type":"file","size":1000}]`))
	}))
	defer server.Close()

	scraper := NewSegmentScraper(server.URL, 100*time.Millisecond, 50*time.Millisecond, 30, nil)

	err := scraper.scrape()
	if err != nil {
		t.Errorf("Empty filename should not error: %v", err)
	}

	// Empty filename should still be cached
	_, ok := scraper.GetSegmentSize("")
	if !ok {
		t.Error("Empty filename segment should be cached")
	}
}

func TestSegmentScraper_SlowServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond) // Slow response
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"name":"seg00001.ts","type":"file","size":1000}]`))
	}))
	defer server.Close()

	// Scraper with short timeout
	scraper := NewSegmentScraper(server.URL, 100*time.Millisecond, 50*time.Millisecond, 30, nil)
	// Note: The scraper has a 5s timeout by default, so this should succeed

	err := scraper.scrape()
	if err != nil {
		t.Errorf("Slow server should succeed with default timeout: %v", err)
	}
}

func TestSegmentScraper_ConcurrentScrapeAndRead(t *testing.T) {
	requestCount := 0
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestCount++
		count := requestCount
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		// Return different data each time to simulate live stream
		w.Write([]byte(`[{"name":"seg` + string(rune('0'+count%10)) + `.ts","type":"file","size":1000}]`))
	}))
	defer server.Close()

	scraper := NewSegmentScraper(server.URL, 50*time.Millisecond, 10*time.Millisecond, 30, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Start scraper
	go scraper.Run(ctx)

	// Concurrent reads while scraper is running
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				// These should not panic
				_ = scraper.CacheSize()
				_ = scraper.GetHighestSegmentNumber()
				_, _ = scraper.GetSegmentSize("seg1.ts")
				time.Sleep(time.Millisecond)
			}
		}()
	}
	wg.Wait()
}

func TestSegmentScraper_ZeroWindowSize(t *testing.T) {
	// Window size of 0 should use default
	scraper := NewSegmentScraper("http://test", 1*time.Second, 500*time.Millisecond, 0, nil)

	if scraper.windowSize <= 0 {
		t.Errorf("Zero window size should use default, got %d", scraper.windowSize)
	}
}

func TestSegmentScraper_ZeroInterval(t *testing.T) {
	// Interval of 0 should use default
	scraper := NewSegmentScraper("http://test", 0, 500*time.Millisecond, 30, nil)

	if scraper.interval <= 0 {
		t.Errorf("Zero interval should use default, got %v", scraper.interval)
	}
}

func TestSegmentScraper_ZeroJitter(t *testing.T) {
	// Jitter of 0 should use default (to avoid Int63n(0) panic)
	scraper := NewSegmentScraper("http://test", 1*time.Second, 0, 30, nil)

	if scraper.jitter <= 0 {
		t.Errorf("Zero jitter should use default, got %v", scraper.jitter)
	}

	// Calling jitteredInterval should not panic
	_ = scraper.jitteredInterval()
}

// Origin Scraper Edge Cases

func TestOriginScraper_MalformedPrometheusMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not valid prometheus format
			{{{invalid}}}
		`))
	}))
	defer server.Close()

	scraper := NewOriginScraper(server.URL, "", 100*time.Millisecond, 30*time.Second, nil)

	// Should not panic
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	scraper.Run(ctx)

	// GetMetrics should return something (even if partially failed)
	metrics := scraper.GetMetrics()
	if metrics == nil {
		t.Error("GetMetrics should not return nil")
	}
}

func TestOriginScraper_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(``)) // Empty response
	}))
	defer server.Close()

	scraper := NewOriginScraper(server.URL, "", 100*time.Millisecond, 30*time.Second, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	scraper.Run(ctx)

	metrics := scraper.GetMetrics()
	if metrics == nil {
		t.Error("GetMetrics should not return nil")
	}
}

func TestOriginScraper_VeryLargeMetricValues(t *testing.T) {
	// Test with very large metric values (potential overflow)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`# HELP test Test
# TYPE test gauge
node_network_receive_bytes_total{device="eth0"} 9223372036854775807
node_network_transmit_bytes_total{device="eth0"} 9223372036854775807
`))
	}))
	defer server.Close()

	scraper := NewOriginScraper(server.URL, "", 100*time.Millisecond, 30*time.Second, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	scraper.Run(ctx)

	metrics := scraper.GetMetrics()
	if metrics == nil {
		t.Fatal("GetMetrics should not return nil")
	}

	// Values should not cause issues (metrics are calculated as rates, not raw totals)
	// The test verifies the scraper doesn't panic on large values
	if metrics.NetInRate < 0 {
		t.Error("NetInRate should not be negative")
	}
}

func TestOriginScraper_ZeroInterval(t *testing.T) {
	// Scraper with 0 interval should use default or handle gracefully
	scraper := NewOriginScraper("http://test", "", 0, 30*time.Second, nil)

	// Should not panic and should have a reasonable interval
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Just verify it doesn't panic
	scraper.Run(ctx)
}

func TestOriginScraper_BothURLsEmpty(t *testing.T) {
	// When both URLs are empty, NewOriginScraper returns nil (feature disabled)
	scraper := NewOriginScraper("", "", 100*time.Millisecond, 30*time.Second, nil)

	if scraper != nil {
		t.Error("NewOriginScraper should return nil when both URLs are empty (feature disabled)")
	}

	// Run should handle nil scraper gracefully
	var nilScraper *OriginScraper
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	nilScraper.Run(ctx) // Should not panic
}
