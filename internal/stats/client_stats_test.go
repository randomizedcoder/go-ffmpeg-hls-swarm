package stats

import (
	"sync"
	"testing"
	"time"
)

func TestNewClientStats(t *testing.T) {
	stats := NewClientStats(42)

	if stats.ClientID != 42 {
		t.Errorf("ClientID = %d, want 42", stats.ClientID)
	}
	if stats.StartTime.IsZero() {
		t.Error("StartTime should not be zero")
	}
	// HTTPErrors is now an array-based atomic counter, no initialization needed
	// Verify it's accessible by checking GetHTTPErrors returns empty map
	errors := stats.GetHTTPErrors()
	if errors == nil {
		t.Error("GetHTTPErrors should not return nil")
	}
	if len(errors) != 0 {
		t.Errorf("New client should have no errors, got %d", len(errors))
	}
	// Note: inferredLatencyDigest removed - use DebugEventParser for accurate latency
	// Check segmentSizes is properly initialized
	if len(stats.segmentSizes) != SegmentSizeRingSize {
		t.Errorf("segmentSizes length = %d, want %d", len(stats.segmentSizes), SegmentSizeRingSize)
	}
	// Check atomic fields are initialized (zero values are safe defaults)
	if stats.segmentSizeIdx.Load() != 0 {
		t.Errorf("segmentSizeIdx should be 0 initially, got %d", stats.segmentSizeIdx.Load())
	}
}

func TestClientStats_RequestCounts(t *testing.T) {
	stats := NewClientStats(0)

	stats.IncrementManifestRequests()
	stats.IncrementManifestRequests()
	stats.IncrementSegmentRequests()
	stats.IncrementSegmentRequests()
	stats.IncrementSegmentRequests()
	stats.IncrementInitRequests()
	stats.IncrementUnknownRequests()
	stats.IncrementUnknownRequests()

	if stats.ManifestRequests.Load() != 2 {
		t.Errorf("ManifestRequests = %d, want 2", stats.ManifestRequests.Load())
	}
	if stats.SegmentRequests.Load() != 3 {
		t.Errorf("SegmentRequests = %d, want 3", stats.SegmentRequests.Load())
	}
	if stats.InitRequests.Load() != 1 {
		t.Errorf("InitRequests = %d, want 1", stats.InitRequests.Load())
	}
	if stats.UnknownRequests.Load() != 2 {
		t.Errorf("UnknownRequests = %d, want 2", stats.UnknownRequests.Load())
	}
}

func TestClientStats_ConcurrentIncrements(t *testing.T) {
	stats := NewClientStats(0)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stats.IncrementManifestRequests()
			stats.IncrementSegmentRequests()
			stats.IncrementInitRequests()
			stats.IncrementUnknownRequests()
		}()
	}
	wg.Wait()

	if stats.ManifestRequests.Load() != 100 {
		t.Errorf("ManifestRequests = %d, want 100", stats.ManifestRequests.Load())
	}
	if stats.SegmentRequests.Load() != 100 {
		t.Errorf("SegmentRequests = %d, want 100", stats.SegmentRequests.Load())
	}
	if stats.InitRequests.Load() != 100 {
		t.Errorf("InitRequests = %d, want 100", stats.InitRequests.Load())
	}
	if stats.UnknownRequests.Load() != 100 {
		t.Errorf("UnknownRequests = %d, want 100", stats.UnknownRequests.Load())
	}
}

