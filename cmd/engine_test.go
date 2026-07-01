package cmd

import (
	"context"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/msradam/ocarina/internal/rondo"
)

// TestSetComputesVars proves a set: step computes vars via CEL (including a
// sibling reference, which resolves because keys evaluate in sorted order) and
// that a later step can interpolate the result.
func TestSetComputesVars(t *testing.T) {
	file := &rondo.File{
		Servers:     map[string]rondo.Server{"default": {Command: "fake"}},
		ServerOrder: []string{"default"},
	}
	steps := []rondo.Step{
		{Name: "compute", Set: map[string]string{"city": "'Tokyo'", "zone": "'Asia/' + city"}},
		{Name: "use", Tool: "echo", Args: map[string]any{"text": "{{zone}}"}, Expect: &rondo.Expect{Contains: "Asia/Tokyo"}},
	}

	eng := newEngine(context.Background(), file, map[string]string{})
	eng.sessions["default"] = newFakeSession(t)
	eng.tools["default"] = map[string]toolMeta{"echo": {}}
	notes := map[string]string{}
	if fails := eng.runSteps(steps, notes, ".", 0); len(fails) > 0 {
		t.Fatalf("set/use failed: %v", fails)
	}
	if notes["zone"] != "Asia/Tokyo" {
		t.Fatalf("zone = %q, want Asia/Tokyo", notes["zone"])
	}
}

// TestSnapshotAssertAndUpdate covers the three snapshot paths: a matching
// baseline passes, a mismatching one fails, and --update captures a baseline
// back into the step slice (which play then saves).
func TestSnapshotAssertAndUpdate(t *testing.T) {
	file := &rondo.File{
		Servers:     map[string]rondo.Server{"default": {Command: "fake"}},
		ServerOrder: []string{"default"},
	}
	mk := func() *engine {
		e := newEngine(context.Background(), file, map[string]string{})
		e.sessions["default"] = newFakeSession(t)
		e.tools["default"] = map[string]toolMeta{"echo": {}}
		e.snapshot = true
		return e
	}

	pass := []rondo.Step{{Tool: "echo", Args: map[string]any{"text": "fixed"},
		Result: []rondo.ResultItem{{Type: "text", Text: "fixed"}}}}
	if f := mk().runSteps(pass, map[string]string{}, ".", 0); len(f) > 0 {
		t.Fatalf("matching snapshot should pass, got %v", f)
	}

	fail := []rondo.Step{{Tool: "echo", Args: map[string]any{"text": "fixed"},
		Result: []rondo.ResultItem{{Type: "text", Text: "different"}}}}
	if f := mk().runSteps(fail, map[string]string{}, ".", 0); len(f) == 0 {
		t.Fatal("mismatching snapshot should fail")
	}

	upd := []rondo.Step{{Tool: "echo", Args: map[string]any{"text": "captured"}}}
	e := mk()
	e.update = true
	e.runSteps(upd, map[string]string{}, ".", 0)
	if len(upd[0].Result) != 1 || upd[0].Result[0].Text != "captured" {
		t.Fatalf("update should capture baseline into the step, got %+v", upd[0].Result)
	}

	// --snapshot on a step with no baseline must fail, not pass vacuously.
	nobase := []rondo.Step{{Tool: "echo", Args: map[string]any{"text": "x"}}}
	if f := mk().runSteps(nobase, map[string]string{}, ".", 0); len(f) == 0 {
		t.Fatal("snapshot with no baseline should fail")
	}
}

// TestEngineConcurrentRuns proves serve's execution model is race-free: many
// engines run the same rondo in parallel, each with its own session and notes.
// Run under -race. Sessions are built on the main goroutine because t.Fatal is
// not safe to call from spawned goroutines.
func TestEngineConcurrentRuns(t *testing.T) {
	const n = 40
	sessions := make([]*mcp.ClientSession, n)
	for i := range sessions {
		sessions[i] = newFakeSession(t)
	}

	file := &rondo.File{
		Servers:     map[string]rondo.Server{"default": {Command: "fake"}},
		ServerOrder: []string{"default"},
	}
	steps := []rondo.Step{
		{Tool: "echo", Args: map[string]any{"text": "hi {{who}}"}, Echo: "greeting", Expect: &rondo.Expect{Contains: "hi"}},
		{Tool: "profile", Grab: "name", Expect: &rondo.Expect{Equals: "ocarina"}},
		{Tool: "count", Retry: &rondo.RetryConfig{Retries: 3, Delay: "1ms", Until: `output == "2"`}},
	}

	var wg sync.WaitGroup
	errs := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			eng := newEngine(context.Background(), file, map[string]string{})
			// pre-seed the session so the engine does not try to spawn a process
			eng.sessions["default"] = sessions[i]
			eng.tools["default"] = map[string]toolMeta{"echo": {}, "profile": {}, "count": {}}
			notes := map[string]string{"who": "vu"}
			if fails := eng.runSteps(steps, notes, ".", 0); len(fails) > 0 {
				errs[i] = fails[0]
			}
		}(i)
	}
	wg.Wait()

	for i, e := range errs {
		if e != "" {
			t.Fatalf("goroutine %d failed: %s", i, e)
		}
	}
}
