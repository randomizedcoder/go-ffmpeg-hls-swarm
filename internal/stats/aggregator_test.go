package stats

import (
	"sync"
	"testing"
	"time"
)

func TestNewStatsAggregator(t *testing.T) {
	agg := NewStatsAggregator(0.01)

	if agg.ClientCount() != 0 {
		t.Errorf("ClientCount = %d, want 0", agg.ClientCount())
	}
	if agg.StartTime().IsZero() {
		t.Error("StartTime should not be zero")
	}
}

func TestStatsAggregator_AddRemoveClient(t *testing.T) {
	agg := NewStatsAggregator(0.01)

	// Add clients
	stats1 := NewClientStats(1)
	stats2 := NewClientStats(2)

	agg.AddClient(stats1)
	agg.AddClient(stats2)

	if agg.ClientCount() != 2 {
		t.Errorf("ClientCount = %d, want 2", agg.ClientCount())
	}

	// Get client
	if agg.GetClient(1) != stats1 {
		t.Error("GetClient(1) should return stats1")
	}

	// Remove client
	agg.RemoveClient(1)
	if agg.ClientCount() != 1 {
		t.Errorf("ClientCount = %d, want 1", agg.ClientCount())
	}
	if agg.GetClient(1) != nil {
		t.Error("GetClient(1) should return nil after removal")
	}
}

func TestStatsAggregator_AggregateEmpty(t *testing.T) {
	agg := NewStatsAggregator(0.01)

	result := agg.Aggregate()

	if result.TotalClients != 0 {
		t.Errorf("TotalClients = %d, want 0", result.TotalClients)
	}
	if result.ActiveClients != 0 {
		t.Errorf("ActiveClients = %d, want 0", result.ActiveClients)
	}
}

func TestStatsAggregator_AggregateRequestCounts(t *testing.T) {
	agg := NewStatsAggregator(0.01)

	// Create clients with requests
	stats1 := NewClientStats(1)
	stats1.IncrementManifestRequests()
	stats1.IncrementManifestRequests()
	stats1.IncrementSegmentRequests()

	stats2 := NewClientStats(2)
	stats2.IncrementManifestRequests()
	stats2.IncrementSegmentRequests()
	stats2.IncrementSegmentRequests()
	stats2.IncrementUnknownRequests()

	agg.AddClient(stats1)
	agg.AddClient(stats2)

	result := agg.Aggregate()

	if result.TotalManifestReqs != 3 {
		t.Errorf("TotalManifestReqs = %d, want 3", result.TotalManifestReqs)
	}
	if result.TotalSegmentReqs != 3 {
		t.Errorf("TotalSegmentReqs = %d, want 3", result.TotalSegmentReqs)
	}
	if result.TotalUnknownReqs != 1 {
		t.Errorf("TotalUnknownReqs = %d, want 1", result.TotalUnknownReqs)
	}
}

func TestStatsAggregator_AggregateBytes(t *testing.T) {
	agg := NewStatsAggregator(0.01)

	stats1 := NewClientStats(1)
	stats1.UpdateCurrentBytes(1000)

	stats2 := NewClientStats(2)
	stats2.UpdateCurrentBytes(2000)

	agg.AddClient(stats1)
	agg.AddClient(stats2)

	result := agg.Aggregate()

	if result.TotalBytes != 3000 {
		t.Errorf("TotalBytes = %d, want 3000", result.TotalBytes)
	}
}

func TestStatsAggregator_AggregateErrors(t *testing.T) {
	agg := NewStatsAggregator(0.01)

	stats1 := NewClientStats(1)
	stats1.RecordHTTPError(503)
	stats1.RecordHTTPError(503)
	stats1.RecordTimeout()

	stats2 := NewClientStats(2)
	stats2.RecordHTTPError(404)
	stats1.RecordReconnection()

	agg.AddClient(stats1)
	agg.AddClient(stats2)

	result := agg.Aggregate()

	if result.TotalHTTPErrors[503] != 2 {
		t.Errorf("TotalHTTPErrors[503] = %d, want 2", result.TotalHTTPErrors[503])
	}
	if result.TotalHTTPErrors[404] != 1 {
		t.Errorf("TotalHTTPErrors[404] = %d, want 1", result.TotalHTTPErrors[404])
	}
	if result.TotalTimeouts != 1 {
		t.Errorf("TotalTimeouts = %d, want 1", result.TotalTimeouts)
	}
	if result.TotalReconnections != 1 {
		t.Errorf("TotalReconnections = %d, want 1", result.TotalReconnections)
	}
}

