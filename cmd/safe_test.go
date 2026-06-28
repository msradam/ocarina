package cmd

import (
	"context"
	"testing"

	"github.com/msradam/ocarina/internal/rondo"
)

// safeEngine wires the engine to a live fake session with annotations captured,
// so the --safe gate runs against real ReadOnlyHint metadata.
func safeEngine(t *testing.T) *engine {
	t.Helper()
	sess := newFakeSession(t)
	file := &rondo.File{
		Servers:     map[string]rondo.Server{"default": {Command: "fake"}},
		ServerOrder: []string{"default"},
	}
	eng := newEngine(context.Background(), file, map[string]string{})
	eng.safe = true
	eng.sessions["default"] = sess
	// peek is read-only; echo is not (no annotations).
	eng.tools["default"] = map[string]toolMeta{
		"peek": {readOnly: true},
		"echo": {readOnly: false},
	}
	return eng
}

func TestSafeAllowsReadOnly(t *testing.T) {
	eng := safeEngine(t)
	fails := eng.runSteps([]rondo.Step{{Tool: "peek"}}, map[string]string{}, ".", 0)
	if len(fails) != 0 {
		t.Fatalf("read-only tool must run under --safe, got %v", fails)
	}
}

func TestSafeRefusesNonReadOnly(t *testing.T) {
	eng := safeEngine(t)
	fails := eng.runSteps([]rondo.Step{{Tool: "echo", Args: map[string]any{"text": "x"}}}, map[string]string{}, ".", 0)
	if len(fails) != 1 {
		t.Fatalf("non-read-only tool must be refused under --safe, got %v", fails)
	}
}

func TestSafeAllowDestructiveOverride(t *testing.T) {
	eng := safeEngine(t)
	step := rondo.Step{Tool: "echo", Args: map[string]any{"text": "x"}, AllowDestructive: true}
	fails := eng.runSteps([]rondo.Step{step}, map[string]string{}, ".", 0)
	if len(fails) != 0 {
		t.Fatalf("allow_destructive must override --safe, got %v", fails)
	}
}

func TestUnsafeRunsEverything(t *testing.T) {
	eng := safeEngine(t)
	eng.safe = false
	fails := eng.runSteps([]rondo.Step{{Tool: "echo", Args: map[string]any{"text": "x"}}}, map[string]string{}, ".", 0)
	if len(fails) != 0 {
		t.Fatalf("without --safe, any tool runs, got %v", fails)
	}
}