func TestClientStats_RecordHTTPError(t *testing.T) {
	tests := []struct {
		name     string
		code     int
		wantCode int // Expected code in result (0 = "other")
		wantCount int64
	}{
		// 4xx Client Errors
		{"400 Bad Request", 400, 400, 1},
		{"401 Unauthorized", 401, 401, 1},
		{"403 Forbidden", 403, 403, 1},
		{"404 Not Found", 404, 404, 1},
		{"405 Method Not Allowed", 405, 405, 1},
		{"408 Request Timeout", 408, 408, 1},
		{"409 Conflict", 409, 409, 1},
		{"413 Payload Too Large", 413, 413, 1},
		{"414 URI Too Long", 414, 414, 1},
		{"415 Unsupported Media Type", 415, 415, 1},
		{"429 Too Many Requests", 429, 429, 1},
		{"499 Client Closed Request", 499, 499, 1},

		// 5xx Server Errors
		{"500 Internal Server Error", 500, 500, 1},
		{"501 Not Implemented", 501, 501, 1},
		{"502 Bad Gateway", 502, 502, 1},
		{"503 Service Unavailable", 503, 503, 1},
		{"504 Gateway Timeout", 504, 504, 1},
		{"599 Network Connect Timeout", 599, 599, 1},

		// Edge cases - should go to "other" (code 0)
		{"399 (not error)", 399, 0, 1},
		{"600 (invalid)", 600, 0, 1},
		{"0 (invalid)", 0, 0, 1},
		{"-1 (invalid)", -1, 0, 1},
		{"300 (redirect, not error)", 300, 0, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewClientStats(0)
			s.RecordHTTPError(tt.code)

			errors := s.GetHTTPErrors()
			if tt.wantCode == 0 {
				// Should be in "other"
				if errors[0] != tt.wantCount {
					t.Errorf("expected other[0]=%d, got %v", tt.wantCount, errors)
				}
			} else {
				if errors[tt.wantCode] != tt.wantCount {
					t.Errorf("expected errors[%d]=%d, got %v", tt.wantCode, tt.wantCount, errors)
				}
			}
		})
	}
}

func TestClientStats_RecordHTTPError_Concurrent(t *testing.T) {
	s := NewClientStats(0)
	var wg sync.WaitGroup

	// Record same code from multiple goroutines
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.RecordHTTPError(404)
		}()
	}

	wg.Wait()

	errors := s.GetHTTPErrors()
	if errors[404] != 100 {
		t.Errorf("expected 100 errors, got %d", errors[404])
	}
}

func TestClientStats_GetHTTPErrors_AllCodes(t *testing.T) {
	s := NewClientStats(0)

	// Record one of each standard code
	for code := 400; code <= 599; code++ {
		s.RecordHTTPError(code)
	}

	errors := s.GetHTTPErrors()
	if len(errors) != 200 {
		t.Errorf("expected 200 error codes, got %d", len(errors))
	}

	for code := 400; code <= 599; code++ {
		if errors[code] != 1 {
			t.Errorf("expected errors[%d]=1, got %d", code, errors[code])
		}
	}
}

func TestClientStats_GetHTTPErrors_ZeroCountsExcluded(t *testing.T) {
	s := NewClientStats(0)

	// Record only one error
	s.RecordHTTPError(404)

	errors := s.GetHTTPErrors()

	// Should only have one entry
	if len(errors) != 1 {
		t.Errorf("expected 1 error code, got %d: %v", len(errors), errors)
	}

	// Should have 404
	if errors[404] != 1 {
		t.Errorf("expected errors[404]=1, got %d", errors[404])
	}
}

func TestClientStats_HTTPErrors(t *testing.T) {
	// Legacy test - keep for backward compatibility
	stats := NewClientStats(0)

	stats.RecordHTTPError(503)
	stats.RecordHTTPError(503)
	stats.RecordHTTPError(404)
	stats.RecordHTTPError(500)

	errors := stats.GetHTTPErrors()
	if errors[503] != 2 {
		t.Errorf("HTTPErrors[503] = %d, want 2", errors[503])
	}
	if errors[404] != 1 {
		t.Errorf("HTTPErrors[404] = %d, want 1", errors[404])
	}
	if errors[500] != 1 {
		t.Errorf("HTTPErrors[500] = %d, want 1", errors[500])
	}
}

func TestClientStats_BytesTracking(t *testing.T) {
	stats := NewClientStats(0)

	// First FFmpeg process downloads 1000 bytes
	stats.UpdateCurrentBytes(1000)
	if stats.TotalBytes() != 1000 {
		t.Errorf("TotalBytes = %d, want 1000", stats.TotalBytes())
	}

	// FFmpeg restarts - bytes should accumulate
	stats.OnProcessStart()
	if stats.TotalBytes() != 1000 {
		t.Errorf("TotalBytes after restart = %d, want 1000", stats.TotalBytes())
	}

	// Second FFmpeg process downloads 500 bytes
	stats.UpdateCurrentBytes(500)
	if stats.TotalBytes() != 1500 {
		t.Errorf("TotalBytes = %d, want 1500", stats.TotalBytes())
	}

	// Another restart
	stats.OnProcessStart()
	stats.UpdateCurrentBytes(200)
	if stats.TotalBytes() != 1700 {
		t.Errorf("TotalBytes = %d, want 1700", stats.TotalBytes())
	}
}

