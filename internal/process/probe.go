package process

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
)

// ProbeResult represents the output of ffprobe -show_programs.
type ProbeResult struct {
	Programs []Program `json:"programs"`
}

// Program represents an HLS program/variant.
type Program struct {
	ProgramID  int  `json:"program_id"`
	ProgramNum int  `json:"program_num"`
	NumStreams int  `json:"nb_streams"`
	Tags       Tags `json:"tags"`
}

// Tags contains metadata tags for a program.
type Tags struct {
	VariantBitrate string `json:"variant_bitrate"`
}

// ProgramInfo holds parsed information about a program.
type ProgramInfo struct {
	ProgramID int
	Bitrate   int64
}

// ProbeVariants uses ffprobe to discover available variants and select
// the appropriate one based on the Variant setting.
// Sets ProgramID in the config if successful.
func (r *FFmpegRunner) ProbeVariants(ctx context.Context) error {
	// Only probe for highest/lowest variants
	if r.config.Variant != VariantHighest && r.config.Variant != VariantLowest {
		return nil
	}

	programs, err := r.probe(ctx)
	if err != nil {
		return err
	}

	if len(programs) == 0 {
		return fmt.Errorf("no programs found in stream")
	}

	// Sort by bitrate
	sort.Slice(programs, func(i, j int) bool {
		return programs[i].Bitrate < programs[j].Bitrate
	})

	// Select program based on variant
	var selected ProgramInfo
	switch r.config.Variant {
	case VariantHighest:
		selected = programs[len(programs)-1]
	case VariantLowest:
		selected = programs[0]
	}

	r.config.ProgramID = selected.ProgramID
	return nil
}

// probe executes ffprobe and returns program information.
func (r *FFmpegRunner) probe(ctx context.Context) ([]ProgramInfo, error) {
	// Find ffprobe binary (usually alongside ffmpeg)
	ffprobePath := r.findFFprobe()

	// Build command
	args := []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_programs",
	}

	// Add user agent if configured
	if r.config.UserAgent != "" {
		args = append(args, "-user_agent", r.config.UserAgent)
	}

	// Handle URL rewriting for IP override
	inputURL := r.effectiveURL()
	args = append(args, inputURL)

	cmd := exec.CommandContext(ctx, ffprobePath, args...)

	// Execute
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	// Parse JSON output
	var result ProbeResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("failed to parse ffprobe output: %w", err)
	}

	// Convert to ProgramInfo slice
	programs := make([]ProgramInfo, 0, len(result.Programs))
	for _, p := range result.Programs {
		bitrate, _ := strconv.ParseInt(p.Tags.VariantBitrate, 10, 64)
		programs = append(programs, ProgramInfo{
			ProgramID: p.ProgramID,
			Bitrate:   bitrate,
		})
	}

	return programs, nil
}

// findFFprobe returns the path to ffprobe.
// It looks in the same directory as ffmpeg, or falls back to PATH.
func (r *FFmpegRunner) findFFprobe() string {
	// If ffmpeg path is explicit and ends with "ffmpeg", try ffprobe in same directory
	const ffmpegSuffix = "ffmpeg"
	if len(r.config.BinaryPath) > len(ffmpegSuffix) &&
		r.config.BinaryPath[len(r.config.BinaryPath)-len(ffmpegSuffix):] == ffmpegSuffix {
		// Replace ffmpeg with ffprobe in path
		// e.g., /usr/local/bin/ffmpeg -> /usr/local/bin/ffprobe
		dir := r.config.BinaryPath[:len(r.config.BinaryPath)-len(ffmpegSuffix)]
		ffprobePath := dir + "ffprobe"
		if _, err := exec.LookPath(ffprobePath); err == nil {
			return ffprobePath
		}
	}

	// Fall back to ffprobe in PATH
	return "ffprobe"
}

// ProbeAvailable checks if ffprobe is available.
func ProbeAvailable() bool {
	_, err := exec.LookPath("ffprobe")
	return err == nil
}

// GetVariantBitrates returns the bitrates of all variants in the stream.
// Useful for logging/debugging.
func (r *FFmpegRunner) GetVariantBitrates(ctx context.Context) ([]int64, error) {
	programs, err := r.probe(ctx)
	if err != nil {
		return nil, err
	}

	bitrates := make([]int64, len(programs))
	for i, p := range programs {
		bitrates[i] = p.Bitrate
	}

	// Sort descending
	sort.Slice(bitrates, func(i, j int) bool {
		return bitrates[i] > bitrates[j]
	})

	return bitrates, nil
}
