package parser

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// benchCountingParser is a simple parser that counts lines (for benchmarks only).
type benchCountingParser struct {
	count *int64
}

func (p *benchCountingParser) ParseLine(line string) {
	atomic.AddInt64(p.count, 1)
}

// =============================================================================
// Pipeline Throughput Benchmarks
// =============================================================================

// BenchmarkPipeline_Feed measures pipeline feed throughput.
func BenchmarkPipeline_Feed(b *testing.B) {
	pipeline := NewPipeline(1, "progress", 4096, 0.01)

	// Run fast noop parser
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		pipeline.RunParser(&benchNoopParser{})
	}()

	lines := []string{
		"frame=100",
		"fps=30.00",
		"bitrate=N/A",
		"out_time_us=1000000",
		"speed=1.00x",
		"progress=continue",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, line := range lines {
			pipeline.FeedLine(line)
		}
	}
	b.StopTimer()

	pipeline.CloseChannel()
	wg.Wait()
}

// benchNoopParser is a no-op parser for benchmarks.
type benchNoopParser struct{}

func (p *benchNoopParser) ParseLine(line string) {}

// BenchmarkPipeline_HighContention benchmarks pipeline under high contention.
func BenchmarkPipeline_HighContention(b *testing.B) {
	pipeline := NewPipeline(1, "progress", 256, 0.01) // Small buffer to stress it

	// Run fast parser
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		pipeline.RunParser(&benchNoopParser{})
	}()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			pipeline.FeedLine("frame=100")
		}
	})
	b.StopTimer()

	pipeline.CloseChannel()
	wg.Wait()
}

// =============================================================================
// Parser Combined Benchmarks
// =============================================================================

// BenchmarkAllParsers_MixedInput benchmarks all parsers with mixed input.
func BenchmarkAllParsers_MixedInput(b *testing.B) {
	// Representative mix of real FFmpeg output
	lines := []string{
		// Progress lines
		"frame=100",
		"fps=30.00",
		"stream_0_0_q=-1.0",
		"bitrate=N/A",
		"total_size=N/A",
		"out_time_us=3333333",
		"out_time=00:00:03.333333",
		"speed=1.00x",
		"progress=continue",
		// Debug lines (should fast-path)
		"[h264 @ 0x55f8] nal_unit_type: 9(AUD), nal_ref_idc: 0",
		"[mpegts @ 0x55f8] stream=0 stream_type=1b pid=100",
		// HLS lines
		"[hls @ 0x55f8] HLS request for url 'http://origin/seg00001.ts', offset 0, playlist 0",
		"[tcp @ 0x55f8] Successfully connected to 10.0.0.1 port 80",
	}

	b.Run("ProgressParser", func(b *testing.B) {
		p := NewProgressParser(nil)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for _, line := range lines {
				p.ParseLine(line)
			}
		}
	})

	b.Run("HLSEventParser", func(b *testing.B) {
		p := NewHLSEventParser(1, nil)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for _, line := range lines {
				p.ParseLine(line)
			}
		}
	})

	b.Run("DebugEventParser", func(b *testing.B) {
		p := NewDebugEventParser(1, 2*time.Second, nil)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for _, line := range lines {
				p.ParseLine(line)
			}
		}
	})
}

// =============================================================================
// Memory Allocation Benchmarks
// =============================================================================

// BenchmarkDebugEventParser_Allocs measures memory allocations per line.
func BenchmarkDebugEventParser_Allocs(b *testing.B) {
	p := NewDebugEventParser(1, 2*time.Second, nil)

	b.Run("FastPath", func(b *testing.B) {
		line := "frame=100 fps=30 q=-1.0 size=N/A time=00:00:03.33"
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			p.ParseLine(line)
		}
	})

	b.Run("HLSRequest", func(b *testing.B) {
		line := "[hls @ 0x55f8] HLS request for url 'http://origin/seg00001.ts', offset 0, playlist 0"
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			p.ParseLine(line)
		}
	})

	b.Run("TCPConnected", func(b *testing.B) {
		line := "[tcp @ 0x55f8] Successfully connected to 10.0.0.1 port 80"
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			p.ParseLine(line)
		}
	})
}

// BenchmarkProgressParser_Allocs measures memory allocations.
func BenchmarkProgressParser_Allocs(b *testing.B) {
	p := NewProgressParser(nil)
	lines := []string{
		"frame=100",
		"fps=30.00",
		"progress=continue",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, line := range lines {
			p.ParseLine(line)
		}
	}
}

// =============================================================================
// Scaling Benchmarks
// =============================================================================

// BenchmarkPipeline_MultiClient simulates multiple clients feeding pipelines.
func BenchmarkPipeline_MultiClient(b *testing.B) {
	for _, numClients := range []int{1, 10, 100} {
		name := ""
		switch numClients {
		case 1:
			name = "1client"
		case 10:
			name = "10clients"
		case 100:
			name = "100clients"
		}

		b.Run(name, func(b *testing.B) {
			pipelines := make([]*Pipeline, numClients)
			for i := 0; i < numClients; i++ {
				pipelines[i] = NewPipeline(i, "progress", 256, 0.01)
			}

			var wg sync.WaitGroup
			for _, p := range pipelines {
				p := p
				wg.Add(1)
				go func() {
					defer wg.Done()
					p.RunParser(&benchNoopParser{})
				}()
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				pipelines[i%numClients].FeedLine("frame=100")
			}
			b.StopTimer()

			for _, p := range pipelines {
				p.CloseChannel()
			}
			wg.Wait()
		})
	}
}


// =============================================================================
// Regex Benchmark
// =============================================================================

// BenchmarkRegex_Matching benchmarks regex matching performance.
func BenchmarkRegex_Matching(b *testing.B) {
	lines := map[string]string{
		"HLSRequest":    "[hls @ 0x55f8] HLS request for url 'http://origin/seg.ts', offset 0, playlist 0",
		"TCPStart":      "[tcp @ 0x55f8] Starting connection attempt to 10.0.0.1 port 80",
		"TCPConnected":  "[tcp @ 0x55f8] Successfully connected to 10.0.0.1 port 80",
		"TCPFailed":     "[tcp @ 0x55f8] Connection refused",
		"PlaylistOpen":  "[hls @ 0x55f8] Opening 'http://origin/stream.m3u8' for reading",
		"SequenceChg":   "[hls @ 0x55f8] Media sequence change (100 -> 105) reflected",
		"Bandwidth":     "#EXT-X-STREAM-INF:BANDWIDTH=2000000",
		"NonMatching":   "frame=100 fps=30 q=-1.0 size=N/A time=00:00:03.33",
	}

	for name, line := range lines {
		b.Run(name, func(b *testing.B) {
			p := NewDebugEventParser(1, 2*time.Second, nil)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				p.ParseLine(line)
			}
		})
	}
}

// BenchmarkRegex_FastPath specifically tests fast-path performance.
func BenchmarkRegex_FastPath(b *testing.B) {
	// Lines that should skip all regex checks
	lines := []string{
		"frame=100",
		"fps=30.00",
		"bitrate=N/A",
		"out_time_us=1000000",
		"speed=1.00x",
		"progress=continue",
		"[h264 @ 0x55f8] nal_unit_type: 9(AUD)",
		"[mpegts @ 0x55f8] stream=0 stream_type=1b",
	}

	p := NewDebugEventParser(1, 2*time.Second, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, line := range lines {
			p.ParseLine(line)
		}
	}
	b.ReportMetric(float64(len(lines)), "lines/op")
}
