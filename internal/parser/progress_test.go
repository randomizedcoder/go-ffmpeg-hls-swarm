package parser

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseKeyValue(t *testing.T) {
	tests := []struct {
		input   string
		wantKey string
		wantVal string
		wantOK  bool
	}{
		{"frame=100", "frame", "100", true},
		{"speed=1.00x", "speed", "1.00x", true},
		{"bitrate=N/A", "bitrate", "N/A", true},
		{"bitrate=512.0kbits/s", "bitrate", "512.0kbits/s", true},
		{"out_time=00:00:02.000000", "out_time", "00:00:02.000000", true},
		{"invalid", "", "", false},
		{"", "", "", false},
		{"=empty_key", "", "empty_key", true},
		{"key=", "key", "", true},
		{"key=value=with=equals", "key", "value=with=equals", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			key, val, ok := parseKeyValue(tt.input)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if key != tt.wantKey {
				t.Errorf("key = %q, want %q", key, tt.wantKey)
			}
			if val != tt.wantVal {
				t.Errorf("val = %q, want %q", val, tt.wantVal)
			}
		})
	}
}

func TestParseSpeed(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"1.00x", 1.0},
		{"0.95x", 0.95},
		{"1.5x", 1.5},
		{"2.00x", 2.0},
		{"0.5x", 0.5},
		{"10.0x", 10.0},
		{"N/A", 0},
		{"", 0},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseSpeed(tt.input)
			if got != tt.want {
				t.Errorf("parseSpeed(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestProgressParser_ParseBlock(t *testing.T) {
	input := `frame=0
fps=0.00
bitrate=N/A
total_size=0
out_time_us=0
speed=N/A
progress=continue
frame=60
fps=30.00
bitrate=512.0kbits/s
total_size=51324
out_time_us=2000000
speed=1.00x
progress=continue
`

	var updates []*ProgressUpdate
	var mu sync.Mutex

	p := NewProgressParser(func(u *ProgressUpdate) {
		mu.Lock()
		updates = append(updates, u)
		mu.Unlock()
	})

	// Parse line by line (as Pipeline would)
	for _, line := range strings.Split(input, "\n") {
		if line != "" {
			p.ParseLine(line)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	if len(updates) != 2 {
		t.Fatalf("got %d updates, want 2", len(updates))
	}

	// First update (startup - all zeros/N/A)
	if updates[0].Frame != 0 {
		t.Errorf("update[0].Frame = %d, want 0", updates[0].Frame)
	}
	if updates[0].Speed != 0 {
		t.Errorf("update[0].Speed = %v, want 0 (N/A)", updates[0].Speed)
	}
	if updates[0].TotalSize != 0 {
		t.Errorf("update[0].TotalSize = %d, want 0", updates[0].TotalSize)
	}
	if updates[0].Progress != "continue" {
		t.Errorf("update[0].Progress = %q, want 'continue'", updates[0].Progress)
	}

	// Second update (real data)
	if updates[1].Frame != 60 {
		t.Errorf("update[1].Frame = %d, want 60", updates[1].Frame)
	}
	if updates[1].FPS != 30.0 {
		t.Errorf("update[1].FPS = %v, want 30.0", updates[1].FPS)
	}
	if updates[1].Bitrate != "512.0kbits/s" {
		t.Errorf("update[1].Bitrate = %q, want '512.0kbits/s'", updates[1].Bitrate)
	}
	if updates[1].TotalSize != 51324 {
		t.Errorf("update[1].TotalSize = %d, want 51324", updates[1].TotalSize)
	}
	if updates[1].OutTimeUS != 2000000 {
		t.Errorf("update[1].OutTimeUS = %d, want 2000000", updates[1].OutTimeUS)
	}
	if updates[1].Speed != 1.0 {
		t.Errorf("update[1].Speed = %v, want 1.0", updates[1].Speed)
	}
	if updates[1].ReceivedAt.IsZero() {
		t.Error("update[1].ReceivedAt should not be zero")
	}
}

func TestProgressParser_NoCallback(t *testing.T) {
	// Should not panic with nil callback
	p := NewProgressParser(nil)
	input := "frame=0\nprogress=continue\n"

	for _, line := range strings.Split(input, "\n") {
		if line != "" {
			p.ParseLine(line)
		}
	}

	blocks, lines := p.Stats()
	if blocks != 1 {
		t.Errorf("blocks = %d, want 1", blocks)
	}
	if lines != 2 {
		t.Errorf("lines = %d, want 2", lines)
	}
}

func TestProgressParser_Stats(t *testing.T) {
	p := NewProgressParser(nil)

	// Parse 3 complete blocks
	input := `frame=0
progress=continue
frame=30
progress=continue
frame=60
progress=end
`

	for _, line := range strings.Split(input, "\n") {
		if line != "" {
			p.ParseLine(line)
		}
	}

	blocks, lines := p.Stats()
	if blocks != 3 {
		t.Errorf("blocks = %d, want 3", blocks)
	}
	if lines != 6 {
		t.Errorf("lines = %d, want 6", lines)
	}
}

func TestProgressParser_Current(t *testing.T) {
	p := NewProgressParser(nil)

	// Parse partial block
	p.ParseLine("frame=100")
	p.ParseLine("fps=30.00")
	p.ParseLine("total_size=12345")

	current := p.Current()
	if current == nil {
		t.Fatal("Current() returned nil")
	}
	if current.Frame != 100 {
		t.Errorf("current.Frame = %d, want 100", current.Frame)
	}
	if current.FPS != 30.0 {
		t.Errorf("current.FPS = %v, want 30.0", current.FPS)
	}
	if current.TotalSize != 12345 {
		t.Errorf("current.TotalSize = %d, want 12345", current.TotalSize)
	}
}

func TestProgressUpdate_OutTimeDuration(t *testing.T) {
	tests := []struct {
		outTimeUS int64
		want      time.Duration
	}{
		{0, 0},
		{1000000, time.Second},
		{2500000, 2500 * time.Millisecond},
		{60000000, time.Minute},
	}

	for _, tt := range tests {
		u := &ProgressUpdate{OutTimeUS: tt.outTimeUS}
		got := u.OutTimeDuration()
		if got != tt.want {
			t.Errorf("OutTimeDuration(%d) = %v, want %v", tt.outTimeUS, got, tt.want)
		}
	}
}

func TestProgressUpdate_IsStalling(t *testing.T) {
	tests := []struct {
		speed float64
		want  bool
	}{
		{0, false},     // N/A - not stalling
		{0.5, true},    // Definitely stalling
		{0.89, true},   // Just below threshold
		{0.9, false},   // At threshold - not stalling
		{0.91, false},  // Just above threshold
		{1.0, false},   // Realtime
		{1.5, false},   // Catching up
		{2.0, false},   // Fast
	}

	for _, tt := range tests {
		u := &ProgressUpdate{Speed: tt.speed}
		got := u.IsStalling()
		if got != tt.want {
			t.Errorf("IsStalling(speed=%v) = %v, want %v", tt.speed, got, tt.want)
		}
	}
}

func TestProgressUpdate_IsEnd(t *testing.T) {
	tests := []struct {
		progress string
		want     bool
	}{
		{"continue", false},
		{"end", true},
		{"", false},
	}

	for _, tt := range tests {
		u := &ProgressUpdate{Progress: tt.progress}
		got := u.IsEnd()
		if got != tt.want {
			t.Errorf("IsEnd(progress=%q) = %v, want %v", tt.progress, got, tt.want)
		}
	}
}

func TestProgressParser_ThreadSafety(t *testing.T) {
	var updates []*ProgressUpdate
	var mu sync.Mutex

	p := NewProgressParser(func(u *ProgressUpdate) {
		mu.Lock()
		updates = append(updates, u)
		mu.Unlock()
	})

	// Simulate concurrent parsing from multiple goroutines
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				p.ParseLine("frame=100")
				p.ParseLine("progress=continue")
			}
		}(i)
	}

	wg.Wait()

	mu.Lock()
	count := len(updates)
	mu.Unlock()

	// 10 goroutines * 100 iterations = 1000 blocks
	if count != 1000 {
		t.Errorf("got %d updates, want 1000", count)
	}
}

