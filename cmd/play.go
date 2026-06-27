package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/msradam/ocarina/internal/interp"
	"github.com/msradam/ocarina/internal/mcpclient"
	"github.com/msradam/ocarina/internal/playbook"
	"github.com/spf13/cobra"
)

var (
	boldCyan   = color.New(color.FgCyan, color.Bold).SprintfFunc()
	green      = color.New(color.FgGreen).SprintfFunc()
	red        = color.New(color.FgRed).SprintfFunc()
	yellowPlay = color.New(color.FgYellow).SprintfFunc()
)

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
		c, err := playbook.Load(args[0])
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
		if err := resolveServer(&c.Server); err != nil {
			return err
		}
		serverArgs := interp.Strings(c.Server.Args, notes)
		serverEnv := interp.StringMap(c.Server.Env, notes)
		sess, err := mcpclient.Connect(ctx, c.Server.Command, serverArgs, serverEnv)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		defer sess.Close()

		dryRun, _ := cmd.Flags().GetBool("dry-run")
		var failures []string

		for i, step := range c.Rondo {
			name := step.Name
			if name == "" {
				name = fmt.Sprintf("step %d", i+1)
			}

			if !matchesTags(step.Tags, onlyTags, skipTags) {
				continue
			}

			items, err := resolveLoop(step.Loop, notes)
			if err != nil {
				return fmt.Errorf("step %q loop: %w", name, err)
			}

			for _, item := range items {
				iterNotes := notes
				if item != "" {
					iterNotes = make(map[string]string, len(notes)+1)
					for k, v := range notes {
						iterNotes[k] = v
					}
					iterNotes["item"] = item
				}

				// sleep-only steps run silently; they exist to pace a demo or add delay
				if step.Sleep != "" && step.Tool == "" && step.Resource == "" && step.ListResources == "" {
					if d, err := time.ParseDuration(step.Sleep); err == nil {
						time.Sleep(d)
					}
					continue
				}

				label := stepLabel(step)
				if item != "" {
					label += fmt.Sprintf(" [%s]", truncate(item, 40))
				}
				fmt.Fprintf(os.Stdout, "%s %s\n", boldCyan("==>"), fmt.Sprintf("%s (%s)", name, label))

				if dryRun {
					fmt.Fprintf(os.Stdout, "    [dry-run] %s\n\n", dryRunDetail(step, iterNotes))
					continue
				}

				output, isToolError, dispatchErr := dispatchStep(ctx, sess, step, iterNotes)
				if dispatchErr != nil {
					msg := fmt.Sprintf("    %s %v\n\n", red("error:"), dispatchErr)
					if step.IgnoreErrors {
						fmt.Fprint(os.Stdout, msg)
						continue
					}
					fmt.Fprint(os.Stderr, msg)
					failures = append(failures, fmt.Sprintf("step %q: %v", name, dispatchErr))
					continue
				}

				captured := output
				if step.Grab != "" {
					extracted, grabErr := interp.Grab(captured, step.Grab)
					if grabErr != nil {
						fmt.Fprintf(os.Stderr, "    %s %v\n", yellowPlay("grab:"), grabErr)
					} else {
						captured = extracted
					}
				}
				// when grab: is set, display the extracted value — not the raw blob
				displayed := output
				if step.Grab != "" {
					displayed = captured
				}
				fmt.Fprintf(os.Stdout, "%s\n", colorOutput(displayed))

				if step.Echo != "" {
					notes[step.Echo] = captured
				}

				if step.Expect != nil {
					fail := checkExpect(step.Expect, output, isToolError, iterNotes)
					if fail != "" {
						fmt.Fprintf(os.Stderr, "    %s %s\n", red("FAIL:"), fail)
						if !step.IgnoreErrors {
							failures = append(failures, fmt.Sprintf("step %q: %s", name, fail))
						}
					}
				}

				fmt.Fprintln(os.Stdout)
			}
		}

		if len(failures) > 0 {
			return fmt.Errorf("%d expectation(s) failed", len(failures))
		}
		return nil
	},
}

