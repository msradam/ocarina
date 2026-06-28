package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/msradam/ocarina/internal/interp"
	"github.com/msradam/ocarina/internal/rondo"
)

func TestDispatchToolEcho(t *testing.T) {
	sess := newFakeSession(t)
	step := rondo.Step{Tool: "echo", Args: map[string]any{"text": "hello {{who}}"}}
	out, isErr, err := dispatchStep(context.Background(), sess, step, map[string]string{"who": "world"})
	if err != nil {
		t.Fatal(err)
	}
	if isErr {
		t.Fatal("echo should not be a tool error")
	}
	if out != "hello world" {
		t.Fatalf("got %q, want interpolated %q", out, "hello world")
	}
}

func TestDispatchUnresolvedKeyFails(t *testing.T) {
	sess := newFakeSession(t)
	step := rondo.Step{Tool: "echo", Args: map[string]any{"text": "{{missing}}"}}
	_, _, err := dispatchStep(context.Background(), sess, step, map[string]string{})
	if err == nil {
		t.Fatal("an unresolved {{key}} must fail the step, not pass it through")
	}
}

func TestDispatchPrefersStructuredContent(t *testing.T) {
	sess := newFakeSession(t)
	step := rondo.Step{Tool: "profile"}
	out, _, err := dispatchStep(context.Background(), sess, step, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The tool's text block says "ignore this text"; the engine must read the
	// structured JSON instead so grab/expect operate on the typed value.
	if want := `"name":"ocarina"`; !strings.Contains(out, want) {
		t.Fatalf("expected structured JSON containing %s, got %q", want, out)
	}
	if strings.Contains(out, "ignore this text") {
		t.Fatalf("engine read the text block instead of structuredContent: %q", out)
	}
}

func TestToolErrorReported(t *testing.T) {
	sess := newFakeSession(t)
	out, isErr, err := dispatchStep(context.Background(), sess, rondo.Step{Tool: "boom"}, nil)
	if err != nil {
		t.Fatalf("isError:true is a valid response, not a transport error: %v", err)
	}
	if !isErr {
		t.Fatal("boom must surface isError=true")
	}
	if out != "kaboom" {
		t.Fatalf("got %q, want %q", out, "kaboom")
	}
}

func TestGrabThenEchoDataFlow(t *testing.T) {
	sess := newFakeSession(t)
	notes := map[string]string{}

	// Step 1: grab a field out of structured output and capture it.
	out, _, err := dispatchStep(context.Background(), sess, rondo.Step{Tool: "profile"}, notes)
	if err != nil {
		t.Fatal(err)
	}
	grabbed, err := interp.Grab(out, "name")
	if err != nil {
		t.Fatal(err)
	}
	notes["project"] = grabbed

	// Step 2: the captured value flows into the next step's args.
	out2, _, err := dispatchStep(context.Background(), sess,
		rondo.Step{Tool: "echo", Args: map[string]any{"text": "{{project}}"}}, notes)
	if err != nil {
		t.Fatal(err)
	}
	if out2 != "ocarina" {
		t.Fatalf("captured value did not flow into step 2: got %q", out2)
	}
}

func TestCheckExpect(t *testing.T) {
	cases := []struct {
		name   string
		expect rondo.Expect
		output string
		isErr  bool
		wantOK bool
	}{
		{"contains pass", rondo.Expect{Contains: "ready"}, "ready set go", false, true},
		{"contains fail", rondo.Expect{Contains: "nope"}, "ready set go", false, false},
		{"equals trims", rondo.Expect{Equals: "ready"}, "  ready\n", false, true},
		{"equals fail", rondo.Expect{Equals: "ready"}, "not ready", false, false},
		{"matches pass", rondo.Expect{Matches: `^\d+$`}, "42", false, true},
		{"matches fail", rondo.Expect{Matches: `^\d+$`}, "x42", false, false},
		{"is_error pass", rondo.Expect{IsError: boolPtr(true)}, "kaboom", true, true},
		{"is_error fail", rondo.Expect{IsError: boolPtr(true)}, "fine", false, false},
		{"rule pass", rondo.Expect{Rule: `output == "42"`}, "42", false, true},
		{"rule fail", rondo.Expect{Rule: `output == "42"`}, "41", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fail := checkExpect(&tc.expect, tc.output, tc.isErr, map[string]string{})
			if (fail == "") != tc.wantOK {
				t.Fatalf("wantOK=%v, got failure=%q", tc.wantOK, fail)
			}
		})
	}
}

