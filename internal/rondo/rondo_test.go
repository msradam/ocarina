package rondo

import (
	"os"
	"path/filepath"
	"testing"
)

func loadString(t *testing.T, body string) *File {
	t.Helper()
	p := filepath.Join(t.TempDir(), "r.yaml")
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestLoadNormalizesAliasesAndServers(t *testing.T) {
	// tasks: -> Steps, register: -> Echo, single server: -> Servers["default"]
	f := loadString(t, `
server: {command: uvx, args: [mcp-server-time]}
tasks:
  - tool: get_current_time
    register: tz
`)
	if len(f.Steps) != 1 || len(f.Tasks) != 0 {
		t.Fatalf("tasks not merged into steps: %+v", f.Steps)
	}
	if f.Steps[0].Echo != "tz" || f.Steps[0].Register != "" {
		t.Fatalf("register not normalized to echo: %+v", f.Steps[0])
	}
	if _, ok := f.Servers["default"]; !ok {
		t.Fatalf("single server not wrapped under default: %+v", f.Servers)
	}
	if f.DefaultServerKey() != "default" {
		t.Fatalf("default key = %q", f.DefaultServerKey())
	}
}

func TestServersOrderAndLookup(t *testing.T) {
	f := loadString(t, `
servers:
  time: {command: uvx, args: [mcp-server-time]}
  fetch: {command: uvx, args: [mcp-server-fetch]}
rondo:
  - tool: get_current_time
  - server: fetch
    tool: fetch
`)
	if got := f.DefaultServerKey(); got != "time" {
		t.Fatalf("first server should be time, got %q", got)
	}
	if !f.MultiServer() {
		t.Fatal("expected MultiServer true")
	}
	// step without server defaults to first; explicit server is honored
	if got := f.StepServerKey(f.Steps[0]); got != "time" {
		t.Fatalf("default StepServerKey = %q", got)
	}
	if got := f.StepServerKey(f.Steps[1]); got != "fetch" {
		t.Fatalf("explicit StepServerKey = %q", got)
	}
	if _, ok := f.Servers["nope"]; ok {
		t.Fatal("undefined server should be absent from the map")
	}
}

func TestLoadHTTPServer(t *testing.T) {
	f := loadString(t, `
server:
  url: https://example.com/mcp
  headers:
    Authorization: "Bearer {{env.TOKEN}}"
rondo:
  - tool: get_me
`)
	s, ok := f.Servers["default"]
	if !ok {
		t.Fatalf("url-only server not wrapped under default: %+v", f.Servers)
	}
	if !s.IsHTTP() || s.URL != "https://example.com/mcp" {
		t.Fatalf("expected HTTP server, got %+v", s)
	}
	if s.Headers["Authorization"] != "Bearer {{env.TOKEN}}" {
		t.Fatalf("headers not parsed: %+v", s.Headers)
	}
}