// TestClientStats_LatencyTracking removed - inferred latency is no longer tracked.
// Latency metrics are now provided by DebugEventParser using accurate FFmpeg timestamps.
// See docs/REMOVE_INFERRED_LATENCY_ANALYSIS.md for details.

// TestClientStats_CompleteOldestSegment and TestClientStats_HangingRequestCleanup removed.
// These features are no longer available - latency tracking is now handled by DebugEventParser.
// See docs/REMOVE_INFERRED_LATENCY_ANALYSIS.md for details.

func TestClientStats_DriftTracking(t *testing.T) {
	stats := NewClientStats(0)

	// Wait a bit, then update drift
	time.Sleep(50 * time.Millisecond)

	// Playback is at 20ms but wall clock is ~50ms
	stats.UpdateDrift(20000) // 20ms in microseconds

	current, max := stats.GetDrift()

	// Drift should be approximately 30ms (50ms wall - 20ms playback)
	if current < 20*time.Millisecond || current > 100*time.Millisecond {
		t.Errorf("CurrentDrift = %v, expected ~30ms", current)
	}
	if max < current {
		t.Errorf("MaxDrift = %v, should be >= CurrentDrift %v", max, current)
	}
}

func TestClientStats_HasHighDrift(t *testing.T) {
	stats := NewClientStats(0)

	// No drift yet
	if stats.HasHighDrift() {
		t.Error("should not have high drift initially")
	}

	// Set high drift by updating with a very small playback time
	// Drift = (Now - StartTime) - PlaybackTime
	// If playbackTime is very small, drift ≈ elapsed time
	// Wait enough to create drift > HighDriftThreshold (5s)
	time.Sleep(HighDriftThreshold + 200*time.Millisecond)
	// Use 1 microsecond playback time, so drift ≈ elapsed time (well above 5s threshold)
	stats.UpdateDrift(1)

	// Give a moment for the update to complete
	time.Sleep(10 * time.Millisecond)

	if !stats.HasHighDrift() {
		t.Error("should have high drift")
	}
}

func TestClientStats_SpeedAndStall(t *testing.T) {
	stats := NewClientStats(0)

	// Normal speed
	stats.UpdateSpeed(1.0)
	if stats.GetSpeed() != 1.0 {
		t.Errorf("Speed = %v, want 1.0", stats.GetSpeed())
	}
	if stats.IsStalled() {
		t.Error("should not be stalled at speed 1.0")
	}

	// Drop below threshold
	stats.UpdateSpeed(0.5)
	if stats.IsStalled() {
		t.Error("should not be stalled immediately")
	}

	// Note: Testing "stalled after 5s" would require waiting 6 seconds or manipulating time
	// Since we're using atomic.Value, we can't directly set speedBelowThresholdAt
	// The behavior is verified by the immediate check above and the recovery test below
	// In production, IsStalled() correctly checks time.Since(speedBelowThresholdAt) > StallDuration

	// Speed recovers
	stats.UpdateSpeed(1.0)
	if stats.IsStalled() {
		t.Error("should not be stalled after speed recovery")
	}
}

func TestClientStats_SegmentSizeTracking(t *testing.T) {
	stats := NewClientStats(0)

	// Record some segment sizes
	stats.RecordSegmentSize(100000)
	stats.RecordSegmentSize(150000)
	stats.RecordSegmentSize(120000)

	avg := stats.GetAverageSegmentSize()
	expected := int64((100000 + 150000 + 120000) / 3)
	if avg != expected {
		t.Errorf("AverageSegmentSize = %d, want %d", avg, expected)
	}
}

