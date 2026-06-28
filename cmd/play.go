package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/msradam/ocarina/internal/condition"
	"github.com/msradam/ocarina/internal/interp"
	"github.com/msradam/ocarina/internal/mcpclient"
	"github.com/msradam/ocarina/internal/rondo"
	"github.com/spf13/cobra"
)

var (
	boldCyan   = color.New(color.FgCyan, color.Bold).SprintfFunc()
	green      = color.New(color.FgGreen).SprintfFunc()
	red        = color.New(color.FgRed).SprintfFunc()
	yellowPlay = color.New(color.FgYellow).SprintfFunc()
)

// stdout receives the human progress output. --output json points it at
// io.Discard so stdout carries only the machine-readable report.
var stdout io.Writer = os.Stdout

var playCmd = &cobra.Command{
	Use:   "play <rondo.yaml>",
	Short: "Play a rondo against an MCP server",
	Long: `Executes each step in the rondo by calling the specified tool or reading the
specified resource. No LLM involved — purely deterministic execution.

keys: values are interpolated as {{key}} throughout all step args and URIs.
echo: captures a step's output into a key for use in later steps.
loop: iterates over a JSON array, setting {{item}} for each iteration.

Example:
  ocarina play audit.yaml
  ocarina play audit.yaml --dry-run`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := rondo.Load(args[0])
		if err != nil {
			return fmt.Errorf("load rondo: %w", err)
		}

		notes := make(map[string]string, len(c.Keys))
		for k, v := range c.Keys {
			notes[k] = v
		}
		// -e key=value overrides rondo keys
		for _, kv := range mustStringArray(cmd, "extra-vars") {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return fmt.Errorf("-e %q: expected key=value", kv)
			}
			notes[k] = v
		}

		onlyTags := tagSet(mustStringArray(cmd, "tags"))
		skipTags := tagSet(mustStringArray(cmd, "skip-tags"))

		ctx := context.Background()
		if len(c.Servers) == 0 {
			return fmt.Errorf("rondo is missing a server: block")
		}

		outputJSON := false
		if o, _ := cmd.Flags().GetString("output"); o == "json" {
			outputJSON = true
			stdout = io.Discard
		}
		if t, _ := cmd.Flags().GetBool("trace"); t {
			mcpclient.TraceWriter = os.Stderr
		}

		eng := newEngine(ctx, c, notes)
		defer eng.close()
		eng.dryRun, _ = cmd.Flags().GetBool("dry-run")
		eng.safe, _ = cmd.Flags().GetBool("safe")
		eng.onlyTags = onlyTags
		eng.skipTags = skipTags

		start := time.Now()
		failures := eng.runSteps(c.Steps, notes, filepath.Dir(args[0]), 0)
		result := summarize(eng.results, failures, time.Since(start))
		exportOTLP(filepath.Base(args[0]), result, start)

		if outputJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(result)
		} else {
			tally := fmt.Sprintf("%d passed, %d failed, %d skipped in %s",
				result.Passed, result.Failed, result.Skipped, time.Since(start).Round(time.Millisecond))
			if result.Ok {
				fmt.Fprintf(stdout, "%s\n", green("%s", tally))
			} else {
				fmt.Fprintf(stdout, "%s\n", red("%s", tally))
			}
		}
		if len(failures) > 0 {
			return fmt.Errorf("%d step(s) failed", len(failures))
		}
		return nil
	},
}

// runWithRetry wraps dispatchStep with Ansible-faithful retry/until semantics.
// When retry: is nil, it delegates directly with no overhead.
func runWithRetry(ctx context.Context, sess *mcp.ClientSession, step rondo.Step, notes map[string]string) (string, bool, error) {
	r := step.Retry
	if r == nil {
		return dispatchStep(ctx, sess, step, notes)
	}

	totalAttempts := 1
	if r.Retries > 0 {
		totalAttempts = 1 + r.Retries
	} else if r.Until != "" {
		totalAttempts = 4 // Ansible default when until: is set but retries: is omitted
	}

	delay := 5 * time.Second
	if r.Delay != "" {
		if d, err := time.ParseDuration(r.Delay); err == nil && d > 0 {
			delay = d
		}
	}

	var lastOutput string
	var lastIsErr bool
	var lastErr error

	for attempt := 1; attempt <= totalAttempts; attempt++ {
		lastOutput, lastIsErr, lastErr = dispatchStep(ctx, sess, step, notes)

		if r.Until != "" {
			passed, evalErr := condition.EvalBool(r.Until, notes, lastOutput)
			if evalErr != nil {
				return lastOutput, lastIsErr, fmt.Errorf("retry until: %w", evalErr)
			}
			if passed {
				return lastOutput, lastIsErr, lastErr
			}
		} else if lastErr == nil && !lastIsErr {
			return lastOutput, lastIsErr, lastErr
		}

		if attempt < totalAttempts {
			fmt.Fprintf(os.Stderr, "    retrying (%d/%d in %s)...\n", attempt+1, totalAttempts, delay)
			select {
			case <-ctx.Done():
				return lastOutput, lastIsErr, ctx.Err()
			case <-time.After(delay):
			}
		}
	}

	if lastErr == nil {
		if r.Until != "" {
			lastErr = fmt.Errorf("retries exhausted: %q never matched", r.Until)
		} else {
			lastErr = fmt.Errorf("retries exhausted after %d attempt(s)", totalAttempts)
		}
	}
	return lastOutput, lastIsErr, lastErr
}

