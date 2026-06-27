package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/fatih/color"
	jschema "github.com/google/jsonschema-go/jsonschema"
	"github.com/msradam/ocarina/internal/condition"
	"github.com/msradam/ocarina/internal/rondo"
	"github.com/spf13/cobra"
)

var templateKeyRe = regexp.MustCompile(`\{\{([\w.]+)\}\}`)

var (
	okLabel   = color.New(color.FgGreen, color.Bold).Sprint("OK")
	errLabel  = color.New(color.FgRed, color.Bold).Sprint("ERROR")
	warnLabel = color.New(color.FgYellow, color.Bold).Sprint("WARN ")
)

var validateCmd = &cobra.Command{
	Use:   "validate <rondo.yaml>",
	Short: "Validate a rondo against the server's tool schemas without running any tools",
	Long: `Connects to the server, fetches tool schemas, and checks every step for:
  - tool exists on the server
  - required args are present
  - arg types match the schema
  - {{key}} references are defined in keys or set by a prior echo:

Exits non-zero if any errors are found.

Example:
  ocarina validate session.yaml
  ocarina validate examples/github-investigation.yaml`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := rondo.Load(args[0])
		if err != nil {
			return fmt.Errorf("load: %w", err)
		}

		ctx := context.Background()
		if len(c.Servers) == 0 {
			return fmt.Errorf("rondo is missing a server: block")
		}

		type schemaEntry struct {
			required   []string
			properties map[string]struct{ typ string }
			raw        []byte
		}
		// schemas[serverKey][toolName]
		schemas := make(map[string]map[string]schemaEntry)
		for key := range referencedServerKeys(c) {
			srv, ok := c.Servers[key]
			if !ok {
				continue // undefined reference; reported per-step below
			}
			sess, err := connectServer(ctx, srv, c.Keys)
			if err != nil {
				return fmt.Errorf("connect %q: %w", key, err)
			}
			defer sess.Close()

			res, err := sess.ListTools(ctx, nil)
			if err != nil {
				return fmt.Errorf("list tools (%s): %w", key, err)
			}

			schemas[key] = make(map[string]schemaEntry)
			for _, t := range res.Tools {
				entry := schemaEntry{}
				if t.InputSchema != nil {
					raw, _ := json.Marshal(t.InputSchema)
					entry.raw = raw
					var s struct {
						Required   []string `json:"required"`
						Properties map[string]struct {
							Type string `json:"type"`
						} `json:"properties"`
					}
					_ = json.Unmarshal(raw, &s)
					entry.required = s.Required
					entry.properties = make(map[string]struct{ typ string }, len(s.Properties))
					for k, p := range s.Properties {
						entry.properties[k] = struct{ typ string }{p.Type}
					}
				}
				schemas[key][t.Name] = entry
			}
		}

		// data-flow: keys available via keys: + prior echo: fields
		available := make(map[string]bool)
		for k := range c.Keys {
			available[k] = true
		}

		var totalErrs, totalWarns int

		for i, step := range c.Steps {
			name := step.Name
			if name == "" {
				name = fmt.Sprintf("step %d", i+1)
			}

			prefix := fmt.Sprintf("  %s (%s)", name, stepLabel(step))

			var errs, warns []string

			// step must declare an action (tool, resource, list_resources, or sleep)
			if step.Tool == "" && step.Resource == "" && step.ListResources == "" && step.Sleep == "" {
				errs = append(errs, "step has no tool, resource, list_resources, or sleep field")
			}

			serverKey := c.StepServerKey(step)
			serverSchemas, serverKnown := schemas[serverKey]
			if !serverKnown {
				errs = append(errs, fmt.Sprintf("references server %q, which is not defined in servers:", serverKey))
			}

			if step.Timeout != "" {
				if _, err := time.ParseDuration(step.Timeout); err != nil {
					errs = append(errs, fmt.Sprintf("invalid timeout %q: %v", step.Timeout, err))
				}
			}

			// loop: injects {{item}} into scope for this step's args
			if step.Loop != "" {
				available["item"] = true
			}

			if step.Tool != "" && serverKnown {
				entry, found := serverSchemas[step.Tool]
				if !found {
					errs = append(errs, fmt.Sprintf("tool %q not found on server %q", step.Tool, serverKey))
				} else {
					for _, req := range entry.required {
						if _, ok := step.Args[req]; !ok {
							errs = append(errs, fmt.Sprintf("missing required arg %q", req))
						}
					}
					for arg := range step.Args {
						if _, ok := entry.properties[arg]; !ok && len(entry.properties) > 0 {
							warns = append(warns, fmt.Sprintf("arg %q not in schema", arg))
						}
					}
					// skip {{key}} strings; JSON Schema can't validate unresolved templates
					if entry.raw != nil {
						filtered := make(map[string]any)
						for k, v := range step.Args {
							if s, ok := v.(string); ok && templateKeyRe.MatchString(s) {
								continue
							}
							filtered[k] = v
						}
						if len(filtered) > 0 {
							var s jschema.Schema
							if err := json.Unmarshal(entry.raw, &s); err == nil {
								s.Required = nil // checked separately; don't fail on missing template args
								if resolved, err := s.Resolve(nil); err == nil {
									if valErr := resolved.Validate(filtered); valErr != nil {
										errs = append(errs, valErr.Error())
									}
								}
							}
						}
					}
				}

				// data-flow: {{key}} references must be available at this point
				for arg, val := range step.Args {
					s, ok := val.(string)
					if !ok {
						continue
					}
					for _, m := range templateKeyRe.FindAllStringSubmatch(s, -1) {
						key := m[1]
						if strings.HasPrefix(key, "env.") {
							continue // {{env.X}} resolves from the process environment at run time
						}
						if !available[key] {
							errs = append(errs, fmt.Sprintf("arg %q: {{%s}} not defined in keys: and no prior step sets it via echo:", arg, key))
						}
					}
				}
			}

			// check {{key}} refs in resource URI
			if step.Resource != "" {
				for _, m := range templateKeyRe.FindAllStringSubmatch(step.Resource, -1) {
					key := m[1]
					if strings.HasPrefix(key, "env.") {
						continue
					}
					if !available[key] {
						errs = append(errs, fmt.Sprintf("resource URI: {{%s}} not defined in keys: and no prior step sets it via echo:", key))
					}
				}
			}

			// list_resources: stores URIs for loop:
			if step.ListResources != "" && step.Echo != "" {
				available[step.Echo] = true
			}

			// CEL syntax checks (parse-only; variables not known at validate time)
			if step.When != "" {
				if synErr := condition.CheckSyntax(step.When); synErr != nil {
					errs = append(errs, fmt.Sprintf("when: %v", synErr))
				}
			}
			if step.Retry != nil && step.Retry.Until != "" {
				if synErr := condition.CheckSyntax(step.Retry.Until); synErr != nil {
					errs = append(errs, fmt.Sprintf("retry.until: %v", synErr))
				}
			}
			if step.Expect != nil && step.Expect.Rule != "" {
				if synErr := condition.CheckSyntax(step.Expect.Rule); synErr != nil {
					errs = append(errs, fmt.Sprintf("expect.rule: %v", synErr))
				}
			}

			if len(errs) == 0 && len(warns) == 0 {
				fmt.Fprintf(os.Stdout, "%s  %s\n", prefix, okLabel)
			} else {
				fmt.Fprintf(os.Stdout, "%s\n", prefix)
				for _, e := range errs {
					fmt.Fprintf(os.Stderr, "    %s %s\n", errLabel, e)
				}
				for _, w := range warns {
					fmt.Fprintf(os.Stderr, "    %s %s\n", warnLabel, w)
				}
			}

			totalErrs += len(errs)
			totalWarns += len(warns)

			if step.Loop != "" {
				delete(available, "item")
			}

			if step.Echo != "" {
				available[step.Echo] = true
			}
		}

		fmt.Fprintln(os.Stdout)
		if totalErrs == 0 && totalWarns == 0 {
			fmt.Fprintf(os.Stdout, "%s\n", color.GreenString("all %d step(s) valid", len(c.Steps)))
			return nil
		}
		fmt.Fprintf(os.Stdout, "%s\n", color.RedString("%d error(s)", totalErrs)+color.YellowString(", %d warning(s)", totalWarns))
		if totalErrs > 0 {
			return fmt.Errorf("%d validation error(s)", totalErrs)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(validateCmd)
}
