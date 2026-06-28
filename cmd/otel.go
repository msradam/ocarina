package cmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// exportOTLP posts the run as OpenTelemetry traces using the OTLP/JSON-over-HTTP
// encoding, when an OTLP endpoint is configured via the standard env vars. It
// uses only the standard library: no OpenTelemetry SDK dependency. Best-effort,
// errors go to stderr and never fail the run.
func exportOTLP(name string, result runResult, started time.Time) {
	endpoint := otelEndpoint()
	if endpoint == "" {
		return
	}
	body, err := json.Marshal(buildOTLP(name, result, started))
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "otel: %v\n", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range otelHeaders() {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "otel: export failed: %v\n", err)
		return
	}
	_ = resp.Body.Close()
}

// otelEndpoint returns the OTLP traces URL from the standard env vars, or "".
func otelEndpoint() string {
	if e := os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"); e != "" {
		return e
	}
	if e := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); e != "" {
		return strings.TrimRight(e, "/") + "/v1/traces"
	}
	return ""
}

// otelHeaders parses OTEL_EXPORTER_OTLP_HEADERS (key=value,key=value), e.g. for
// an Authorization header to a hosted collector.
func otelHeaders() map[string]string {
	out := map[string]string{}
	raw := os.Getenv("OTEL_EXPORTER_OTLP_HEADERS")
	if raw == "" {
		return out
	}
	for _, kv := range strings.Split(raw, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

// buildOTLP shapes the run as an OTLP ExportTraceServiceRequest: one root span
// for the run, one child span per step. 64-bit timestamps are JSON strings, per
// the OTLP/JSON spec.
func buildOTLP(name string, result runResult, started time.Time) map[string]any {
	traceID := randHex(16)
	runSpanID := randHex(8)
	end := started.Add(time.Duration(result.DurationMS) * time.Millisecond)

	runStatus := map[string]any{"code": 1} // STATUS_CODE_OK
	if !result.Ok {
		runStatus = map[string]any{"code": 2, "message": fmt.Sprintf("%d step(s) failed", result.Failed)}
	}
	spans := []any{map[string]any{
		"traceId": traceID, "spanId": runSpanID,
		"name": "run " + name, "kind": 1,
		"startTimeUnixNano": nano(started), "endTimeUnixNano": nano(end),
		"status": runStatus,
	}}
	for _, s := range result.Steps {
		st, en := s.startedAt, s.endedAt
		if st.IsZero() {
			st, en = started, started
		}
		code := 1
		if s.Status == "failed" {
			code = 2
		}
		spans = append(spans, map[string]any{
			"traceId": traceID, "spanId": randHex(8), "parentSpanId": runSpanID,
			"name": s.Name, "kind": 1,
			"startTimeUnixNano": nano(st), "endTimeUnixNano": nano(en),
			"attributes": []any{
				attr("mcp.tool", s.Tool),
				attr("mcp.server", s.Server),
				attr("ocarina.status", s.Status),
			},
			"status": map[string]any{"code": code, "message": s.Message},
		})
	}
	return map[string]any{"resourceSpans": []any{map[string]any{
		"resource":   map[string]any{"attributes": []any{attr("service.name", "ocarina")}},
		"scopeSpans": []any{map[string]any{"scope": map[string]any{"name": "ocarina"}, "spans": spans}},
	}}}
}

func attr(k, v string) map[string]any {
	return map[string]any{"key": k, "value": map[string]any{"stringValue": v}}
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func nano(t time.Time) string { return strconv.FormatInt(t.UnixNano(), 10) }