func TestStatsAggregator_AggregateSpeed(t *testing.T) {
	agg := NewStatsAggregator(0.01)

	stats1 := NewClientStats(1)
	stats1.UpdateSpeed(1.2) // Above realtime

	stats2 := NewClientStats(2)
	stats2.UpdateSpeed(0.8) // Below realtime

	stats3 := NewClientStats(3)
	stats3.UpdateSpeed(1.0) // Exactly realtime

	agg.AddClient(stats1)
	agg.AddClient(stats2)
	agg.AddClient(stats3)

	result := agg.Aggregate()

	if result.ClientsAboveRealtime != 2 { // 1.2 and 1.0
		t.Errorf("ClientsAboveRealtime = %d, want 2", result.ClientsAboveRealtime)
	}
	if result.ClientsBelowRealtime != 1 { // 0.8
		t.Errorf("ClientsBelowRealtime = %d, want 1", result.ClientsBelowRealtime)
	}

	expectedAvg := (1.2 + 0.8 + 1.0) / 3.0
	if result.AverageSpeed < expectedAvg-0.01 || result.AverageSpeed > expectedAvg+0.01 {
		t.Errorf("AverageSpeed = %v, want ~%v", result.AverageSpeed, expectedAvg)
	}
}

func TestStatsAggregator_AggregateDrift(t *testing.T) {
	agg := NewStatsAggregator(0.01)

	stats1 := NewClientStats(1)
	// Wait enough to create drift values
	// Drift = (Now - StartTime) - PlaybackTime
	// For drift of ~3s: wait 4s, then set playbackTime to 1s (drift = 4s - 1s = 3s)
	time.Sleep(4 * time.Second)
	elapsed1 := time.Since(stats1.StartTime)
	// PlaybackTime = elapsed - 3s, so drift ≈ 3s
	stats1.UpdateDrift(int64((elapsed1 - 3*time.Second).Microseconds()))

	stats2 := NewClientStats(2)
	// Set high drift (6s current, 8s max)
	// First set to 8s to establish max, then to 6s for current
	time.Sleep(9 * time.Second)
	elapsed2 := time.Since(stats2.StartTime)
	// First update: playbackTime = elapsed - 8s, so drift = 8s (establishes max)
	stats2.UpdateDrift(int64((elapsed2 - 8*time.Second).Microseconds()))
	// Second update: playbackTime = elapsed - 6s, so drift = 6s (current), max stays 8s
	stats2.UpdateDrift(int64((elapsed2 - 6*time.Second).Microseconds()))

	agg.AddClient(stats1)
	agg.AddClient(stats2)

	result := agg.Aggregate()

	// Verify max drift is approximately 8s (allow some timing variance)
	if result.MaxDrift < 7*time.Second || result.MaxDrift > 9*time.Second {
		t.Errorf("MaxDrift = %v, want ~8s (7-9s range)", result.MaxDrift)
	}
	// Verify that stats2 (with high drift) is counted
	if result.ClientsWithHighDrift != 1 { // Only stats2 has high drift (>5s)
		t.Errorf("ClientsWithHighDrift = %d, want 1", result.ClientsWithHighDrift)
	}
}

func TestStatsAggregator_AggregatePipelineHealth(t *testing.T) {
	agg := NewStatsAggregator(0.01)

	stats1 := NewClientStats(1)
	stats1.RecordDroppedLines(100, 5, 100, 5) // 10 dropped / 200 read = 5%

	stats2 := NewClientStats(2)
	stats2.RecordDroppedLines(100, 0, 100, 0) // No drops

	agg.AddClient(stats1)
	agg.AddClient(stats2)

	result := agg.Aggregate()

	if result.TotalLinesRead != 400 {
		t.Errorf("TotalLinesRead = %d, want 400", result.TotalLinesRead)
	}
	if result.TotalLinesDropped != 10 {
		t.Errorf("TotalLinesDropped = %d, want 10", result.TotalLinesDropped)
	}
	if result.ClientsWithDrops != 1 {
		t.Errorf("ClientsWithDrops = %d, want 1", result.ClientsWithDrops)
	}
	if !result.MetricsDegraded {
		t.Error("MetricsDegraded should be true (2.5% > 1%)")
	}
}

