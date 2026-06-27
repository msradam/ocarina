package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"

	"github.com/fatih/color"
	jschema "github.com/google/jsonschema-go/jsonschema"
	"github.com/msradam/ocarina/internal/condition"
	"github.com/msradam/ocarina/internal/interp"
	"github.com/msradam/ocarina/internal/mcpclient"
	"github.com/msradam/ocarina/internal/rondo"
	"github.com/spf13/cobra"
)

var templateKeyRe = regexp.MustCompile(`\{\{(\w+)\}\}`)

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
		if err := resolveServer(&c.Server); err != nil {
			return err
		}
		serverArgs := interp.Strings(c.Server.Args, c.Keys)
		serverEnv := interp.StringMap(c.Server.Env, c.Keys)
		sess, err := mcpclient.Connect(ctx, c.Server.Command, serverArgs, serverEnv)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		defer sess.Close()

		res, err := sess.ListTools(ctx, nil)
		if err != nil {
			return fmt.Errorf("list tools: %w", err)
		}

		type schemaEntry struct {
			required   []string
			properties map[string]struct{ typ string }
			raw        []byte
		}
		schemas := make(map[string]schemaEntry)
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
			schemas[t.Name] = entry
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

			if step.Tool != "" {
				entry, found := schemas[step.Tool]
				if !found {
					errs = append(errs, fmt.Sprintf("tool %q not found on server", step.Tool))
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
						if !available[key] {
							warns = append(warns, fmt.Sprintf("arg %q: {{%s}} not in keys and no prior step sets it via echo:", arg, key))
						}
					}
				}
			}

			// check {{key}} refs in resource URI
			if step.Resource != "" {
				for _, m := range templateKeyRe.FindAllStringSubmatch(step.Resource, -1) {
					key := m[1]
					if !available[key] {
						warns = append(warns, fmt.Sprintf("resource URI: {{%s}} not defined", key))
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