func TestRetryUntilMatches(t *testing.T) {
	sess := newFakeSession(t)
	// count returns 1, 2, 3...; retry until the output is "3".
	step := rondo.Step{
		Tool:  "count",
		Retry: &rondo.RetryConfig{Retries: 5, Delay: "1ms", Until: `output == "3"`},
	}
	out, _, err := runWithRetry(context.Background(), sess, step, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if out != "3" {
		t.Fatalf("retry stopped at %q, want it to keep going until %q", out, "3")
	}
}

func TestRetryRecoversFromToolError(t *testing.T) {
	sess := newFakeSession(t)
	// flaky fails twice then succeeds; the default retry path keeps going while
	// isError is true and stops once the call is clean.
	step := rondo.Step{Tool: "flaky", Retry: &rondo.RetryConfig{Retries: 5, Delay: "1ms"}}
	out, isErr, err := runWithRetry(context.Background(), sess, step, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if isErr {
		t.Fatal("final attempt should be clean")
	}
	if out != "ready" {
		t.Fatalf("got %q, want %q", out, "ready")
	}
}

func TestRetryExhaustedFails(t *testing.T) {
	sess := newFakeSession(t)
	// boom never recovers; retries exhaust and the step fails.
	step := rondo.Step{Tool: "boom", Retry: &rondo.RetryConfig{Retries: 2, Delay: "1ms", Until: `output == "never"`}}
	_, _, err := runWithRetry(context.Background(), sess, step, map[string]string{})
	if err == nil {
		t.Fatal("exhausted retries must return an error")
	}
}

func TestReadResource(t *testing.T) {
	sess := newFakeSession(t)
	out, _, err := dispatchStep(context.Background(), sess,
		rondo.Step{Resource: "test://greeting"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello resource" {
		t.Fatalf("got %q, want %q", out, "hello resource")
	}
}

func TestListResources(t *testing.T) {
	sess := newFakeSession(t)
	out, _, err := dispatchStep(context.Background(), sess,
		rondo.Step{ListResources: "default"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// list_resources returns a JSON array of URIs for grab/loop to consume.
	if !strings.Contains(out, "test://greeting") {
		t.Fatalf("listed resources missing the registered URI: %q", out)
	}
}

func TestResolveLoop(t *testing.T) {
	items, err := resolveLoop(`["a", "b", "c"]`, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0] != "a" || items[2] != "c" {
		t.Fatalf("got %v, want [a b c]", items)
	}
	if got, _ := resolveLoop("", nil); len(got) != 1 || got[0] != "" {
		t.Fatalf("empty loop should yield one no-op iteration, got %v", got)
	}
	if _, err := resolveLoop("not json", nil); err == nil {
		t.Fatal("a non-array loop value must error")
	}
}

func TestMotifNotes(t *testing.T) {
	// A motif sees its own keys: as defaults, overridden by with: params.
	// It does not inherit parent captures.
	defaults := map[string]string{"token": "default-tok", "region": "us"}
	with := map[string]string{"token": "passed-tok"}
	n := motifNotes(defaults, with)
	if n["token"] != "passed-tok" {
		t.Fatalf("with: should override the default, got %q", n["token"])
	}
	if n["region"] != "us" {
		t.Fatalf("unoverridden default should survive, got %q", n["region"])
	}
	if _, leaked := n["item"]; leaked {
		t.Fatal("motif scope must not contain parent-only keys")
	}
}

func TestRunBlock(t *testing.T) {
	// fakeRun fails any step whose Name is in the failSet, and records the
	// order steps ran so we can assert short-circuit and always: behavior.
	newRun := func(failSet map[string]bool, ran *[]string) func([]rondo.Step) []string {
		return func(sub []rondo.Step) []string {
			var f []string
			for _, s := range sub {
				*ran = append(*ran, s.Name)
				if failSet[s.Name] {
					f = append(f, s.Name+" failed")
				}
			}
			return f
		}
	}
	blk := func() rondo.Step {
		return rondo.Step{
			Block:  []rondo.Step{{Name: "b1"}, {Name: "b2"}},
			Rescue: []rondo.Step{{Name: "r1"}},
			Always: []rondo.Step{{Name: "a1"}},
		}
	}

	t.Run("block succeeds: rescue skipped, always runs", func(t *testing.T) {
		var ran []string
		out := runBlock(blk(), newRun(map[string]bool{}, &ran))
		if len(out) != 0 {
			t.Fatalf("clean block should report no failures, got %v", out)
		}
		want := []string{"b1", "b2", "a1"} // no r1
		if got := join(ran); got != join(want) {
			t.Fatalf("ran %v, want %v (rescue must be skipped)", ran, want)
		}
	})

	t.Run("block fails, rescue recovers: no failures, block short-circuits", func(t *testing.T) {
		var ran []string
		out := runBlock(blk(), newRun(map[string]bool{"b1": true}, &ran))
		if len(out) != 0 {
			t.Fatalf("a clean rescue must recover, got failures %v", out)
		}
		want := []string{"b1", "r1", "a1"} // b2 skipped (block stops at first failure)
		if got := join(ran); got != join(want) {
			t.Fatalf("ran %v, want %v", ran, want)
		}
	})

	t.Run("block fails, rescue also fails: failures propagate", func(t *testing.T) {
		var ran []string
		out := runBlock(blk(), newRun(map[string]bool{"b1": true, "r1": true}, &ran))
		if len(out) != 2 {
			t.Fatalf("block+rescue failures should propagate, got %v", out)
		}
	})

	t.Run("always runs even when block fails with no rescue", func(t *testing.T) {
		var ran []string
		step := rondo.Step{Block: []rondo.Step{{Name: "b1"}}, Always: []rondo.Step{{Name: "a1"}}}
		out := runBlock(step, newRun(map[string]bool{"b1": true}, &ran))
		if len(out) != 1 {
			t.Fatalf("unrescued block failure should propagate, got %v", out)
		}
		if join(ran) != join([]string{"b1", "a1"}) {
			t.Fatalf("always must run regardless, ran %v", ran)
		}
	})

	t.Run("a failing always step propagates even on a clean block", func(t *testing.T) {
		var ran []string
		step := rondo.Step{Block: []rondo.Step{{Name: "b1"}}, Always: []rondo.Step{{Name: "a1"}}}
		out := runBlock(step, newRun(map[string]bool{"a1": true}, &ran))
		if len(out) != 1 {
			t.Fatalf("a failing always: must surface, got %v", out)
		}
	})
}

func join(ss []string) string { return strings.Join(ss, ",") }

func boolPtr(b bool) *bool { return &b }
