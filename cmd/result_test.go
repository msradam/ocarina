package cmd

import (
	"testing"
	"time"
)

func TestSummarize(t *testing.T) {
	results := []stepResult{
		{Name: "a", Status: "ok"},
		{Name: "b", Status: "failed", Message: "boom"},
		{Name: "c", Status: "skipped"},
	}

	r := summarize(results, []string{`step "b": boom`}, 250*time.Millisecond)
	if r.Total != 3 || r.Passed != 1 || r.Failed != 1 || r.Skipped != 1 {
		t.Fatalf("tally wrong: %+v", r)
	}
	if r.Ok {
		t.Fatal("Ok must be false when there are failures")
	}
	if r.DurationMS != 250 {
		t.Fatalf("duration = %d", r.DurationMS)
	}

	// Recovery case: a step record is failed, but no run-level failures (a block
	// rescued it). Ok derives from failures, not from the per-step records.
	r2 := summarize(results, nil, time.Second)
	if !r2.Ok {
		t.Fatal("Ok must be true when the failures list is empty, even if a step record shows failed")
	}
	if r2.Failed != 1 {
		t.Fatalf("the failed step is still recorded honestly, got Failed=%d", r2.Failed)
	}
}
