package cmd

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBuildOTLP(t *testing.T) {
	now := time.Now()
	result := runResult{
		Ok: false, Failed: 1, DurationMS: 100,
		Steps: []stepResult{
			{Name: "a", Tool: "t", Server: "s", Status: "ok", startedAt: now, endedAt: now.Add(time.Millisecond)},
			{Name: "b", Status: "failed", Message: "boom", startedAt: now, endedAt: now},
		},
	}
	b, err := json.Marshal(buildOTLP("test.yaml", result, now))
	if err != nil {
		t.Fatalf("OTLP payload must marshal: %v", err)
	}

	var parsed struct {
		ResourceSpans []struct {
			ScopeSpans []struct {
				Spans []struct {
					TraceID string `json:"traceId"`
					Name    string `json:"name"`
					Status  struct {
						Code int `json:"code"`
					} `json:"status"`
				} `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatal(err)
	}
	spans := parsed.ResourceSpans[0].ScopeSpans[0].Spans
	if len(spans) != 3 { // one run span + two step spans
		t.Fatalf("want 3 spans, got %d", len(spans))
	}
	if len(spans[0].TraceID) != 32 {
		t.Fatalf("traceId should be 16 bytes hex, got %q", spans[0].TraceID)
	}
	for _, s := range spans {
		if s.TraceID != spans[0].TraceID {
			t.Fatal("all spans must share one trace id")
		}
	}
	if spans[0].Status.Code != 2 {
		t.Fatal("run span should carry error status when the run failed")
	}
}

func TestOtelEndpointResolution(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4318/")
	if got := otelEndpoint(); got != "http://collector:4318/v1/traces" {
		t.Fatalf("base endpoint should get /v1/traces appended, got %q", got)
	}
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://x/custom")
	if got := otelEndpoint(); got != "http://x/custom" {
		t.Fatalf("explicit traces endpoint should win, got %q", got)
	}
}
