package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/msradam/ocarina/internal/rondo"
)

func TestMotifInputSchema(t *testing.T) {
	s := motifInputSchema([]rondo.Param{
		{Name: "dir", Required: true, Description: "the path"},
		{Name: "count", Type: "integer"},
	})
	if s.Type != "object" {
		t.Fatalf("schema type = %q", s.Type)
	}
	if s.Properties["dir"].Type != "string" {
		t.Fatalf("untyped param should default to string, got %q", s.Properties["dir"].Type)
	}
	if s.Properties["count"].Type != "integer" {
		t.Fatalf("explicit type not honored, got %q", s.Properties["count"].Type)
	}
	if len(s.Required) != 1 || s.Required[0] != "dir" {
		t.Fatalf("required = %v, want [dir]", s.Required)
	}
}

func TestServeToolName(t *testing.T) {
	if got := serveToolName("/x/provision.yaml", &rondo.File{}); got != "provision" {
		t.Fatalf("filename-derived name = %q, want provision", got)
	}
	if got := serveToolName("/x/provision.yaml", &rondo.File{Name: "make_workspace"}); got != "make_workspace" {
		t.Fatalf("explicit name should win, got %q", got)
	}
}

func TestCollectServeFiles(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.yaml", "b.yml", "ignore.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := collectServeFiles([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 yaml files, got %v", got)
	}
	// an explicit file passes through unchanged
	one, err := collectServeFiles([]string{filepath.Join(dir, "a.yaml")})
	if err != nil || len(one) != 1 {
		t.Fatalf("explicit file: got %v, err %v", one, err)
	}
}