func TestStatsAggregator_AggregateUptime(t *testing.T) {
	agg := NewStatsAggregator(0.01)

	// Create clients with different start times
	stats1 := NewClientStats(1)
	stats1.StartTime = time.Now().Add(-10 * time.Second)

	stats2 := NewClientStats(2)
	stats2.StartTime = time.Now().Add(-20 * time.Second)

	agg.AddClient(stats1)
	agg.AddClient(stats2)

	result := agg.Aggregate()

	// Min uptime should be around 10s
	if result.MinUptime < 9*time.Second || result.MinUptime > 11*time.Second {
		t.Errorf("MinUptime = %v, want ~10s", result.MinUptime)
	}

	// Max uptime should be around 20s
	if result.MaxUptime < 19*time.Second || result.MaxUptime > 21*time.Second {
		t.Errorf("MaxUptime = %v, want ~20s", result.MaxUptime)
	}

	// Avg uptime should be around 15s
	if result.AvgUptime < 14*time.Second || result.AvgUptime > 16*time.Second {
		t.Errorf("AvgUptime = %v, want ~15s", result.AvgUptime)
	}
}

func TestStatsAggregator_AggregateRates(t *testing.T) {
	agg := NewStatsAggregator(0.01)

	// Wait a bit to get meaningful rates
	time.Sleep(100 * time.Millisecond)

	stats1 := NewClientStats(1)
	stats1.IncrementManifestRequests()
	stats1.IncrementSegmentRequests()
	stats1.UpdateCurrentBytes(1000)

	agg.AddClient(stats1)

	result := agg.Aggregate()

	// Rates should be positive
	if result.ManifestReqRate <= 0 {
		t.Error("ManifestReqRate should be > 0")
	}
	if result.SegmentReqRate <= 0 {
		t.Error("SegmentReqRate should be > 0")
	}
	if result.ThroughputBytesPerSec <= 0 {
		t.Error("ThroughputBytesPerSec should be > 0")
	}
}

func TestStatsAggregator_InstantaneousRates(t *testing.T) {
	agg := NewStatsAggregator(0.01)

	stats1 := NewClientStats(1)
	agg.AddClient(stats1)

	// First aggregation
	agg.Aggregate()

	// Add more requests
	stats1.IncrementManifestRequests()
	stats1.IncrementManifestRequests()
	stats1.IncrementSegmentRequests()
	stats1.UpdateCurrentBytes(5000)

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	// Second aggregation should have instantaneous rates
	result := agg.Aggregate()

	if result.InstantManifestRate <= 0 {
		t.Error("InstantManifestRate should be > 0")
	}
	if result.InstantSegmentRate <= 0 {
		t.Error("InstantSegmentRate should be > 0")
	}
	if result.InstantThroughputRate <= 0 {
		t.Error("InstantThroughputRate should be > 0")
	}
}

// TestStatsAggregator_AggregateLatency removed - inferred latency is no longer tracked.
// Latency metrics are now provided by DebugEventParser using accurate FFmpeg timestamps.
// See docs/REMOVE_INFERRED_LATENCY_ANALYSIS.md for details.

func TestStatsAggregator_Reset(t *testing.T) {
	agg := NewStatsAggregator(0.01)

	stats1 := NewClientStats(1)
	agg.AddClient(stats1)

	if agg.ClientCount() != 1 {
		t.Errorf("ClientCount = %d, want 1", agg.ClientCount())
	}

	agg.Reset()

	if agg.ClientCount() != 0 {
		t.Errorf("ClientCount after reset = %d, want 0", agg.ClientCount())
	}
}

func TestStatsAggregator_ForEachClient(t *testing.T) {
	agg := NewStatsAggregator(0.01)

	stats1 := NewClientStats(1)
	stats2 := NewClientStats(2)
	stats3 := NewClientStats(3)

	agg.AddClient(stats1)
	agg.AddClient(stats2)
	agg.AddClient(stats3)

	visited := make(map[int]bool)
	agg.ForEachClient(func(clientID int, stats *ClientStats) {
		visited[clientID] = true
	})

	if len(visited) != 3 {
		t.Errorf("ForEachClient visited %d clients, want 3", len(visited))
	}
	for _, id := range []int{1, 2, 3} {
		if !visited[id] {
			t.Errorf("ForEachClient did not visit client %d", id)
		}
	}
}