// isToolError is true when the server returned isError:true on a tool call.
func dispatchStep(ctx context.Context, sess *mcp.ClientSession, step rondo.Step, notes map[string]string) (output string, isToolError bool, err error) {
	switch {
	case step.Tool != "":
		return callTool(ctx, sess, step, notes)
	case step.Resource != "":
		return readResource(ctx, sess, step, notes)
	case step.ListResources != "":
		return listResources(ctx, sess, step, notes)
	default:
		return "", false, fmt.Errorf("step has no tool, resource, list_resources, or sleep field")
	}
}

func callTool(ctx context.Context, sess *mcp.ClientSession, step rondo.Step, notes map[string]string) (string, bool, error) {
	var callArgs map[string]any
	if step.Args != nil {
		callArgs, _ = interp.Apply(step.Args, notes).(map[string]any)
	}
	if callArgs == nil {
		callArgs = map[string]any{}
	}
	if leftover := interp.Unresolved(callArgs); len(leftover) > 0 {
		return "", false, fmt.Errorf("unresolved %s, not defined in keys or set by a prior echo", strings.Join(leftover, ", "))
	}

	result, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      step.Tool,
		Arguments: callArgs,
	})
	if err != nil {
		return "", false, err
	}

	var parts []string
	for _, content := range result.Content {
		switch v := content.(type) {
		case *mcp.TextContent:
			parts = append(parts, v.Text)
		case *mcp.ImageContent:
			parts = append(parts, fmt.Sprintf("[image %s, %d bytes]", v.MIMEType, len(v.Data)))
		case *mcp.ResourceLink:
			parts = append(parts, fmt.Sprintf("[resource %s]", v.URI))
		case *mcp.EmbeddedResource:
			if v.Resource != nil && v.Resource.Text != "" {
				parts = append(parts, v.Resource.Text)
			} else {
				parts = append(parts, "[embedded resource]")
			}
		default:
			parts = append(parts, fmt.Sprintf("[%T]", content))
		}
	}
	// Prefer structured output when the server provides it: grab/echo/expect
	// then operate on typed JSON instead of parsing the text block.
	if result.StructuredContent != nil {
		if b, err := json.Marshal(result.StructuredContent); err == nil {
			return string(b), result.IsError, nil
		}
	}
	return strings.Join(parts, "\n"), result.IsError, nil
}

func readResource(ctx context.Context, sess *mcp.ClientSession, step rondo.Step, notes map[string]string) (string, bool, error) {
	uri := interp.Apply(step.Resource, notes).(string)
	if leftover := interp.Unresolved(uri); len(leftover) > 0 {
		return "", false, fmt.Errorf("unresolved %s, not defined in keys or set by a prior echo", strings.Join(leftover, ", "))
	}
	result, err := sess.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		return "", false, err
	}
	var parts []string
	for _, rc := range result.Contents {
		if rc.Text != "" {
			parts = append(parts, rc.Text)
		} else if len(rc.Blob) > 0 {
			parts = append(parts, fmt.Sprintf("[blob %s, %d bytes]", rc.MIMEType, len(rc.Blob)))
		}
	}
	return strings.Join(parts, "\n"), false, nil
}

func listResources(ctx context.Context, sess *mcp.ClientSession, _ rondo.Step, _ map[string]string) (string, bool, error) {
	res, err := sess.ListResources(ctx, nil)
	if err != nil {
		return "", false, err
	}

	var uris []string
	for _, r := range res.Resources {
		uris = append(uris, r.URI)
	}
	for res.NextCursor != "" {
		res, err = sess.ListResources(ctx, &mcp.ListResourcesParams{Cursor: res.NextCursor})
		if err != nil {
			break
		}
		for _, r := range res.Resources {
			uris = append(uris, r.URI)
		}
	}

	// Return a JSON array of URI strings. grab: and echo: operate on this;
	// loop: over the echo'd key iterates one URI per {{item}}.
	out, _ := json.Marshal(uris)
	return string(out), false, nil
}