// isToolError is true when the server returned isError:true on a tool call.
func dispatchStep(ctx context.Context, sess *mcp.ClientSession, step playbook.Step, notes map[string]string) (output string, isToolError bool, err error) {
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

func callTool(ctx context.Context, sess *mcp.ClientSession, step playbook.Step, notes map[string]string) (string, bool, error) {
	var callArgs map[string]any
	if step.Args != nil {
		callArgs, _ = interp.Apply(step.Args, notes).(map[string]any)
	}
	if callArgs == nil {
		callArgs = map[string]any{}
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
	return strings.Join(parts, "\n"), result.IsError, nil
}

func readResource(ctx context.Context, sess *mcp.ClientSession, step playbook.Step, notes map[string]string) (string, bool, error) {
	uri := interp.Apply(step.Resource, notes).(string)
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

func listResources(ctx context.Context, sess *mcp.ClientSession, _ playbook.Step, _ map[string]string) (string, bool, error) {
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
		return nil, fmt.Errorf("loop value is not a JSON array: %w", err)
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

func checkExpect(e *playbook.Expect, output string, isToolError bool, notes map[string]string) string {
	if e.Contains != "" {
		want := interp.Apply(e.Contains, notes).(string)
		if !strings.Contains(output, want) {
			return fmt.Sprintf("expected output to contain %q", want)
		}
		fmt.Fprintf(os.Stdout, "    %s contains %q\n", green("PASS:"), want)
	}
	if e.Equals != "" {
		want := interp.Apply(e.Equals, notes).(string)
		if strings.TrimSpace(output) != strings.TrimSpace(want) {
			return fmt.Sprintf("expected output to equal %q", want)
		}
		fmt.Fprintf(os.Stdout, "    %s equals %q\n", green("PASS:"), want)
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
		fmt.Fprintf(os.Stdout, "    %s matches %q\n", green("PASS:"), pattern)
	}
	if e.IsError != nil {
		if isToolError != *e.IsError {
			return fmt.Sprintf("expected is_error=%v, got %v", *e.IsError, isToolError)
		}
		fmt.Fprintf(os.Stdout, "    %s is_error=%v\n", green("PASS:"), *e.IsError)
	}
	return ""
}

func stepLabel(t playbook.Step) string {
	switch {
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

func dryRunDetail(t playbook.Step, notes map[string]string) string {
	switch {
	case t.Tool != "":
		return fmt.Sprintf("tool=%s args=%v", t.Tool, t.Args)
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

	// try proper JSON first
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		// try converting Python dict repr: True/False/None and single-quoted strings
		if converted := pyReprToJSON(trimmed); converted != "" {
			if err2 := json.Unmarshal([]byte(converted), &parsed); err2 != nil {
				return s
			}
		} else {
			return s
		}
	}

	return renderColor(parsed, "")
}

// pyReprToJSON converts Python dict/list repr to JSON. Only handles the common
// case where string values contain no embedded single quotes.
func pyReprToJSON(s string) string {
	if len(s) == 0 || (s[0] != '[' && s[0] != '{') {
		return ""
	}
	s = strings.NewReplacer(
		": True", ": true",
		": False", ": false",
		": None", ": null",
		"[True", "[true",
		"[False", "[false",
		"[None", "[null",
	).Replace(s)
	// swap single-quoted strings to double-quoted
	var b strings.Builder
	inSingle := false
	for _, c := range s {
		switch {
		case c == '\'' && !inSingle:
			inSingle = true
			b.WriteRune('"')
		case c == '\'' && inSingle:
			inSingle = false
			b.WriteRune('"')
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
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
	playCmd.Flags().StringArrayP("extra-vars", "e", nil, "override keys: variables (key=value, repeatable)")
	playCmd.Flags().StringArray("tags", nil, "run only steps with these tags (repeatable)")
	playCmd.Flags().StringArray("skip-tags", nil, "skip steps with these tags (repeatable)")
	rootCmd.AddCommand(playCmd)
}
