// Package load aggregates latency samples from a load run and reports k6-style
// summary statistics. It holds no MCP logic: the caller drives the virtual
// users through the existing rondo engine and feeds results here.
package load

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Collector accumulates per-call results across goroutines.
type Collector struct {
	mu     sync.Mutex
	durs   []time.Duration
	failed int
}

// Record adds one call's latency and whether it passed.
func (c *Collector) Record(d time.Duration, ok bool) {
	c.mu.Lock()
	c.durs = append(c.durs, d)
	if !ok {
		c.failed++
	}
	c.mu.Unlock()
}

// Summary is the k6-shaped result of a load run.
type Summary struct {
	Calls   int
	Failed  int
	Elapsed time.Duration
	RPS     float64
	P50     time.Duration
	P90     time.Duration
	P95     time.Duration
	P99     time.Duration
	Max     time.Duration
}

// Summarize computes the summary over the elapsed wall-clock time. Percentiles
// use the nearest-rank method on the sorted samples; swap in an HdrHistogram if
// sample counts ever grow past what a slice should hold.
func (c *Collector) Summarize(elapsed time.Duration) Summary {
	c.mu.Lock()
	durs := append([]time.Duration(nil), c.durs...)
	failed := c.failed
	c.mu.Unlock()

	s := Summary{Calls: len(durs), Failed: failed, Elapsed: elapsed}
	if elapsed > 0 {
		s.RPS = float64(len(durs)) / elapsed.Seconds()
	}
	if len(durs) > 0 {
		sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
		s.P50 = percentile(durs, 50)
		s.P90 = percentile(durs, 90)
		s.P95 = percentile(durs, 95)
		s.P99 = percentile(durs, 99)
		s.Max = durs[len(durs)-1]
	}
	return s
}

func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := (p*len(sorted) + 99) / 100 // ceil(p/100 * n)
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

func (s Summary) PassRate() float64 {
	if s.Calls == 0 {
		return 100
	}
	return float64(s.Calls-s.Failed) / float64(s.Calls) * 100
}

func (s Summary) String() string {
	round := func(d time.Duration) time.Duration { return d.Round(time.Microsecond) }
	return fmt.Sprintf(
		"calls       %d (%d failed, %.1f%% pass)\n"+
			"throughput  %.0f calls/s over %s\n"+
			"latency     p50 %s   p90 %s   p95 %s   p99 %s   max %s",
		s.Calls, s.Failed, s.PassRate(),
		s.RPS, s.Elapsed.Round(time.Millisecond),
		round(s.P50), round(s.P90), round(s.P95), round(s.P99), round(s.Max),
	)
}

// Threshold is a pass/fail gate like k6's, e.g. "p95<500ms".
type Threshold struct {
	Metric string // p50, p90, p95, p99, max
	Limit  time.Duration
}

// ParseThreshold parses "p95<500ms".
func ParseThreshold(s string) (Threshold, error) {
	metric, limit, ok := strings.Cut(s, "<")
	if !ok {
		return Threshold{}, fmt.Errorf("threshold %q: expected metric<duration, e.g. p95<500ms", s)
	}
	metric = strings.TrimSpace(strings.ToLower(metric))
	switch metric {
	case "p50", "p90", "p95", "p99", "max":
	default:
		return Threshold{}, fmt.Errorf("threshold %q: metric must be p50, p90, p95, p99, or max", s)
	}
	d, err := time.ParseDuration(strings.TrimSpace(limit))
	if err != nil {
		return Threshold{}, fmt.Errorf("threshold %q: %w", s, err)
	}
	return Threshold{Metric: metric, Limit: d}, nil
}

func (t Threshold) value(s Summary) time.Duration {
	switch t.Metric {
	case "p50":
		return s.P50
	case "p90":
		return s.P90
	case "p95":
		return s.P95
	case "p99":
		return s.P99
	default:
		return s.Max
	}
}

// Met reports whether the summary satisfies the threshold.
func (t Threshold) Met(s Summary) bool { return t.value(s) < t.Limit }

func (t Threshold) String() string {
	return t.Metric + "<" + strconv.FormatInt(t.Limit.Milliseconds(), 10) + "ms"
}