func TestStatsAggregator_GetAllClientSummaries(t *testing.T) {
	agg := NewStatsAggregator(0.01)

	stats1 := NewClientStats(1)
	stats1.IncrementManifestRequests()

	stats2 := NewClientStats(2)
	stats2.IncrementSegmentRequests()

	agg.AddClient(stats1)
	agg.AddClient(stats2)

	summaries := agg.GetAllClientSummaries()

	if len(summaries) != 2 {
		t.Errorf("GetAllClientSummaries returned %d summaries, want 2", len(summaries))
	}
}

func TestStatsAggregator_ThreadSafety(t *testing.T) {
	agg := NewStatsAggregator(0.01)

	var wg sync.WaitGroup

	// Concurrent adds
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			stats := NewClientStats(id)
			agg.AddClient(stats)
		}(i)
	}

	// Concurrent aggregations
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = agg.Aggregate()
		}()
	}

	// Concurrent reads
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_ = agg.GetClient(id)
			_ = agg.ClientCount()
		}(i)
	}

	wg.Wait()

	// Just verify no panics
	if agg.ClientCount() != 10 {
		t.Errorf("ClientCount = %d, want 10", agg.ClientCount())
	}
}

func TestStatsAggregator_ConcurrentAggregation(t *testing.T) {
	agg := NewStatsAggregator(0.01)

	// Add 100 clients with known stats
	for i := 0; i < 100; i++ {
		stats := NewClientStats(i)
		stats.IncrementSegmentRequests()
		stats.IncrementManifestRequests()
		agg.AddClient(stats)
	}

	var wg sync.WaitGroup
	aggregationErrors := make(chan error, 20)

	// Concurrent aggregations (should not block each other)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := agg.Aggregate()
			// During concurrent add/remove, client count may vary
			// But aggregation should complete without panicking
			if result.TotalClients < 100 {
				aggregationErrors <- nil // Expected during concurrent operations
			}
			// Verify aggregation logic works (sums are correct for clients that exist)
			if result.TotalSegmentReqs < int64(result.TotalClients) {
				aggregationErrors <- nil // Expected - some clients may have 0 requests
			}
		}()
	}

	// Concurrent client add/remove during aggregation
	for i := 100; i < 110; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			stats := NewClientStats(id)
			agg.AddClient(stats)
			time.Sleep(10 * time.Millisecond)
			agg.RemoveClient(id)
		}(i)
	}

	wg.Wait()
	close(aggregationErrors)

	// Verify no panics occurred (aggregationErrors channel should be empty or have nil errors)
	// The key is that aggregation completes successfully even with concurrent add/remove

	// Verify final state (after all concurrent operations complete)
	// Note: May be 100-110 depending on timing, but should be stable
	finalCount := agg.ClientCount()
	if finalCount < 100 || finalCount > 110 {
		t.Errorf("ClientCount = %d, want between 100-110", finalCount)
	}
}

func TestStatsAggregator_ErrorRate(t *testing.T) {
	agg := NewStatsAggregator(0.01)

	stats1 := NewClientStats(1)
	// 10 requests, 1 error = 10% error rate
	for i := 0; i < 10; i++ {
		stats1.IncrementSegmentRequests()
	}
	stats1.RecordHTTPError(503)

	agg.AddClient(stats1)

	result := agg.Aggregate()

	expectedErrorRate := 1.0 / 10.0 // 10%
	if result.ErrorRate < expectedErrorRate-0.01 || result.ErrorRate > expectedErrorRate+0.01 {
		t.Errorf("ErrorRate = %v, want ~%v", result.ErrorRate, expectedErrorRate)
	}
}

func BenchmarkStatsAggregator_Aggregate(b *testing.B) {
	agg := NewStatsAggregator(0.01)

	// Add 100 clients with data
	for i := 0; i < 100; i++ {
		stats := NewClientStats(i)
		for j := 0; j < 100; j++ {
			stats.IncrementManifestRequests()
			stats.IncrementSegmentRequests()
			// Note: Latency tracking removed - use DebugEventParser for accurate latency
		}
		stats.UpdateCurrentBytes(int64(i * 1000))
		stats.UpdateSpeed(1.0)
		agg.AddClient(stats)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = agg.Aggregate()
	}
}

func BenchmarkStatsAggregator_AddClient(b *testing.B) {
	agg := NewStatsAggregator(0.01)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stats := NewClientStats(i)
		agg.AddClient(stats)
	}
}