// If loop is empty, returns a single empty string (one iteration, no {{item}}).
func resolveLoop(loop string, notes map[string]string) ([]string, error) {
	if loop == "" {
		return []string{""}, nil
	}
	resolved := interp.Apply(loop, notes).(string)
	var arr []any
	if err := json.Unmarshal([]byte(resolved), &arr); err != nil {
		return nil, fmt.Errorf("loop must be a JSON array (e.g. '[\"UTC\", \"Tokyo\"]'), got: %s", truncate(resolved, 60))
	}
	items := make([]string, len(arr))
	for i, v := range arr {
		switch s := v.(type) {
		case string:
			items[i] = s
		default:
			b, _ := json.Marshal(v)
			items[i] = string(b)
		}
	}
	return items, nil
}

func checkExpect(e *rondo.Expect, output string, isToolError bool, notes map[string]string) string {
	if e.Contains != "" {
		want := interp.Apply(e.Contains, notes).(string)
		if !strings.Contains(output, want) {
			return fmt.Sprintf("expected output to contain %q", want)
		}
		fmt.Fprintf(stdout, "    %s contains %q\n", green("PASS:"), want)
	}
	if e.Equals != "" {
		want := interp.Apply(e.Equals, notes).(string)
		if strings.TrimSpace(output) != strings.TrimSpace(want) {
			return fmt.Sprintf("expected output to equal %q", want)
		}
		fmt.Fprintf(stdout, "    %s equals %q\n", green("PASS:"), want)
	}
	if e.Matches != "" {
		pattern := interp.Apply(e.Matches, notes).(string)
		re, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Sprintf("invalid regex %q: %v", pattern, err)
		}
		if !re.MatchString(output) {
			return fmt.Sprintf("expected output to match %q", pattern)
		}
		fmt.Fprintf(stdout, "    %s matches %q\n", green("PASS:"), pattern)
	}
	if e.IsError != nil {
		if isToolError != *e.IsError {
			return fmt.Sprintf("expected is_error=%v, got %v", *e.IsError, isToolError)
		}
		fmt.Fprintf(stdout, "    %s is_error=%v\n", green("PASS:"), *e.IsError)
	}
	if e.Rule != "" {
		passed, evalErr := condition.EvalBool(e.Rule, notes, output)
		if evalErr != nil {
			return fmt.Sprintf("expect.rule eval: %v", evalErr)
		}
		if !passed {
			msg := e.Message
			if msg == "" {
				msg = fmt.Sprintf("rule %q was false", e.Rule)
			}
			return msg
		}
		fmt.Fprintf(stdout, "    %s rule %q\n", green("PASS:"), e.Rule)
	}
	return ""
}

const maxMotifDepth = 20

// motifNotes builds a motif's isolated variable scope: its own keys: as
// defaults, overlaid by the with: parameters the caller passed (already
// interpolated in the caller's scope). A motif does not inherit the parent's
// captures; anything it needs is threaded in explicitly through with:.
func motifNotes(defaults, with map[string]string) map[string]string {
	n := make(map[string]string, len(defaults)+len(with))
	for k, v := range defaults {
		n[k] = v
	}
	for k, v := range with {
		n[k] = v
	}
	return n
}

// runBlock executes a block/rescue/always step with try-catch semantics: run
// the block steps until one fails; on failure run rescue (a clean rescue
// recovers, dropping the block's failures); always run the always steps
// regardless. run executes a sub-list and returns its failures.
func runBlock(step rondo.Step, run func([]rondo.Step) []string) []string {
	var blockFails []string
	for _, bs := range step.Block {
		if f := run([]rondo.Step{bs}); len(f) > 0 {
			blockFails = f
			break // stop the block at the first failure, like Ansible
		}
	}

	var out []string
	if len(blockFails) > 0 {
		if len(step.Rescue) > 0 {
			if rf := run(step.Rescue); len(rf) > 0 {
				out = append(out, blockFails...)
				out = append(out, rf...)
			}
			// a clean rescue recovers: the block's failures are dropped
		} else {
			out = append(out, blockFails...)
		}
	}
	if len(step.Always) > 0 {
		out = append(out, run(step.Always)...)
	}
	return out
}

