package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/msradam/ocarina/internal/condition"
	"github.com/msradam/ocarina/internal/interp"
	"github.com/msradam/ocarina/internal/rondo"
)

// engine runs a rondo's steps against one or more MCP servers, reusing a
// session per server. play and serve both drive it. Human progress goes to the
// package-level stdout (point it at io.Discard to silence); errors go to
// stderr. runSteps returns the list of step failures.
type engine struct {
	ctx      context.Context
	file     *rondo.File
	keys     map[string]string // used to interpolate server env/headers on connect
	dryRun   bool
	safe     bool // refuse any tool not marked readOnlyHint, unless allow_destructive
	onlyTags map[string]struct{}
	skipTags map[string]struct{}

	sessions map[string]*mcp.ClientSession
	tools    map[string]map[string]toolMeta // server -> tool -> metadata

	results []stepResult // per-leaf-step outcomes, for the structured report
}

// rec records one leaf step's outcome.
func (e *engine) rec(name, server string, step rondo.Step, status, msg string, start time.Time) {
	e.results = append(e.results, stepResult{
		Name:       name,
		Server:     server,
		Tool:       step.Tool,
		Resource:   step.Resource,
		Status:     status,
		Message:    msg,
		DurationMS: time.Since(start).Milliseconds(),
	})
}

// toolMeta is what the static pre-call check needs from a tool's schema.
type toolMeta struct {
	required []string
	readOnly bool // ReadOnlyHint == true; safe to run under --safe
}

func newEngine(ctx context.Context, f *rondo.File, keys map[string]string) *engine {
	return &engine{
		ctx:      ctx,
		file:     f,
		keys:     keys,
		sessions: make(map[string]*mcp.ClientSession),
		tools:    make(map[string]map[string]toolMeta),
	}
}

func (e *engine) close() {
	for _, s := range e.sessions {
		s.Close()
	}
}

// session connects to the named server once and caches the session along with
// each tool's required-args list for the static pre-call check.
func (e *engine) session(key string) (*mcp.ClientSession, error) {
	if s, ok := e.sessions[key]; ok {
		return s, nil
	}
	srv, ok := e.file.Servers[key]
	if !ok {
		return nil, fmt.Errorf("step references server %q, which is not defined in the servers map", key)
	}
	s, err := connectServer(e.ctx, srv, e.keys)
	if err != nil {
		return nil, fmt.Errorf("connect %q: %w", key, err)
	}
	e.sessions[key] = s
	if toolsList, lerr := listAllTools(e.ctx, s); lerr == nil {
		tools := make(map[string]toolMeta, len(toolsList))
		for _, t := range toolsList {
			var meta toolMeta
			if t.InputSchema != nil {
				raw, _ := json.Marshal(t.InputSchema)
				var sc struct {
					Required []string `json:"required"`
				}
				_ = json.Unmarshal(raw, &sc)
				meta.required = sc.Required
			}
			meta.readOnly = t.Annotations != nil && t.Annotations.ReadOnlyHint
			tools[t.Name] = meta
		}
		e.tools[key] = tools
	}
	return s, nil
}