func TestProgressParser_RealWorldOutput(t *testing.T) {
	// Real FFmpeg output includes additional fields we should ignore gracefully
	input := `frame=0
fps=0.00
stream_0_0_q=-1.0
bitrate=N/A
total_size=0
out_time_us=0
out_time_ms=0
out_time=00:00:00.000000
dup_frames=0
drop_frames=0
speed=N/A
progress=continue
frame=30
fps=30.00
stream_0_0_q=-1.0
bitrate=512.0kbits/s
total_size=51324
out_time_us=2000000
out_time_ms=2000
out_time=00:00:02.000000
dup_frames=0
drop_frames=0
speed=1.00x
progress=continue
`

	var updates []*ProgressUpdate
	p := NewProgressParser(func(u *ProgressUpdate) {
		updates = append(updates, u)
	})

	for _, line := range strings.Split(input, "\n") {
		if line != "" {
			p.ParseLine(line)
		}
	}

	if len(updates) != 2 {
		t.Fatalf("got %d updates, want 2", len(updates))
	}

	// Verify we parsed the fields we care about
	if updates[1].Frame != 30 {
		t.Errorf("Frame = %d, want 30", updates[1].Frame)
	}
	if updates[1].TotalSize != 51324 {
		t.Errorf("TotalSize = %d, want 51324", updates[1].TotalSize)
	}
	if updates[1].OutTimeUS != 2000000 {
		t.Errorf("OutTimeUS = %d, want 2000000", updates[1].OutTimeUS)
	}
	if updates[1].Speed != 1.0 {
		t.Errorf("Speed = %v, want 1.0", updates[1].Speed)
	}
}

func BenchmarkProgressParser_ParseLine(b *testing.B) {
	p := NewProgressParser(func(u *ProgressUpdate) {})

	lines := []string{
		"frame=1000",
		"fps=30.00",
		"bitrate=512.0kbits/s",
		"total_size=1234567",
		"out_time_us=33333333",
		"speed=1.00x",
		"progress=continue",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, line := range lines {
			p.ParseLine(line)
		}
	}
}
