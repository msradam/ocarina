package load

import (
	"testing"
	"time"
)

func TestSummaryPercentiles(t *testing.T) {
	c := &Collector{}
	for i := 1; i <= 100; i++ {
		c.Record(time.Duration(i)*time.Millisecond, i != 100) // one failure
	}
	s := c.Summarize(time.Second)
	if s.Calls != 100 || s.Failed != 1 {
		t.Fatalf("calls=%d failed=%d", s.Calls, s.Failed)
	}
	if s.P50 != 50*time.Millisecond || s.P95 != 95*time.Millisecond || s.Max != 100*time.Millisecond {
		t.Fatalf("p50=%v p95=%v max=%v", s.P50, s.P95, s.Max)
	}
	if s.PassRate() != 99 {
		t.Fatalf("pass rate %.1f", s.PassRate())
	}
}

func TestThreshold(t *testing.T) {
	th, err := ParseThreshold("p95<500ms")
	if err != nil || th.Metric != "p95" || th.Limit != 500*time.Millisecond {
		t.Fatalf("parse: %+v %v", th, err)
	}
	if !th.Met(Summary{P95: 400 * time.Millisecond}) {
		t.Fatal("400ms should meet p95<500ms")
	}
	if th.Met(Summary{P95: 600 * time.Millisecond}) {
		t.Fatal("600ms should not meet p95<500ms")
	}
	if _, err := ParseThreshold("nonsense"); err == nil {
		t.Fatal("expected parse error")
	}
	if _, err := ParseThreshold("p77<1s"); err == nil {
		t.Fatal("expected unknown-metric error")
	}
}