func stepLabel(t rondo.Step) string {
	switch {
	case t.Motif != "":
		return "motif:" + t.Motif
	case len(t.Block) > 0 || len(t.Rescue) > 0 || len(t.Always) > 0:
		return "block"
	case t.Tool != "":
		return t.Tool
	case t.Resource != "":
		return "resource:" + t.Resource
	case t.ListResources != "":
		return "list_resources"
	case t.Sleep != "":
		return "sleep:" + t.Sleep
	default:
		return "?"
	}
}

func dryRunDetail(t rondo.Step, notes map[string]string) string {
	switch {
	case t.Tool != "":
		resolved, _ := interp.Apply(t.Args, notes).(map[string]any)
		args, _ := json.Marshal(resolved)
		return fmt.Sprintf("tool=%s args=%s", t.Tool, args)
	case t.Resource != "":
		return fmt.Sprintf("resource=%s", interp.Apply(t.Resource, notes).(string))
	case t.ListResources != "":
		return "list_resources"
	case t.Sleep != "":
		return "sleep=" + t.Sleep
	default:
		return "?"
	}
}

func tagSet(tags []string) map[string]struct{} {
	if len(tags) == 0 {
		return nil
	}
	m := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		m[t] = struct{}{}
	}
	return m
}

func matchesTags(stepTags []string, only, skip map[string]struct{}) bool {
	if len(skip) > 0 {
		for _, t := range stepTags {
			if _, ok := skip[t]; ok {
				return false
			}
		}
	}
	if len(only) == 0 {
		return true
	}
	for _, t := range stepTags {
		if _, ok := only[t]; ok {
			return true
		}
	}
	return false
}

func mustStringArray(cmd *cobra.Command, name string) []string {
	v, _ := cmd.Flags().GetStringArray(name)
	return v
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

var (
	jsonKeyColor = color.New(color.FgCyan).SprintFunc()
	jsonStrColor = color.New(color.FgGreen).SprintFunc()
	jsonNumColor = color.New(color.FgYellow).SprintFunc()
	jsonKwColor  = color.New(color.FgMagenta).SprintFunc()
)

// colorOutput pretty-prints and syntax-highlights JSON output.
// Also handles Python dict repr (e.g. mcp-server-sqlite returns str() of Python lists).
func colorOutput(s string) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) == 0 {
		return s
	}

	var parsed any

	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		// try converting Python dict repr: True/False/None and single-quoted strings
		if converted := interp.PyReprToJSON(trimmed); converted != "" {
			if err2 := json.Unmarshal([]byte(converted), &parsed); err2 != nil {
				return s
			}
		} else {
			return s
		}
	}

	return renderColor(parsed, "")
}

func renderColor(v any, indent string) string {
	next := indent + "  "
	switch val := v.(type) {
	case map[string]any:
		if len(val) == 0 {
			return "{}"
		}
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		lines := make([]string, 0, len(keys))
		for _, k := range keys {
			lines = append(lines, next+jsonKeyColor(`"`+k+`"`)+": "+renderColor(val[k], next))
		}
		return "{\n" + strings.Join(lines, ",\n") + "\n" + indent + "}"
	case []any:
		if len(val) == 0 {
			return "[]"
		}
		items := make([]string, len(val))
		for i, v2 := range val {
			items[i] = next + renderColor(v2, next)
		}
		return "[\n" + strings.Join(items, ",\n") + "\n" + indent + "]"
	case string:
		return jsonStrColor(`"` + val + `"`)
	case float64:
		if val == float64(int64(val)) {
			return jsonNumColor(strconv.FormatInt(int64(val), 10))
		}
		return jsonNumColor(strconv.FormatFloat(val, 'f', -1, 64))
	case bool:
		if val {
			return jsonKwColor("true")
		}
		return jsonKwColor("false")
	case nil:
		return jsonKwColor("null")
	default:
		return fmt.Sprintf("%v", val)
	}
}

func init() {
	playCmd.Flags().Bool("dry-run", false, "print steps without executing them")
	playCmd.Flags().Bool("safe", false, "refuse any tool not marked read-only (override per step with allow_destructive: true)")
	playCmd.Flags().String("output", "text", "output format: text or json")
	playCmd.Flags().Bool("trace", false, "log every JSON-RPC frame to stderr")
	playCmd.Flags().StringArrayP("extra-vars", "e", nil, "override keys: variables (key=value, repeatable)")
	playCmd.Flags().StringArray("tags", nil, "run only steps with these tags (repeatable)")
	playCmd.Flags().StringArray("skip-tags", nil, "skip steps with these tags (repeatable)")
	rootCmd.AddCommand(playCmd)
}