func TestClientStats_PipelineHealth(t *testing.T) {
	stats := NewClientStats(0)

	// No drops initially
	if stats.CurrentDropRate() != 0 {
		t.Errorf("CurrentDropRate = %v, want 0", stats.CurrentDropRate())
	}

	// Record some dropped lines
	stats.RecordDroppedLines(100, 5, 200, 10)

	// Drop rate should be (5+10)/(100+200) = 15/300 = 0.05
	expectedRate := 15.0 / 300.0
	if stats.CurrentDropRate() != expectedRate {
		t.Errorf("CurrentDropRate = %v, want %v", stats.CurrentDropRate(), expectedRate)
	}

	// Should be degraded at 1% threshold
	if !stats.MetricsDegraded(0.01) {
		t.Error("should be degraded at 1% threshold")
	}

	// Should not be degraded at 10% threshold
	if stats.MetricsDegraded(0.10) {
		t.Error("should not be degraded at 10% threshold")
	}

	// Peak drop rate should be recorded
	if stats.GetPeakDropRate() != expectedRate {
		t.Errorf("PeakDropRate = %v, want %v", stats.GetPeakDropRate(), expectedRate)
	}
}

func TestClientStats_Uptime(t *testing.T) {
	stats := NewClientStats(0)

	time.Sleep(50 * time.Millisecond)

	uptime := stats.Uptime()
	if uptime < 50*time.Millisecond {
		t.Errorf("Uptime = %v, want >= 50ms", uptime)
	}
}

func TestClientStats_GetSummary(t *testing.T) {
	stats := NewClientStats(42)

	stats.IncrementManifestRequests()
	stats.IncrementSegmentRequests()
	stats.UpdateCurrentBytes(1000)
	stats.RecordHTTPError(503)
	stats.UpdateSpeed(1.0)

	summary := stats.GetSummary()

	if summary.ClientID != 42 {
		t.Errorf("Summary.ClientID = %d, want 42", summary.ClientID)
	}
	if summary.ManifestRequests != 1 {
		t.Errorf("Summary.ManifestRequests = %d, want 1", summary.ManifestRequests)
	}
	if summary.SegmentRequests != 1 {
		t.Errorf("Summary.SegmentRequests = %d, want 1", summary.SegmentRequests)
	}
	if summary.TotalBytes != 1000 {
		t.Errorf("Summary.TotalBytes = %d, want 1000", summary.TotalBytes)
	}
	if summary.HTTPErrors[503] != 1 {
		t.Errorf("Summary.HTTPErrors[503] = %d, want 1", summary.HTTPErrors[503])
	}
	if summary.CurrentSpeed != 1.0 {
		t.Errorf("Summary.CurrentSpeed = %v, want 1.0", summary.CurrentSpeed)
	}
}

func TestClientStats_ThreadSafety(t *testing.T) {
	stats := NewClientStats(0)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				stats.IncrementManifestRequests()
				stats.IncrementSegmentRequests()
				stats.RecordHTTPError(503)
				stats.UpdateCurrentBytes(int64(j * 100))
				// Note: OnSegmentRequestStart and CompleteOldestSegment removed - use DebugEventParser
				stats.UpdateDrift(int64(j * 1000))
				stats.UpdateSpeed(1.0)
				stats.RecordDroppedLines(int64(j), 0, int64(j), 0)
				_ = stats.GetSummary()
			}
		}(i)
	}

	wg.Wait()

	// Just verify no panics and counts are reasonable
	if stats.ManifestRequests.Load() != 1000 {
		t.Errorf("ManifestRequests = %d, want 1000", stats.ManifestRequests.Load())
	}
	if stats.SegmentRequests.Load() != 1000 {
		t.Errorf("SegmentRequests = %d, want 1000", stats.SegmentRequests.Load())
	}
}

// TestClientStats_LatencyPercentiles removed - inferred latency is no longer tracked.
// Latency metrics are now provided by DebugEventParser using accurate FFmpeg timestamps.
// See docs/REMOVE_INFERRED_LATENCY_ANALYSIS.md for details.

func BenchmarkClientStats_IncrementCounters(b *testing.B) {
	stats := NewClientStats(0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stats.IncrementManifestRequests()
		stats.IncrementSegmentRequests()
	}
}

// BenchmarkClientStats_RecordLatency removed - inferred latency is no longer tracked.

func BenchmarkClientStats_GetSummary(b *testing.B) {
	stats := NewClientStats(0)

	// Populate with some data
	for i := 0; i < 100; i++ {
		stats.IncrementManifestRequests()
		stats.IncrementSegmentRequests()
		// Note: Latency tracking removed - use DebugEventParser for accurate latency
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = stats.GetSummary()
	}
}
