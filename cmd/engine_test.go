package cmd

import (
	"context"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/msradam/ocarina/internal/rondo"
)

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
			eng.toolReq["default"] = map[string][]string{"echo": nil, "profile": nil, "count": nil}
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
