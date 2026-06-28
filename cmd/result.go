package cmd

import "time"

// stepResult is one executed leaf step's outcome. The engine accumulates these
// so play can emit a structured report; everything downstream (JSON now, other
// formats later) is a transform of this.
type stepResult struct {
	Name       string `json:"name"`
	Server     string `json:"server,omitempty"`
	Tool       string `json:"tool,omitempty"`
	Resource   string `json:"resource,omitempty"`
	Status     string `json:"status"` // ok | failed | skipped
	Message    string `json:"message,omitempty"`
	DurationMS int64  `json:"duration_ms"`

	// absolute span bounds for OTLP export; not serialized to the user JSON.
	startedAt time.Time
	endedAt   time.Time
}

// runResult is the whole-run report. Ok and Failures drive the exit code;
// Steps is the per-step detail.
type runResult struct {
	Ok         bool         `json:"ok"`
	Total      int          `json:"total"`
	Passed     int          `json:"passed"`
	Failed     int          `json:"failed"`
	Skipped    int          `json:"skipped"`
	DurationMS int64        `json:"duration_ms"`
	Steps      []stepResult `json:"steps"`
	Failures   []string     `json:"failures,omitempty"`
}

func summarize(results []stepResult, failures []string, elapsed time.Duration) runResult {
	r := runResult{
		Ok:         len(failures) == 0,
		Total:      len(results),
		DurationMS: elapsed.Milliseconds(),
		Steps:      results,
		Failures:   failures,
	}
	for _, s := range results {
		switch s.Status {
		case "ok":
			r.Passed++
		case "failed":
			r.Failed++
		case "skipped":
			r.Skipped++
		}
	}
	return r
}
