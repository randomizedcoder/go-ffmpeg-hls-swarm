# Client Deployment Implementation Plan

> **Status**: Implementation Ready
> **Related**: [CLIENT_DEPLOYMENT.md](CLIENT_DEPLOYMENT.md), [DESIGN.md](DESIGN.md), [SUPERVISION.md](SUPERVISION.md)

This document provides the step-by-step implementation plan for `go-ffmpeg-hls-swarm`, designed to integrate seamlessly with the OCI container and MicroVM deployments defined in [CLIENT_DEPLOYMENT.md](CLIENT_DEPLOYMENT.md).

---

## Table of Contents

- [Overview](#overview)
- [Success Criteria](#success-criteria)
- [Package Structure](#package-structure)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Foundation](#phase-1-foundation)
  - [Phase 2: Core Orchestration](#phase-2-core-orchestration)
  - [Phase 3: FFmpeg Integration](#phase-3-ffmpeg-integration)
  - [Phase 4: Observability](#phase-4-observability)
  - [Phase 5: CLI & Container Integration](#phase-5-cli--container-integration)
  - [Phase 6: Polish & Testing](#phase-6-polish--testing)
- [Detailed Component Specifications](#detailed-component-specifications)
- [CLI Interface Specification](#cli-interface-specification)
- [Testing Strategy](#testing-strategy)
- [Milestone Checklist](#milestone-checklist)

---

## Overview

### Goal

Implement the `go-ffmpeg-hls-swarm` Go binary that:

1. Orchestrates 50-200+ concurrent FFmpeg processes
2. Exposes Prometheus metrics at `/metrics`
3. Handles graceful shutdown with signal propagation
4. Integrates with OCI containers and MicroVMs via environment variables

### Key Constraints

| Constraint | Requirement |
|------------|-------------|
| **Container compatibility** | Must accept config via environment variables (see `container.nix`) |
| **Metrics format** | Prometheus metrics matching [OBSERVABILITY.md](OBSERVABILITY.md) |
| **CLI flags** | Must match [CONFIGURATION.md](CONFIGURATION.md) exactly |
| **Exit behavior** | Clean shutdown with summary report |

---

## Success Criteria

### Minimum Viable Product (MVP)

- [ ] Start N FFmpeg processes with controlled ramp-up
- [ ] Restart failed processes with exponential backoff
- [ ] Expose `hlsswarm_clients_active` and `hlsswarm_clients_target` metrics
- [ ] Handle SIGTERM/SIGINT with graceful shutdown
- [ ] Print exit summary on shutdown

### Container-Ready

- [ ] All config via CLI flags (environment variables handled by entrypoint)
- [ ] Non-zero exit on fatal errors
- [ ] Structured JSON logging to stderr
- [ ] Works with `docker run -e STREAM_URL=... -e CLIENTS=50`

---

## Package Structure

```
go-ffmpeg-hls-swarm/
├── cmd/
│   └── go-ffmpeg-hls-swarm/
│       └── main.go                 # CLI entry point
├── internal/
│   ├── config/
│   │   ├── config.go               # Configuration struct
│   │   ├── flags.go                # CLI flag parsing
│   │   └── validate.go             # Config validation
│   ├── orchestrator/
│   │   ├── orchestrator.go         # Main orchestrator
│   │   ├── client_manager.go       # Manages all supervisors
│   │   └── ramp_scheduler.go       # Controlled ramp-up
│   ├── supervisor/
│   │   ├── supervisor.go           # Single client lifecycle
│   │   ├── backoff.go              # Exponential backoff
│   │   ├── state.go                # Client state machine
│   │   └── jitter.go               # Per-client deterministic jitter
│   ├── process/
│   │   ├── runner.go               # ProcessRunner interface
│   │   ├── ffmpeg.go               # FFmpeg implementation
│   │   ├── command.go              # Command building utilities
│   │   └── output.go               # Stderr handling
│   ├── metrics/
│   │   ├── collector.go            # Prometheus metrics
│   │   ├── server.go               # HTTP server for /metrics
│   │   └── summary.go              # Exit summary generation
│   ├── preflight/
│   │   ├── checks.go               # Preflight check runner
│   │   ├── ulimit.go               # File descriptor checks
│   │   └── ffmpeg.go               # FFmpeg binary detection
│   └── logging/
│       ├── logger.go               # Structured logging setup
│       └── handler.go              # Stderr line handler
├── go.mod
├── go.sum
└── Makefile
```

---

## Implementation Phases

### Phase 1: Foundation

**Goal**: Project skeleton, config parsing, basic logging

**Duration**: 1-2 days

#### 1.1 Project Setup

```bash
# Initialize module
go mod init github.com/randomizedcoder/go-ffmpeg-hls-swarm

# Add dependencies
go get github.com/prometheus/client_golang/prometheus
go get github.com/prometheus/client_golang/prometheus/promhttp
```

#### 1.2 Config Package (`internal/config/`)

```go
// config/config.go

package config

import "time"

type Config struct {
    // Orchestration
    Clients      int           `json:"clients"`
    RampRate     int           `json:"ramp_rate"`
    RampJitter   time.Duration `json:"ramp_jitter"`
    Duration     time.Duration `json:"duration"` // 0 = forever

    // FFmpeg
    FFmpegPath        string        `json:"ffmpeg_path"`
    StreamURL         string        `json:"stream_url"`
    Variant           string        `json:"variant"` // all, highest, lowest, first
    UserAgent         string        `json:"user_agent"`
    Timeout           time.Duration `json:"timeout"`
    Reconnect         bool          `json:"reconnect"`
    ReconnectDelayMax int           `json:"reconnect_delay_max"`
    SegMaxRetry       int           `json:"seg_max_retry"`

    // Network
    ResolveIP     string   `json:"resolve_ip"`
    DangerousMode bool     `json:"dangerous_mode"`
    NoCache       bool     `json:"no_cache"`
    Headers       []string `json:"headers"`

    // Observability
    MetricsAddr string `json:"metrics_addr"`
    LogLevel    string `json:"log_level"`
    LogFormat   string `json:"log_format"` // json, text
    Verbose     bool   `json:"verbose"`

    // Diagnostic
    PrintCmd      bool `json:"print_cmd"`
    Check         bool `json:"check"`
    SkipPreflight bool `json:"skip_preflight"`
}

func DefaultConfig() *Config {
    return &Config{
        Clients:           10,
        RampRate:          5,
        RampJitter:        200 * time.Millisecond,
        Duration:          0,
        FFmpegPath:        "ffmpeg",
        Variant:           "all",
        UserAgent:         "go-ffmpeg-hls-swarm/1.0",
        Timeout:           15 * time.Second,
        Reconnect:         true,
        ReconnectDelayMax: 5,
        SegMaxRetry:       3,
        MetricsAddr:       "0.0.0.0:9090",
        LogLevel:          "info",
        LogFormat:         "json",
    }
}
```

#### 1.3 CLI Flag Parsing (`internal/config/flags.go`)

```go
// config/flags.go

package config

import (
    "flag"
    "fmt"
    "os"
    "time"
)

func ParseFlags() (*Config, error) {
    cfg := DefaultConfig()

    // Orchestration
    flag.IntVar(&cfg.Clients, "clients", cfg.Clients, "Number of concurrent clients")
    flag.IntVar(&cfg.RampRate, "ramp-rate", cfg.RampRate, "Clients to start per second")
    flag.DurationVar(&cfg.RampJitter, "ramp-jitter", cfg.RampJitter, "Random jitter per client start")
    flag.DurationVar(&cfg.Duration, "duration", cfg.Duration, "Run duration (0 = forever)")

    // Variant selection
    flag.StringVar(&cfg.Variant, "variant", cfg.Variant, "Bitrate selection: all, highest, lowest, first")

    // Network
    flag.StringVar(&cfg.ResolveIP, "resolve", cfg.ResolveIP, "Connect to this IP (requires --dangerous)")
    flag.BoolVar(&cfg.NoCache, "no-cache", cfg.NoCache, "Add no-cache headers")
    // -header handled separately (repeatable)

    // Safety flags (double-dash convention)
    flag.BoolVar(&cfg.DangerousMode, "dangerous", cfg.DangerousMode, "Required for -resolve")
    flag.BoolVar(&cfg.PrintCmd, "print-cmd", cfg.PrintCmd, "Print FFmpeg command and exit")
    flag.BoolVar(&cfg.Check, "check", cfg.Check, "Run 1 client for 10s and exit")
    flag.BoolVar(&cfg.SkipPreflight, "skip-preflight", cfg.SkipPreflight, "Skip preflight checks")

    // Observability
    flag.StringVar(&cfg.MetricsAddr, "metrics", cfg.MetricsAddr, "Prometheus metrics address")
    flag.BoolVar(&cfg.Verbose, "v", cfg.Verbose, "Verbose logging")

    // FFmpeg
    flag.StringVar(&cfg.FFmpegPath, "ffmpeg", cfg.FFmpegPath, "Path to FFmpeg binary")
    flag.StringVar(&cfg.UserAgent, "user-agent", cfg.UserAgent, "HTTP User-Agent")
    flag.DurationVar(&cfg.Timeout, "timeout", cfg.Timeout, "Network timeout")
    flag.BoolVar(&cfg.Reconnect, "reconnect", cfg.Reconnect, "Enable reconnection")
    flag.IntVar(&cfg.ReconnectDelayMax, "reconnect-delay", cfg.ReconnectDelayMax, "Max reconnect delay (seconds)")
    flag.IntVar(&cfg.SegMaxRetry, "seg-retry", cfg.SegMaxRetry, "Segment retry count")

    flag.Parse()

    // Positional argument: stream URL
    args := flag.Args()
    if len(args) < 1 && !cfg.PrintCmd {
        return nil, fmt.Errorf("stream URL required")
    }
    if len(args) > 0 {
        cfg.StreamURL = args[0]
    }

    return cfg, nil
}
```

#### 1.4 Logging Package (`internal/logging/`)

```go
// logging/logger.go

package logging

import (
    "log/slog"
    "os"
)

func NewLogger(format, level string) *slog.Logger {
    var handler slog.Handler

    opts := &slog.HandlerOptions{
        Level: parseLevel(level),
    }

    switch format {
    case "json":
        handler = slog.NewJSONHandler(os.Stderr, opts)
    default:
        handler = slog.NewTextHandler(os.Stderr, opts)
    }

    return slog.New(handler)
}

func parseLevel(level string) slog.Level {
    switch level {
    case "debug":
        return slog.LevelDebug
    case "warn", "warning":
        return slog.LevelWarn
    case "error":
        return slog.LevelError
    default:
        return slog.LevelInfo
    }
}
```

#### 1.5 Deliverables

- [ ] `go build` produces binary
- [ ] `./go-ffmpeg-hls-swarm --help` shows all flags
- [ ] Config validation rejects invalid combinations
- [ ] JSON logging works

---

### Phase 2: Core Orchestration

**Goal**: Client manager, ramp scheduler, supervisor skeleton

**Duration**: 2-3 days

#### 2.1 Supervisor State Machine (`internal/supervisor/state.go`)

```go
// supervisor/state.go

package supervisor

type State int

const (
    StateCreated State = iota
    StateStarting
    StateRunning
    StateBackoff
    StateStopped
)

func (s State) String() string {
    return [...]string{"created", "starting", "running", "backoff", "stopped"}[s]
}
```

#### 2.2 Backoff Calculator (`internal/supervisor/backoff.go`)

```go
// supervisor/backoff.go

package supervisor

import (
    "math"
    "math/rand"
    "time"
)

type Backoff struct {
    Initial    time.Duration
    Max        time.Duration
    Multiplier float64
    attempts   int
    rng        *rand.Rand
}

func NewBackoff(clientID int, configSeed int64) *Backoff {
    seed := int64(clientID) ^ configSeed
    return &Backoff{
        Initial:    250 * time.Millisecond,
        Max:        5 * time.Second,
        Multiplier: 1.7,
        rng:        rand.New(rand.NewSource(seed)),
    }
}

func (b *Backoff) Next() time.Duration {
    delay := float64(b.Initial) * math.Pow(b.Multiplier, float64(b.attempts))
    if delay > float64(b.Max) {
        delay = float64(b.Max)
    }

    // Add ±20% jitter
    jitter := delay * 0.4 * b.rng.Float64() - delay*0.2
    delay += jitter

    b.attempts++
    return time.Duration(delay)
}

func (b *Backoff) Reset() {
    b.attempts = 0
}

func (b *Backoff) Attempts() int {
    return b.attempts
}
```

#### 2.3 Supervisor (`internal/supervisor/supervisor.go`)

```go
// supervisor/supervisor.go

package supervisor

import (
    "context"
    "log/slog"
    "sync"
    "time"

    "github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/process"
)

const BackoffResetThreshold = 30 * time.Second

type Supervisor struct {
    clientID int
    runner   process.Runner
    backoff  *Backoff
    logger   *slog.Logger

    state     State
    stateMu   sync.RWMutex
    startTime time.Time

    // Callbacks
    onStateChange func(clientID int, state State)
    onExit        func(clientID int, exitCode int, uptime time.Duration)
}

func New(clientID int, runner process.Runner, backoff *Backoff, logger *slog.Logger) *Supervisor {
    return &Supervisor{
        clientID: clientID,
        runner:   runner,
        backoff:  backoff,
        logger:   logger,
        state:    StateCreated,
    }
}

func (s *Supervisor) Run(ctx context.Context) error {
    for {
        select {
        case <-ctx.Done():
            s.setState(StateStopped)
            return ctx.Err()
        default:
        }

        // Start process
        s.setState(StateStarting)
        s.startTime = time.Now()

        cmd, err := s.runner.BuildCommand(ctx, s.clientID)
        if err != nil {
            s.logger.Error("failed to build command", "client_id", s.clientID, "error", err)
            s.backoffWait(ctx)
            continue
        }

        s.setState(StateRunning)
        s.logger.Info("client_started", "client_id", s.clientID, "pid", cmd.Process.Pid)

        // Wait for process to exit
        err = cmd.Wait()
        uptime := time.Since(s.startTime)
        exitCode := exitCodeFromError(err)

        s.logger.Info("client_exited",
            "client_id", s.clientID,
            "exit_code", exitCode,
            "uptime", uptime.String(),
        )

        if s.onExit != nil {
            s.onExit(s.clientID, exitCode, uptime)
        }

        // Check if we should reset backoff
        if s.shouldResetBackoff(uptime, exitCode) {
            s.backoff.Reset()
        }

        // Backoff before restart
        s.backoffWait(ctx)
    }
}

func (s *Supervisor) backoffWait(ctx context.Context) {
    s.setState(StateBackoff)
    delay := s.backoff.Next()
    s.logger.Debug("client_restart_scheduled",
        "client_id", s.clientID,
        "backoff_ms", delay.Milliseconds(),
    )

    select {
    case <-ctx.Done():
    case <-time.After(delay):
    }
}

func (s *Supervisor) shouldResetBackoff(uptime time.Duration, exitCode int) bool {
    if uptime >= BackoffResetThreshold {
        return true
    }
    if exitCode == 0 {
        return true
    }
    return false
}

func (s *Supervisor) setState(state State) {
    s.stateMu.Lock()
    s.state = state
    s.stateMu.Unlock()

    if s.onStateChange != nil {
        s.onStateChange(s.clientID, state)
    }
}

func (s *Supervisor) State() State {
    s.stateMu.RLock()
    defer s.stateMu.RUnlock()
    return s.state
}

func exitCodeFromError(err error) int {
    if err == nil {
        return 0
    }
    // Extract exit code from *exec.ExitError
    // Implementation details...
    return 1
}
```

#### 2.4 Ramp Scheduler (`internal/orchestrator/ramp_scheduler.go`)

```go
// orchestrator/ramp_scheduler.go

package orchestrator

import (
    "context"
    "math/rand"
    "time"
)

type RampScheduler struct {
    rate       int           // clients per second
    maxJitter  time.Duration
    configSeed int64
}

func NewRampScheduler(rate int, maxJitter time.Duration) *RampScheduler {
    return &RampScheduler{
        rate:       rate,
        maxJitter:  maxJitter,
        configSeed: time.Now().UnixNano(),
    }
}

func (r *RampScheduler) Schedule(ctx context.Context, clientID int) error {
    // Base delay from rate
    baseDelay := time.Second / time.Duration(r.rate)

    // Per-client jitter (deterministic)
    jitter := r.clientJitter(clientID)

    totalDelay := baseDelay + jitter

    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-time.After(totalDelay):
        return nil
    }
}

func (r *RampScheduler) clientJitter(clientID int) time.Duration {
    seed := int64(clientID) ^ r.configSeed
    rng := rand.New(rand.NewSource(seed))
    return time.Duration(rng.Int63n(int64(r.maxJitter)))
}
```

#### 2.5 Client Manager (`internal/orchestrator/client_manager.go`)

```go
// orchestrator/client_manager.go

package orchestrator

import (
    "context"
    "log/slog"
    "sync"

    "github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/metrics"
    "github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/process"
    "github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/supervisor"
)

type ClientManager struct {
    runner      process.Runner
    metrics     *metrics.Collector
    logger      *slog.Logger

    supervisors map[int]*supervisor.Supervisor
    mu          sync.RWMutex
    wg          sync.WaitGroup
}

func NewClientManager(runner process.Runner, metrics *metrics.Collector, logger *slog.Logger) *ClientManager {
    return &ClientManager{
        runner:      runner,
        metrics:     metrics,
        logger:      logger,
        supervisors: make(map[int]*supervisor.Supervisor),
    }
}

func (m *ClientManager) StartClient(ctx context.Context, clientID int) {
    backoff := supervisor.NewBackoff(clientID, time.Now().UnixNano())

    sup := supervisor.New(clientID, m.runner, backoff, m.logger)

    // Wire up callbacks
    sup.OnStateChange = func(id int, state supervisor.State) {
        m.metrics.SetClientState(id, state)
    }
    sup.OnExit = func(id int, exitCode int, uptime time.Duration) {
        m.metrics.RecordExit(exitCode, uptime)
    }

    m.mu.Lock()
    m.supervisors[clientID] = sup
    m.mu.Unlock()

    m.wg.Add(1)
    go func() {
        defer m.wg.Done()
        sup.Run(ctx)
    }()

    m.metrics.ClientStarted()
}

func (m *ClientManager) Shutdown(ctx context.Context) {
    // Wait for all supervisors to stop (context cancellation stops them)
    done := make(chan struct{})
    go func() {
        m.wg.Wait()
        close(done)
    }()

    select {
    case <-done:
        m.logger.Info("all_clients_stopped")
    case <-ctx.Done():
        m.logger.Warn("shutdown_timeout")
    }
}

func (m *ClientManager) ActiveCount() int {
    m.mu.RLock()
    defer m.mu.RUnlock()

    count := 0
    for _, sup := range m.supervisors {
        if sup.State() == supervisor.StateRunning {
            count++
        }
    }
    return count
}
```

#### 2.6 Deliverables

- [ ] Supervisor starts and restarts a dummy process
- [ ] Backoff increases exponentially with jitter
- [ ] Ramp scheduler respects rate limiting
- [ ] Client manager tracks all supervisors

---

### Phase 3: FFmpeg Integration

**Goal**: FFmpeg process runner, command building

**Duration**: 2-3 days

#### 3.1 Process Runner Interface (`internal/process/runner.go`)

```go
// process/runner.go

package process

import (
    "context"
    "os/exec"
)

// Runner creates executable commands for clients.
type Runner interface {
    // BuildCommand returns a ready-to-run command for the given client.
    // The command should NOT be started yet.
    BuildCommand(ctx context.Context, clientID int) (*exec.Cmd, error)

    // Name returns a human-readable name for this runner type.
    Name() string
}
```

#### 3.2 FFmpeg Runner (`internal/process/ffmpeg.go`)

```go
// process/ffmpeg.go

package process

import (
    "context"
    "fmt"
    "net/url"
    "os/exec"
    "strconv"
    "strings"
    "time"
)

type FFmpegConfig struct {
    BinaryPath        string
    StreamURL         string
    Variant           string // all, highest, lowest, first
    UserAgent         string
    Timeout           time.Duration
    Reconnect         bool
    ReconnectDelayMax int
    SegMaxRetry       int
    LogLevel          string

    // Network overrides
    ResolveIP     string
    DangerousMode bool
    NoCache       bool
    Headers       []string
}

type FFmpegRunner struct {
    config    *FFmpegConfig
    programID int // For highest/lowest, determined by ffprobe
}

func NewFFmpegRunner(cfg *FFmpegConfig) *FFmpegRunner {
    return &FFmpegRunner{
        config:    cfg,
        programID: -1,
    }
}

func (r *FFmpegRunner) Name() string {
    return "ffmpeg"
}

func (r *FFmpegRunner) BuildCommand(ctx context.Context, clientID int) (*exec.Cmd, error) {
    args := r.buildArgs()

    cmd := exec.CommandContext(ctx, r.config.BinaryPath, args...)

    // Set process group for clean shutdown
    // cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

    return cmd, nil
}

func (r *FFmpegRunner) buildArgs() []string {
    args := []string{
        "-hide_banner",
        "-nostdin",
        "-loglevel", r.config.LogLevel,
    }

    // TLS verification (must be early)
    if r.config.DangerousMode && r.config.ResolveIP != "" {
        args = append(args, "-tls_verify", "0")
    }

    // Reconnection flags
    if r.config.Reconnect {
        args = append(args,
            "-reconnect", "1",
            "-reconnect_streamed", "1",
            "-reconnect_on_network_error", "1",
            "-reconnect_delay_max", strconv.Itoa(r.config.ReconnectDelayMax),
        )
    }

    // Timeout (in microseconds)
    args = append(args, "-rw_timeout", strconv.FormatInt(r.config.Timeout.Microseconds(), 10))

    // User agent
    args = append(args, "-user_agent", r.config.UserAgent)

    // Headers
    headers := r.buildHeaders()
    if len(headers) > 0 {
        args = append(args, "-headers", strings.Join(headers, "\r\n")+"\r\n")
    }

    // Segment retry
    args = append(args, "-seg_max_retry", strconv.Itoa(r.config.SegMaxRetry))

    // Input URL (potentially rewritten for IP override)
    inputURL := r.effectiveURL()
    args = append(args, "-i", inputURL)

    // Mapping based on variant
    args = append(args, r.mapArgs()...)

    // Output: copy to null
    args = append(args, "-c", "copy", "-f", "null", "-")

    return args
}

func (r *FFmpegRunner) buildHeaders() []string {
    var headers []string

    // Host header for IP override
    if r.config.ResolveIP != "" {
        u, _ := url.Parse(r.config.StreamURL)
        headers = append(headers, fmt.Sprintf("Host: %s", u.Host))
    }

    // Cache bypass headers
    if r.config.NoCache {
        headers = append(headers,
            "Cache-Control: no-cache, no-store, must-revalidate",
            "Pragma: no-cache",
        )
    }

    // Custom headers
    headers = append(headers, r.config.Headers...)

    return headers
}

func (r *FFmpegRunner) effectiveURL() string {
    if r.config.ResolveIP == "" {
        return r.config.StreamURL
    }

    // Replace hostname with IP
    u, err := url.Parse(r.config.StreamURL)
    if err != nil {
        return r.config.StreamURL
    }

    u.Host = r.config.ResolveIP
    return u.String()
}

func (r *FFmpegRunner) mapArgs() []string {
    switch r.config.Variant {
    case "all":
        return []string{"-map", "0"}
    case "first":
        return []string{"-map", "0:v:0?", "-map", "0:a:0?"}
    case "highest", "lowest":
        if r.programID >= 0 {
            return []string{"-map", fmt.Sprintf("0:p:%d", r.programID)}
        }
        // Fallback to first
        return []string{"-map", "0:v:0?", "-map", "0:a:0?"}
    default:
        return []string{"-map", "0"}
    }
}
```

#### 3.3 FFprobe for Variant Selection (`internal/process/probe.go`)

```go
// process/probe.go

package process

import (
    "context"
    "encoding/json"
    "os/exec"
    "sort"
)

type ProbeResult struct {
    Programs []Program `json:"programs"`
}

type Program struct {
    ProgramID int   `json:"program_id"`
    Tags      Tags  `json:"tags"`
}

type Tags struct {
    VariantBitrate string `json:"variant_bitrate"`
}

func (r *FFmpegRunner) ProbeVariants(ctx context.Context) error {
    if r.config.Variant != "highest" && r.config.Variant != "lowest" {
        return nil // No probe needed
    }

    cmd := exec.CommandContext(ctx, "ffprobe",
        "-v", "quiet",
        "-print_format", "json",
        "-show_programs",
        r.config.StreamURL,
    )

    output, err := cmd.Output()
    if err != nil {
        return err
    }

    var result ProbeResult
    if err := json.Unmarshal(output, &result); err != nil {
        return err
    }

    if len(result.Programs) == 0 {
        return fmt.Errorf("no programs found in stream")
    }

    // Sort by bitrate
    programs := result.Programs
    sort.Slice(programs, func(i, j int) bool {
        bi := parseBitrate(programs[i].Tags.VariantBitrate)
        bj := parseBitrate(programs[j].Tags.VariantBitrate)
        return bi < bj
    })

    if r.config.Variant == "highest" {
        r.programID = programs[len(programs)-1].ProgramID
    } else {
        r.programID = programs[0].ProgramID
    }

    return nil
}
```

#### 3.4 Deliverables

- [ ] FFmpeg command builds correctly for all flag combinations
- [ ] `--print-cmd` outputs the exact FFmpeg command
- [ ] FFprobe variant detection works for highest/lowest
- [ ] IP override rewrites URL correctly

---

### Phase 4: Observability

**Goal**: Prometheus metrics, logging, exit summary

**Duration**: 2 days

#### 4.1 Metrics Collector (`internal/metrics/collector.go`)

```go
// metrics/collector.go

package metrics

import (
    "sync"
    "time"

    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

type Collector struct {
    // Gauges
    clientsActive prometheus.Gauge
    clientsTarget prometheus.Gauge
    rampProgress  prometheus.Gauge

    // Counters
    clientsStarted   prometheus.Counter
    clientsRestarted prometheus.Counter
    processExits     *prometheus.CounterVec

    // Histograms
    clientUptime prometheus.Histogram

    // Internal tracking for summary
    mu             sync.Mutex
    startTime      time.Time
    peakActive     int
    totalStarts    int64
    totalRestarts  int64
    exitCodes      map[int]int64
    uptimes        []time.Duration
}

func NewCollector(targetClients int) *Collector {
    c := &Collector{
        startTime: time.Now(),
        exitCodes: make(map[int]int64),
        uptimes:   make([]time.Duration, 0),
    }

    c.clientsActive = promauto.NewGauge(prometheus.GaugeOpts{
        Name: "hlsswarm_clients_active",
        Help: "Currently running FFmpeg processes",
    })

    c.clientsTarget = promauto.NewGauge(prometheus.GaugeOpts{
        Name: "hlsswarm_clients_target",
        Help: "Configured target client count",
    })
    c.clientsTarget.Set(float64(targetClients))

    c.rampProgress = promauto.NewGauge(prometheus.GaugeOpts{
        Name: "hlsswarm_ramp_progress",
        Help: "Ramp-up progress (0.0 to 1.0)",
    })

    c.clientsStarted = promauto.NewCounter(prometheus.CounterOpts{
        Name: "hlsswarm_clients_started_total",
        Help: "Total clients started",
    })

    c.clientsRestarted = promauto.NewCounter(prometheus.CounterOpts{
        Name: "hlsswarm_clients_restarted_total",
        Help: "Total restart events",
    })

    c.processExits = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "hlsswarm_process_exits_total",
        Help: "Process exits by exit code",
    }, []string{"code"})

    c.clientUptime = promauto.NewHistogram(prometheus.HistogramOpts{
        Name:    "hlsswarm_client_uptime_seconds",
        Help:    "Client uptime before exit",
        Buckets: []float64{1, 5, 30, 60, 300, 600, 1800, 3600},
    })

    return c
}

func (c *Collector) ClientStarted() {
    c.clientsStarted.Inc()
    c.mu.Lock()
    c.totalStarts++
    c.mu.Unlock()
}

func (c *Collector) SetActiveCount(count int) {
    c.clientsActive.Set(float64(count))
    c.mu.Lock()
    if count > c.peakActive {
        c.peakActive = count
    }
    c.mu.Unlock()
}

func (c *Collector) RecordExit(exitCode int, uptime time.Duration) {
    c.processExits.WithLabelValues(strconv.Itoa(exitCode)).Inc()
    c.clientUptime.Observe(uptime.Seconds())

    c.mu.Lock()
    c.exitCodes[exitCode]++
    c.uptimes = append(c.uptimes, uptime)
    c.mu.Unlock()
}

func (c *Collector) RecordRestart() {
    c.clientsRestarted.Inc()
    c.mu.Lock()
    c.totalRestarts++
    c.mu.Unlock()
}
```

#### 4.2 Metrics Server (`internal/metrics/server.go`)

```go
// metrics/server.go

package metrics

import (
    "context"
    "net/http"
    "time"

    "github.com/prometheus/client_golang/prometheus/promhttp"
)

type Server struct {
    addr   string
    server *http.Server
}

func NewServer(addr string) *Server {
    mux := http.NewServeMux()
    mux.Handle("/metrics", promhttp.Handler())
    mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("ok"))
    })

    return &Server{
        addr: addr,
        server: &http.Server{
            Addr:    addr,
            Handler: mux,
        },
    }
}

func (s *Server) Start() error {
    return s.server.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
    return s.server.Shutdown(ctx)
}
```

#### 4.3 Exit Summary (`internal/metrics/summary.go`)

```go
// metrics/summary.go

package metrics

import (
    "fmt"
    "io"
    "sort"
    "time"
)

type Summary struct {
    Duration          time.Duration
    TargetClients     int
    PeakActiveClients int
    TotalStarts       int64
    TotalRestarts     int64
    ExitCodes         map[int]int64
    UptimeP50         time.Duration
    UptimeP95         time.Duration
}

func (c *Collector) GenerateSummary(targetClients int) *Summary {
    c.mu.Lock()
    defer c.mu.Unlock()

    s := &Summary{
        Duration:          time.Since(c.startTime),
        TargetClients:     targetClients,
        PeakActiveClients: c.peakActive,
        TotalStarts:       c.totalStarts,
        TotalRestarts:     c.totalRestarts,
        ExitCodes:         make(map[int]int64),
    }

    for code, count := range c.exitCodes {
        s.ExitCodes[code] = count
    }

    // Calculate percentiles
    if len(c.uptimes) > 0 {
        sorted := make([]time.Duration, len(c.uptimes))
        copy(sorted, c.uptimes)
        sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

        s.UptimeP50 = percentile(sorted, 0.50)
        s.UptimeP95 = percentile(sorted, 0.95)
    }

    return s
}

func (s *Summary) Print(w io.Writer) {
    fmt.Fprintln(w, "═══════════════════════════════════════════════════════════════════")
    fmt.Fprintln(w, "                        go-ffmpeg-hls-swarm Exit Summary")
    fmt.Fprintln(w, "═══════════════════════════════════════════════════════════════════")
    fmt.Fprintf(w, "Run Duration:           %s\n", formatDuration(s.Duration))
    fmt.Fprintf(w, "Target Clients:         %d\n", s.TargetClients)
    fmt.Fprintf(w, "Peak Active Clients:    %d\n", s.PeakActiveClients)
    fmt.Fprintln(w)
    fmt.Fprintf(w, "Uptime Distribution:\n")
    fmt.Fprintf(w, "  P50 (median):         %s\n", formatDuration(s.UptimeP50))
    fmt.Fprintf(w, "  P95:                  %s\n", formatDuration(s.UptimeP95))
    fmt.Fprintln(w)
    fmt.Fprintf(w, "Lifecycle:\n")
    fmt.Fprintf(w, "  Total Starts:         %d\n", s.TotalStarts)
    fmt.Fprintf(w, "  Total Restarts:       %d\n", s.TotalRestarts)
    fmt.Fprintln(w)
    fmt.Fprintf(w, "Exit Codes:\n")
    for code, count := range s.ExitCodes {
        fmt.Fprintf(w, "  %3d:                  %d\n", code, count)
    }
    fmt.Fprintln(w, "═══════════════════════════════════════════════════════════════════")
}

func percentile(sorted []time.Duration, p float64) time.Duration {
    idx := int(float64(len(sorted)-1) * p)
    return sorted[idx]
}

func formatDuration(d time.Duration) string {
    h := int(d.Hours())
    m := int(d.Minutes()) % 60
    s := int(d.Seconds()) % 60
    return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}
```

#### 4.4 Deliverables

- [ ] `/metrics` endpoint returns Prometheus metrics
- [ ] `/health` endpoint returns 200 OK
- [ ] Exit summary prints on shutdown
- [ ] All metrics from [OBSERVABILITY.md](OBSERVABILITY.md) implemented

---

### Phase 5: CLI & Container Integration

**Goal**: Complete CLI, preflight checks, container compatibility

**Duration**: 2 days

#### 5.1 Preflight Checks (`internal/preflight/`)

```go
// preflight/checks.go

package preflight

import (
    "fmt"
    "os/exec"
    "syscall"
)

type Check struct {
    Name     string
    Required int
    Actual   int
    Passed   bool
    Message  string
}

func RunAll(targetClients int, ffmpegPath string) ([]Check, error) {
    checks := []Check{}

    // File descriptor check
    fdCheck := checkFileDescriptors(targetClients)
    checks = append(checks, fdCheck)

    // FFmpeg check
    ffmpegCheck := checkFFmpeg(ffmpegPath)
    checks = append(checks, ffmpegCheck)

    // Check for failures
    for _, c := range checks {
        if !c.Passed {
            return checks, fmt.Errorf("preflight failed: %s", c.Name)
        }
    }

    return checks, nil
}

func checkFileDescriptors(clients int) Check {
    var limit syscall.Rlimit
    syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit)

    required := clients*20 + 100
    actual := int(limit.Cur)

    return Check{
        Name:     "file_descriptors",
        Required: required,
        Actual:   actual,
        Passed:   actual >= required,
        Message:  fmt.Sprintf("need %d, have %d", required, actual),
    }
}

func checkFFmpeg(path string) Check {
    cmd := exec.Command(path, "-version")
    output, err := cmd.Output()

    if err != nil {
        return Check{
            Name:    "ffmpeg",
            Passed:  false,
            Message: fmt.Sprintf("not found at %s", path),
        }
    }

    return Check{
        Name:    "ffmpeg",
        Passed:  true,
        Message: string(output[:50]) + "...",
    }
}
```

#### 5.2 Main Orchestrator (`internal/orchestrator/orchestrator.go`)

```go
// orchestrator/orchestrator.go

package orchestrator

import (
    "context"
    "log/slog"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/config"
    "github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/metrics"
    "github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/preflight"
    "github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/process"
)

type Orchestrator struct {
    config        *config.Config
    logger        *slog.Logger
    metrics       *metrics.Collector
    metricsServer *metrics.Server
    clientManager *ClientManager
    rampScheduler *RampScheduler
}

func New(cfg *config.Config, logger *slog.Logger) *Orchestrator {
    collector := metrics.NewCollector(cfg.Clients)
    metricsServer := metrics.NewServer(cfg.MetricsAddr)

    ffmpegConfig := &process.FFmpegConfig{
        BinaryPath:        cfg.FFmpegPath,
        StreamURL:         cfg.StreamURL,
        Variant:           cfg.Variant,
        UserAgent:         cfg.UserAgent,
        Timeout:           cfg.Timeout,
        Reconnect:         cfg.Reconnect,
        ReconnectDelayMax: cfg.ReconnectDelayMax,
        SegMaxRetry:       cfg.SegMaxRetry,
        LogLevel:          "info",
        ResolveIP:         cfg.ResolveIP,
        DangerousMode:     cfg.DangerousMode,
        NoCache:           cfg.NoCache,
        Headers:           cfg.Headers,
    }
    runner := process.NewFFmpegRunner(ffmpegConfig)

    return &Orchestrator{
        config:        cfg,
        logger:        logger,
        metrics:       collector,
        metricsServer: metricsServer,
        clientManager: NewClientManager(runner, collector, logger),
        rampScheduler: NewRampScheduler(cfg.RampRate, cfg.RampJitter),
    }
}

func (o *Orchestrator) Run(ctx context.Context) error {
    // Preflight checks
    if !o.config.SkipPreflight {
        checks, err := preflight.RunAll(o.config.Clients, o.config.FFmpegPath)
        o.printPreflightResults(checks)
        if err != nil {
            return err
        }
    }

    // Start metrics server
    go func() {
        if err := o.metricsServer.Start(); err != nil {
            o.logger.Error("metrics server failed", "error", err)
        }
    }()
    o.logger.Info("metrics_server_started", "addr", o.config.MetricsAddr)

    // Setup signal handling
    ctx, cancel := context.WithCancel(ctx)
    defer cancel()

    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

    // Ramp up clients
    o.logger.Info("starting_ramp", "clients", o.config.Clients, "rate", o.config.RampRate)

    for i := 0; i < o.config.Clients; i++ {
        select {
        case <-ctx.Done():
            break
        default:
        }

        if err := o.rampScheduler.Schedule(ctx, i); err != nil {
            break
        }

        o.clientManager.StartClient(ctx, i)
        o.metrics.SetRampProgress(float64(i+1) / float64(o.config.Clients))
    }

    o.logger.Info("ramp_complete", "clients", o.config.Clients)

    // Wait for signal or duration
    select {
    case sig := <-sigCh:
        o.logger.Info("received_signal", "signal", sig.String())
    case <-ctx.Done():
    }

    // Graceful shutdown
    cancel() // Cancel all supervisors

    shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer shutdownCancel()

    o.clientManager.Shutdown(shutdownCtx)
    o.metricsServer.Shutdown(shutdownCtx)

    // Print exit summary
    summary := o.metrics.GenerateSummary(o.config.Clients)
    summary.Print(os.Stdout)

    return nil
}
```

#### 5.3 Main Entry Point (`cmd/go-ffmpeg-hls-swarm/main.go`)

```go
// cmd/go-ffmpeg-hls-swarm/main.go

package main

import (
    "context"
    "fmt"
    "os"

    "github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/config"
    "github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/logging"
    "github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/orchestrator"
    "github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/process"
)

var version = "dev"

func main() {
    // Version flag
    if len(os.Args) > 1 && (os.Args[1] == "-v" || os.Args[1] == "--version" || os.Args[1] == "version") {
        fmt.Printf("go-ffmpeg-hls-swarm %s\n", version)
        return
    }

    // Parse config
    cfg, err := config.ParseFlags()
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error: %v\n", err)
        os.Exit(1)
    }

    // Validate
    if err := config.Validate(cfg); err != nil {
        fmt.Fprintf(os.Stderr, "Error: %v\n", err)
        os.Exit(1)
    }

    // Initialize logger
    logger := logging.NewLogger(cfg.LogFormat, cfg.LogLevel)

    // Handle --print-cmd
    if cfg.PrintCmd {
        runner := process.NewFFmpegRunner(configToFFmpegConfig(cfg))
        cmd, _ := runner.BuildCommand(context.Background(), 0)
        fmt.Println(cmd.String())
        return
    }

    // Handle --check
    if cfg.Check {
        cfg.Clients = 1
        cfg.Duration = 10 * time.Second
    }

    // Run orchestrator
    orch := orchestrator.New(cfg, logger)
    if err := orch.Run(context.Background()); err != nil {
        logger.Error("orchestrator failed", "error", err)
        os.Exit(1)
    }
}
```

#### 5.4 Deliverables

- [ ] `./go-ffmpeg-hls-swarm -clients 5 https://...` runs successfully
- [ ] Preflight checks validate system resources
- [ ] `--print-cmd` outputs valid FFmpeg command
- [ ] `--check` runs single client for 10 seconds
- [ ] Works with container entrypoint environment variables

---

### Phase 6: Polish & Testing

**Goal**: Testing, edge cases, documentation

**Duration**: 2-3 days

#### 6.1 Unit Tests

| Package | Test Focus |
|---------|------------|
| `config` | Flag parsing, validation, defaults |
| `supervisor/backoff` | Exponential calculation, jitter bounds |
| `process/ffmpeg` | Command building for all flag combinations |
| `metrics` | Counter increments, histogram buckets |

#### 6.2 Integration Tests

```go
// Test: Start and stop 5 clients
func TestOrchestratorStartStop(t *testing.T) {
    cfg := config.DefaultConfig()
    cfg.Clients = 5
    cfg.StreamURL = "https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8"

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    orch := orchestrator.New(cfg, slog.Default())

    go func() {
        time.Sleep(10 * time.Second)
        cancel()
    }()

    err := orch.Run(ctx)
    assert.NoError(t, err)
}
```

#### 6.3 Deliverables

- [ ] Unit tests pass with `go test ./...`
- [ ] Integration test with public HLS stream
- [ ] Manual test with container build
- [ ] Update README with actual working examples

---

## CLI Interface Specification

To match container expectations from `container.nix`:

| CLI Flag | Environment Variable | Default | Description |
|----------|---------------------|---------|-------------|
| `-clients` | `CLIENTS` | 10 | Number of concurrent clients |
| `-ramp-rate` | `RAMP_RATE` | 5 | Clients per second |
| `-metrics` | `METRICS_PORT` | 9090 | Metrics port |
| `-log-level` | `LOG_LEVEL` | info | Log verbosity |
| `-variant` | `VARIANT` | all | Bitrate selection |
| `<URL>` | `STREAM_URL` | — | HLS stream URL (required) |

---

## Testing Strategy

### Local Testing

```bash
# Build
go build -o go-ffmpeg-hls-swarm ./cmd/go-ffmpeg-hls-swarm

# Quick test
./go-ffmpeg-hls-swarm -clients 5 https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8

# Check metrics
curl http://localhost:9090/metrics | grep hlsswarm
```

### Container Testing

```bash
# Build container (with Nix)
nix build .#swarm-client-container
docker load < ./result

# Run
docker run --rm \
  -e STREAM_URL=https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8 \
  -e CLIENTS=10 \
  -p 9090:9090 \
  go-ffmpeg-hls-swarm:latest
```

### With Test Origin

```bash
# Start test origin
nix run .#test-origin &

# Run swarm against it
./go-ffmpeg-hls-swarm -clients 50 http://localhost:8080/stream.m3u8
```

---

## Milestone Checklist

### Milestone 1: Foundation (Phase 1)
- [ ] Project compiles
- [ ] CLI flags parse correctly
- [ ] Logger outputs JSON

### Milestone 2: Orchestration (Phase 2)
- [ ] Supervisor restarts dummy processes
- [ ] Backoff works correctly
- [ ] Client manager tracks state

### Milestone 3: FFmpeg (Phase 3)
- [ ] FFmpeg commands execute
- [ ] All variant modes work
- [ ] `--print-cmd` is accurate

### Milestone 4: Observability (Phase 4)
- [ ] `/metrics` endpoint works
- [ ] Exit summary prints
- [ ] All metrics from spec implemented

### Milestone 5: Integration (Phase 5)
- [ ] End-to-end test passes
- [ ] Container works
- [ ] Preflight checks gate startup

### Milestone 6: Release (Phase 6)
- [ ] Tests pass
- [ ] Documentation updated
- [ ] Version tagged

---

## Estimated Timeline

| Phase | Duration | Cumulative |
|-------|----------|------------|
| Phase 1: Foundation | 1-2 days | 2 days |
| Phase 2: Orchestration | 2-3 days | 5 days |
| Phase 3: FFmpeg | 2-3 days | 8 days |
| Phase 4: Observability | 2 days | 10 days |
| Phase 5: CLI & Integration | 2 days | 12 days |
| Phase 6: Polish | 2-3 days | 15 days |

**Total: ~3 weeks for MVP**

---

## Next Steps

1. **Start with Phase 1** — Get the project skeleton building
2. **Create `internal/` directories** — Match the package structure
3. **Implement incrementally** — Each phase should produce testable output
4. **Test against public streams** — Use Mux test stream for development
5. **Integrate with Nix** — Update `flake.nix` to build the Go binary

---

## Related Documents

- [DESIGN.md](DESIGN.md) — Architecture and interfaces
- [SUPERVISION.md](SUPERVISION.md) — Process lifecycle details
- [CONFIGURATION.md](CONFIGURATION.md) — CLI flag reference
- [OBSERVABILITY.md](OBSERVABILITY.md) — Metrics specification
- [CLIENT_DEPLOYMENT.md](CLIENT_DEPLOYMENT.md) — Container/MicroVM deployment