// runSteps executes a step list and returns its failures. It recurses for
// motif: includes (isolated scope, depth-guarded) and block/rescue/always.
func (e *engine) runSteps(steps []rondo.Step, notes map[string]string, dir string, depth int) []string {
	var fails []string
	for i, step := range steps {
		name := step.Name
		if name == "" {
			name = fmt.Sprintf("step %d", i+1)
		}

		if !matchesTags(step.Tags, e.onlyTags, e.skipTags) {
			continue
		}

		if step.Motif != "" {
			if step.When != "" {
				ok, evalErr := condition.EvalBool(step.When, notes, "")
				if evalErr != nil {
					fmt.Fprintf(os.Stderr, "    %s when: %v\n\n", red("error:"), evalErr)
					fails = append(fails, fmt.Sprintf("step %q when: %v", name, evalErr))
					continue
				}
				if !ok {
					fmt.Fprintf(stdout, "%s %s\n    %s\n\n", boldCyan("==>"), name, yellowPlay("skipped"))
					continue
				}
			}
			if depth >= maxMotifDepth {
				fmt.Fprintf(os.Stderr, "    %s motif nesting exceeds %d levels (cycle?)\n\n", red("error:"), maxMotifDepth)
				fails = append(fails, fmt.Sprintf("step %q: motif nesting too deep", name))
				continue
			}
			path := step.Motif
			if !filepath.IsAbs(path) {
				path = filepath.Join(dir, path)
			}
			mf, mErr := rondo.Load(path)
			if mErr != nil {
				fmt.Fprintf(os.Stderr, "    %s motif %s: %v\n\n", red("error:"), step.Motif, mErr)
				fails = append(fails, fmt.Sprintf("step %q: motif %s: %v", name, step.Motif, mErr))
				continue
			}
			fmt.Fprintf(stdout, "%s %s (motif %s)\n\n", boldCyan("==>"), name, step.Motif)
			fails = append(fails, e.runSteps(mf.Steps, motifNotes(mf.Keys, interp.StringMap(step.With, notes)), filepath.Dir(path), depth+1)...)
			continue
		}

		if len(step.Block) > 0 || len(step.Rescue) > 0 || len(step.Always) > 0 {
			if step.When != "" {
				ok, evalErr := condition.EvalBool(step.When, notes, "")
				if evalErr != nil {
					fmt.Fprintf(os.Stderr, "    %s when: %v\n\n", red("error:"), evalErr)
					fails = append(fails, fmt.Sprintf("step %q when: %v", name, evalErr))
					continue
				}
				if !ok {
					fmt.Fprintf(stdout, "%s %s\n    %s\n\n", boldCyan("==>"), name, yellowPlay("skipped"))
					continue
				}
			}
			if step.Name != "" {
				fmt.Fprintf(stdout, "%s %s (block)\n\n", boldCyan("==>"), name)
			}
			run := func(sub []rondo.Step) []string {
				return e.runSteps(sub, notes, dir, depth)
			}
			fails = append(fails, runBlock(step, run)...)
			continue
		}

		items, err := resolveLoop(step.Loop, notes)
		if err != nil {
			if step.IgnoreErrors {
				fmt.Fprintf(stdout, "%s %s\n    %s %v\n\n", boldCyan("==>"), name, red("error:"), err)
				continue
			}
			fmt.Fprintf(os.Stderr, "%s %s\n    %s %v\n\n", boldCyan("==>"), name, red("error:"), err)
			fails = append(fails, fmt.Sprintf("step %q loop: %v", name, err))
			continue
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

			dispServer := e.file.StepServerKey(step)
			label := stepLabel(step)
			if e.file.MultiServer() && (step.Tool != "" || step.Resource != "" || step.ListResources != "") {
				label = dispServer + "." + label
			}
			if item != "" {
				label += fmt.Sprintf(" [%s]", truncate(item, 40))
			}
			fmt.Fprintf(stdout, "%s %s\n", boldCyan("==>"), fmt.Sprintf("%s (%s)", name, label))
			start := time.Now()

			if step.When != "" {
				ok, evalErr := condition.EvalBool(step.When, iterNotes, "")
				if evalErr != nil {
					fmt.Fprintf(os.Stderr, "    %s when: %v\n\n", red("error:"), evalErr)
					fails = append(fails, fmt.Sprintf("step %q when: %v", name, evalErr))
					e.rec(name, dispServer, step, "failed", "when: "+evalErr.Error(), start)
					continue
				}
				if !ok {
					fmt.Fprintf(stdout, "    %s\n\n", yellowPlay("skipped"))
					e.rec(name, dispServer, step, "skipped", "", start)
					continue
				}
			}

			if e.dryRun {
				fmt.Fprintf(stdout, "    [dry-run] %s\n\n", dryRunDetail(step, iterNotes))
				e.rec(name, dispServer, step, "skipped", "dry-run", start)
				continue
			}

			stepCtx := e.ctx
			var cancelFn context.CancelFunc
			if step.Timeout != "" {
				d, parseErr := time.ParseDuration(step.Timeout)
				if parseErr != nil {
					fmt.Fprintf(os.Stderr, "    %s invalid timeout %q: %v\n\n", red("error:"), step.Timeout, parseErr)
					fails = append(fails, fmt.Sprintf("step %q: invalid timeout %q", name, step.Timeout))
					e.rec(name, dispServer, step, "failed", "invalid timeout", start)
					continue
				}
				stepCtx, cancelFn = context.WithTimeout(e.ctx, d)
			}

			sess, sessErr := e.session(dispServer)
			if sessErr != nil {
				if cancelFn != nil {
					cancelFn()
				}
				fmt.Fprintf(os.Stderr, "    %s %v\n\n", red("error:"), sessErr)
				fails = append(fails, fmt.Sprintf("step %q: %v", name, sessErr))
				e.rec(name, dispServer, step, "failed", sessErr.Error(), start)
				continue
			}

			// Static check against the live schema: a typo'd tool or a missing
			// required arg is a deterministic failure, not a green run. In --safe
			// mode also refuse any tool not marked read-only.
			if tools, ok := e.tools[dispServer]; ok && step.Tool != "" {
				meta, found := tools[step.Tool]
				var schemaErr string
				switch {
				case !found:
					schemaErr = fmt.Sprintf("tool %q not found on server %q", step.Tool, dispServer)
				case e.safe && !meta.readOnly && !step.AllowDestructive:
					schemaErr = fmt.Sprintf("tool %q is not marked read-only; refused in --safe mode (set allow_destructive: true to override)", step.Tool)
				default:
					for _, r := range meta.required {
						if _, present := step.Args[r]; !present {
							schemaErr = fmt.Sprintf("missing required arg %q for tool %q", r, step.Tool)
							break
						}
					}
				}
				if schemaErr != "" {
					if cancelFn != nil {
						cancelFn()
					}
					e.rec(name, dispServer, step, "failed", schemaErr, start)
					if step.IgnoreErrors {
						fmt.Fprintf(stdout, "    %s %s\n\n", red("error:"), schemaErr)
						continue
					}
					fmt.Fprintf(os.Stderr, "    %s %s\n\n", red("error:"), schemaErr)
					fails = append(fails, fmt.Sprintf("step %q: %s", name, schemaErr))
					continue
				}
			}

			output, isToolError, dispatchErr := runWithRetry(stepCtx, sess, step, iterNotes)
			if cancelFn != nil {
				cancelFn()
			}
			if dispatchErr != nil {
				msg := fmt.Sprintf("    %s %v\n\n", red("error:"), dispatchErr)
				e.rec(name, dispServer, step, "failed", dispatchErr.Error(), start)
				if step.IgnoreErrors {
					fmt.Fprint(stdout, msg)
					continue
				}
				fmt.Fprint(os.Stderr, msg)
				fails = append(fails, fmt.Sprintf("step %q: %v", name, dispatchErr))
				continue
			}

			// Tool-level errors (isError:true) are failures by default.
			// Opt out with ignore_errors: true or expect: is_error: true.
			if isToolError && !step.IgnoreErrors {
				expectsError := step.Expect != nil && step.Expect.IsError != nil && *step.Expect.IsError
				if !expectsError {
					fmt.Fprintf(os.Stderr, "    %s %s\n\n", red("error:"), truncate(output, 200))
					fails = append(fails, fmt.Sprintf("step %q: tool returned an error", name))
					e.rec(name, dispServer, step, "failed", "tool returned an error", start)
					continue
				}
			}

			captured := output
			if step.Grab != "" {
				extracted, grabErr := interp.Grab(captured, step.Grab)
				if grabErr != nil {
					if !step.IgnoreErrors {
						fmt.Fprintf(os.Stderr, "    %s %v\n\n", red("error:"), grabErr)
						fails = append(fails, fmt.Sprintf("step %q: %v", name, grabErr))
						e.rec(name, dispServer, step, "failed", grabErr.Error(), start)
						continue
					}
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
			fmt.Fprintf(stdout, "%s\n", colorOutput(displayed))

			if step.Echo != "" {
				notes[step.Echo] = captured
			}

			status, statusMsg := "ok", ""
			if step.Expect != nil {
				if fail := checkExpect(step.Expect, captured, isToolError, iterNotes); fail != "" {
					fmt.Fprintf(os.Stderr, "    %s %s\n", red("FAIL:"), fail)
					status, statusMsg = "failed", fail
					if !step.IgnoreErrors {
						fails = append(fails, fmt.Sprintf("step %q: %s", name, fail))
					}
				}
			}
			e.rec(name, dispServer, step, status, statusMsg, start)

			fmt.Fprintln(stdout)
		}
	}
	return fails
}
