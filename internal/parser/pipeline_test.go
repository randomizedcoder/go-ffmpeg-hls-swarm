package parser

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// slowParser simulates a parser that can't keep up with input.
type slowParser struct {
	delay time.Duration
	lines []string
	mu    sync.Mutex
}

func (p *slowParser) ParseLine(line string) {
	time.Sleep(p.delay)
	p.mu.Lock()
	p.lines = append(p.lines, line)
	p.mu.Unlock()
}

func (p *slowParser) Lines() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]string, len(p.lines))
	copy(result, p.lines)
	return result
}

// countingParser counts lines without delay.
type countingParser struct {
	count int64
	mu    sync.Mutex
}

func (p *countingParser) ParseLine(string) {
	p.mu.Lock()
	p.count++
	p.mu.Unlock()
}

func (p *countingParser) Count() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.count
}

func TestPipeline_DropsUnderPressure(t *testing.T) {
	// Small buffer, slow parser = should drop lines
	pipeline := NewPipeline(0, "test", 5, 0.01) // Only 5 line buffer
	parser := &slowParser{delay: 10 * time.Millisecond}

	// Generate 100 lines quickly
	input := strings.Repeat("line\n", 100)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		pipeline.RunReader(strings.NewReader(input))
	}()

	go func() {
		defer wg.Done()
		pipeline.RunParser(parser)
	}()

	wg.Wait()

	read, dropped, parsed := pipeline.Stats()

	if read != 100 {
		t.Errorf("read = %d, want 100", read)
	}
	if dropped == 0 {
		t.Error("expected some lines to be dropped with slow parser and small buffer")
	}
	if parsed+dropped != read {
		t.Errorf("parsed(%d) + dropped(%d) != read(%d)", parsed, dropped, read)
	}

	t.Logf("Pipeline stats: read=%d, dropped=%d (%.1f%%), parsed=%d",
		read, dropped, float64(dropped)/float64(read)*100, parsed)
}

func TestPipeline_NoDropsWhenFast(t *testing.T) {
	// Large buffer, fast parser = no drops
	pipeline := NewPipeline(0, "test", 1000, 0.01)
	parser := &countingParser{}

	// Generate 100 lines
	input := strings.Repeat("line\n", 100)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		pipeline.RunReader(strings.NewReader(input))
	}()

	go func() {
		defer wg.Done()
		pipeline.RunParser(parser)
	}()

	wg.Wait()

	read, dropped, parsed := pipeline.Stats()

	if read != 100 {
		t.Errorf("read = %d, want 100", read)
	}
	if dropped != 0 {
		t.Errorf("dropped = %d, want 0", dropped)
	}
	if parsed != 100 {
		t.Errorf("parsed = %d, want 100", parsed)
	}
}

func TestPipeline_IsDegraded(t *testing.T) {
	tests := []struct {
		name          string
		bufferSize    int
		dropThreshold float64
		lineCount     int
		parserDelay   time.Duration
		wantDegraded  bool
	}{
		{
			name:          "no degradation with fast parser",
			bufferSize:    1000,
			dropThreshold: 0.01,
			lineCount:     100,
			parserDelay:   0,
			wantDegraded:  false,
		},
		{
			name:          "degradation with slow parser",
			bufferSize:    5,
			dropThreshold: 0.01,
			lineCount:     100,
			parserDelay:   5 * time.Millisecond,
			wantDegraded:  true,
		},
		{
			name:          "high threshold tolerates more drops",
			bufferSize:    5,
			dropThreshold: 0.99, // 99% threshold
			lineCount:     100,
			parserDelay:   5 * time.Millisecond,
			wantDegraded:  false, // Even with drops, probably won't hit 99%
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipeline := NewPipeline(0, "test", tt.bufferSize, tt.dropThreshold)
			parser := &slowParser{delay: tt.parserDelay}

			input := strings.Repeat("line\n", tt.lineCount)

			var wg sync.WaitGroup
			wg.Add(2)

			go func() {
				defer wg.Done()
				pipeline.RunReader(strings.NewReader(input))
			}()

			go func() {
				defer wg.Done()
				pipeline.RunParser(parser)
			}()

			wg.Wait()

			got := pipeline.IsDegraded()
			read, dropped, _ := pipeline.Stats()

			t.Logf("Stats: read=%d, dropped=%d (%.1f%%), threshold=%.1f%%, degraded=%v",
				read, dropped, pipeline.DropRate()*100, tt.dropThreshold*100, got)

			if got != tt.wantDegraded {
				t.Errorf("IsDegraded() = %v, want %v", got, tt.wantDegraded)
			}
		})
	}
}

func TestPipeline_DropRate(t *testing.T) {
	pipeline := NewPipeline(0, "test", 1000, 0.01)

	// No reads yet
	if rate := pipeline.DropRate(); rate != 0 {
		t.Errorf("DropRate() before any reads = %v, want 0", rate)
	}

	// Process some lines
	parser := &countingParser{}
	input := strings.Repeat("line\n", 50)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		pipeline.RunReader(strings.NewReader(input))
	}()

	go func() {
		defer wg.Done()
		pipeline.RunParser(parser)
	}()

	wg.Wait()

	// With large buffer and fast parser, should be 0
	if rate := pipeline.DropRate(); rate != 0 {
		t.Errorf("DropRate() with large buffer = %v, want 0", rate)
	}
}

func TestPipeline_ClientID(t *testing.T) {
	pipeline := NewPipeline(42, "progress", 100, 0.01)

	if got := pipeline.ClientID(); got != 42 {
		t.Errorf("ClientID() = %d, want 42", got)
	}
}

func TestPipeline_StreamType(t *testing.T) {
	tests := []struct {
		streamType string
	}{
		{"progress"},
		{"stderr"},
		{"custom"},
	}

	for _, tt := range tests {
		pipeline := NewPipeline(0, tt.streamType, 100, 0.01)
		if got := pipeline.StreamType(); got != tt.streamType {
			t.Errorf("StreamType() = %q, want %q", got, tt.streamType)
		}
	}
}

func TestPipeline_DefaultValues(t *testing.T) {
	// Test that invalid values get reasonable defaults
	pipeline := NewPipeline(0, "test", 0, 0) // Invalid buffer and threshold

	// Buffer should be at least 1
	if pipeline.bufferSize < 1 {
		t.Errorf("bufferSize = %d, want >= 1", pipeline.bufferSize)
	}

	// Threshold should be positive
	if pipeline.dropThreshold <= 0 {
		t.Errorf("dropThreshold = %v, want > 0", pipeline.dropThreshold)
	}
}

func TestNoopParser(t *testing.T) {
	// Just ensure NoopParser compiles and doesn't panic
	var parser NoopParser
	parser.ParseLine("test line")
	parser.ParseLine("")
}

func BenchmarkPipeline_FastParser(b *testing.B) {
	for i := 0; i < b.N; i++ {
		pipeline := NewPipeline(0, "bench", 1000, 0.01)
		parser := &countingParser{}

		input := strings.Repeat("benchmark line with some content\n", 1000)

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			pipeline.RunReader(strings.NewReader(input))
		}()

		go func() {
			defer wg.Done()
			pipeline.RunParser(parser)
		}()

		wg.Wait()
	}
}
