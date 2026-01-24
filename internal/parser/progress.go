// Package parser provides parsing for FFmpeg output streams.
//
// This file implements the ProgressParser which parses FFmpeg's -progress pipe:1
// output format. The output is a series of key=value pairs, with each block
// terminated by a "progress=continue" or "progress=end" line.
//
// Tested FFmpeg Version:
//
//	ffmpeg version 8.0 Copyright (c) 2000-2025 the FFmpeg developers
//	built with gcc 15.2.0 (GCC)
//	libavutil      60.  8.100
//	libavcodec     62. 11.100
//	libavformat    62.  3.100
//
// If parsing breaks after an FFmpeg upgrade, the -progress output format may
// have changed. See testdata/ffmpeg_progress.txt for expected format.
//
// Example FFmpeg progress output:
//
//	frame=60
//	fps=30.00
//	bitrate=512.0kbits/s
//	total_size=51324
//	out_time_us=2000000
//	speed=1.00x
//	progress=continue
package parser

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// ProgressUpdate represents a single progress report from FFmpeg.
//
// FFmpeg emits these updates periodically (controlled by -stats_period).
// Each update contains cumulative values since the process started.
type ProgressUpdate struct {
	// Frame count (cumulative)
	Frame int64

	// Frames per second (current)
	FPS float64

	// Current bitrate as string (e.g., "512.0kbits/s", "N/A")
	Bitrate string

	// Total bytes downloaded (cumulative) - CRITICAL for throughput
	// Note: This resets to 0 when FFmpeg restarts!
	TotalSize int64

	// Playback position in microseconds (cumulative)
	// Used for wall-clock drift calculation
	OutTimeUS int64

	// Playback speed relative to realtime (1.0 = realtime)
	// < 1.0 indicates stalling/buffering
	// > 1.0 indicates catching up or fast download
	Speed float64

	// Progress status: "continue" or "end"
	Progress string

	// Timestamp when this update was received (for rate calculations)
	ReceivedAt time.Time
}

// ProgressCallback is called for each complete progress update.
// The callback receives a copy of the update, so it's safe to store.
type ProgressCallback func(*ProgressUpdate)

// ProgressParser parses FFmpeg -progress pipe:1 output.
//
// It implements the LineParser interface for use with Pipeline.
// Thread-safe: can be called from multiple goroutines.
type ProgressParser struct {
	callback ProgressCallback

	mu      sync.Mutex
	current *ProgressUpdate

	// Stats for monitoring parser health
	blocksReceived int64
	linesProcessed int64
}

// NewProgressParser creates a new progress parser with the given callback.
//
// The callback is invoked for each complete progress block (when "progress="
// line is received). Pass nil for callback if you only want to count blocks.
func NewProgressParser(cb ProgressCallback) *ProgressParser {
	return &ProgressParser{
		callback: cb,
		current:  &ProgressUpdate{},
	}
}

// ParseLine implements the LineParser interface.
//
// This is the primary entry point when used with Pipeline.
// Each line is parsed and accumulated until a "progress=" line completes the block.
func (p *ProgressParser) ParseLine(line string) {
	p.parseLine(line)
}

// parseLine handles a single line of progress output.
func (p *ProgressParser) parseLine(line string) {
	key, value, ok := parseKeyValue(line)
	if !ok {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.linesProcessed++

	switch key {
	case "frame":
		p.current.Frame, _ = strconv.ParseInt(value, 10, 64)

	case "fps":
		p.current.FPS, _ = strconv.ParseFloat(value, 64)

	case "bitrate":
		p.current.Bitrate = value

	case "total_size":
		// For live HLS streams, FFmpeg reports "N/A" instead of a number.
		// We'll track bytes from HTTP Content-Length headers instead.
		if value != "N/A" && value != "" {
			p.current.TotalSize, _ = strconv.ParseInt(value, 10, 64)
		}
		// If value is "N/A" or empty, TotalSize remains 0 (will be tracked via HTTP headers)

	case "out_time_us":
		p.current.OutTimeUS, _ = strconv.ParseInt(value, 10, 64)

	case "speed":
		p.current.Speed = parseSpeed(value)

	case "progress":
		p.current.Progress = value
		p.current.ReceivedAt = time.Now()

		// End of block - emit update
		p.blocksReceived++
		if p.callback != nil {
			update := *p.current // copy
			p.callback(&update)
		}

		// Reset for next block
		p.current = &ProgressUpdate{}
	}
}

// Stats returns parser statistics.
func (p *ProgressParser) Stats() (blocksReceived, linesProcessed int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.blocksReceived, p.linesProcessed
}

// Current returns the current (incomplete) progress update.
// Useful for getting partial data if the stream ends mid-block.
func (p *ProgressParser) Current() *ProgressUpdate {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.current == nil {
		return nil
	}
	copy := *p.current
	return &copy
}

// parseKeyValue splits "key=value" into parts.
//
// Returns empty strings and false if the line doesn't contain '='.
func parseKeyValue(line string) (key, value string, ok bool) {
	idx := strings.Index(line, "=")
	if idx < 0 {
		return "", "", false
	}
	return line[:idx], line[idx+1:], true
}

// parseSpeed converts FFmpeg speed string to float64.
//
// Examples:
//   - "1.00x" -> 1.0
//   - "0.95x" -> 0.95
//   - "N/A"   -> 0.0
//   - ""      -> 0.0
func parseSpeed(s string) float64 {
	s = strings.TrimSuffix(s, "x")
	if s == "N/A" || s == "" {
		return 0
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// OutTimeDuration returns the playback position as a time.Duration.
func (u *ProgressUpdate) OutTimeDuration() time.Duration {
	return time.Duration(u.OutTimeUS) * time.Microsecond
}

// IsStalling returns true if the playback speed indicates stalling.
//
// A speed below 0.9 for an extended period typically indicates buffering.
func (u *ProgressUpdate) IsStalling() bool {
	// Speed of 0 means N/A (startup), not stalling
	if u.Speed == 0 {
		return false
	}
	return u.Speed < 0.9
}

// IsEnd returns true if this is the final progress update.
func (u *ProgressUpdate) IsEnd() bool {
	return u.Progress == "end"
}
