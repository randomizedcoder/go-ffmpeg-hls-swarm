// Package metrics provides Prometheus metrics for go-ffmpeg-hls-swarm.
package metrics

import (
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Collector manages all Prometheus metrics for the swarm.
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
	mu            sync.Mutex
	startTime     time.Time
	targetClients int
	peakActive    int
	totalStarts   int64
	totalRestarts int64
	exitCodes     map[int]int64
	uptimes       []time.Duration
}

// NewCollector creates a new metrics collector.
func NewCollector(targetClients int) *Collector {
	c := &Collector{
		startTime:     time.Now(),
		targetClients: targetClients,
		exitCodes:     make(map[int]int64),
		uptimes:       make([]time.Duration, 0, targetClients),
	}

	// Register metrics
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

// ClientStarted records a client start event.
func (c *Collector) ClientStarted() {
	c.clientsStarted.Inc()

	c.mu.Lock()
	c.totalStarts++
	c.mu.Unlock()
}

// ClientRestarted records a client restart event.
func (c *Collector) ClientRestarted() {
	c.clientsRestarted.Inc()

	c.mu.Lock()
	c.totalRestarts++
	c.mu.Unlock()
}

// SetActiveCount updates the active client count.
func (c *Collector) SetActiveCount(count int) {
	c.clientsActive.Set(float64(count))

	c.mu.Lock()
	if count > c.peakActive {
		c.peakActive = count
	}
	c.mu.Unlock()
}

// SetRampProgress updates the ramp-up progress (0.0 to 1.0).
func (c *Collector) SetRampProgress(progress float64) {
	c.rampProgress.Set(progress)
}

// RecordExit records a process exit event.
func (c *Collector) RecordExit(exitCode int, uptime time.Duration) {
	c.processExits.WithLabelValues(strconv.Itoa(exitCode)).Inc()
	c.clientUptime.Observe(uptime.Seconds())

	c.mu.Lock()
	c.exitCodes[exitCode]++
	c.uptimes = append(c.uptimes, uptime)
	c.mu.Unlock()
}

// Summary holds the data for generating an exit summary.
type Summary struct {
	Duration          time.Duration
	TargetClients     int
	PeakActiveClients int
	TotalStarts       int64
	TotalRestarts     int64
	ExitCodes         map[int]int64
	UptimeP50         time.Duration
	UptimeP95         time.Duration
	UptimeP99         time.Duration
}

// GenerateSummary creates a summary of the run.
func (c *Collector) GenerateSummary() *Summary {
	c.mu.Lock()
	defer c.mu.Unlock()

	s := &Summary{
		Duration:          time.Since(c.startTime),
		TargetClients:     c.targetClients,
		PeakActiveClients: c.peakActive,
		TotalStarts:       c.totalStarts,
		TotalRestarts:     c.totalRestarts,
		ExitCodes:         make(map[int]int64),
	}

	// Copy exit codes
	for code, count := range c.exitCodes {
		s.ExitCodes[code] = count
	}

	// Calculate percentiles
	if len(c.uptimes) > 0 {
		sorted := make([]time.Duration, len(c.uptimes))
		copy(sorted, c.uptimes)
		sortDurations(sorted)

		s.UptimeP50 = percentile(sorted, 0.50)
		s.UptimeP95 = percentile(sorted, 0.95)
		s.UptimeP99 = percentile(sorted, 0.99)
	}

	return s
}

// PeakActive returns the peak active client count.
func (c *Collector) PeakActive() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.peakActive
}

// TotalStarts returns the total number of client starts.
func (c *Collector) TotalStarts() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.totalStarts
}

// TotalRestarts returns the total number of restarts.
func (c *Collector) TotalRestarts() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.totalRestarts
}

// sortDurations sorts a slice of durations in place.
func sortDurations(d []time.Duration) {
	for i := 1; i < len(d); i++ {
		for j := i; j > 0 && d[j] < d[j-1]; j-- {
			d[j], d[j-1] = d[j-1], d[j]
		}
	}
}

// percentile returns the value at the given percentile (0.0-1.0).
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
