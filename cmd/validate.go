package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"

	"github.com/msradam/ocarina/internal/interp"
	"github.com/msradam/ocarina/internal/mcpclient"
	"github.com/msradam/ocarina/internal/playbook"
	"github.com/spf13/cobra"
)

var templateKeyRe = regexp.MustCompile(`\{\{(\w+)\}\}`)

type toolSchema struct {
	Required   []string              `json:"required"`
	Properties map[string]schemaProp `json:"properties"`
}

type schemaProp struct {
	Type string `json:"type"`
}

var validateCmd = &cobra.Command{
	Use:   "validate <cassette.yaml>",
	Short: "Validate a cassette against the server's tool schemas without running any tools",
	Long: `Connects to the server, fetches tool schemas, and checks every track for:
  - tool exists on the server
  - required args are present
  - arg types match the schema
  - {{key}} references are defined in notes or set by a prior echo:

Exits non-zero if any errors are found.

Example:
  ocarina validate session.yaml
  ocarina validate examples/github-investigation.yaml`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := playbook.Load(args[0])
		if err != nil {
			return fmt.Errorf("load: %w", err)
		}

		ctx := context.Background()
		serverArgs := interp.Strings(c.Server.Args, c.Notes)
		sess, err := mcpclient.Connect(ctx, c.Server.Command, serverArgs)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		defer sess.Close()

		res, err := sess.ListTools(ctx, nil)
		if err != nil {
			return fmt.Errorf("list tools: %w", err)
		}

		schemas := make(map[string]toolSchema)
		for _, t := range res.Tools {
			var s toolSchema
			if t.InputSchema != nil {
				raw, _ := json.Marshal(t.InputSchema)
				_ = json.Unmarshal(raw, &s)
			}
			schemas[t.Name] = s
		}

		// data-flow: keys available via notes + prior echo: fields
		available := make(map[string]bool)
		for k := range c.Notes {
			available[k] = true
		}

		var totalErrs, totalWarns int

		for i, track := range c.Tracks {
			name := track.Name
			if name == "" {
				name = fmt.Sprintf("track %d", i+1)
			}
			prefix := fmt.Sprintf("  %s (%s)", name, track.Tool)

			var errs, warns []string

			schema, found := schemas[track.Tool]
			if !found {
				errs = append(errs, fmt.Sprintf("tool %q not found on server", track.Tool))
			} else {
				// required args present
				for _, req := range schema.Required {
					if _, ok := track.Args[req]; !ok {
						errs = append(errs, fmt.Sprintf("missing required arg %q", req))
					}
				}

				// per-arg checks
				reqSet := make(map[string]bool, len(schema.Required))
				for _, r := range schema.Required {
					reqSet[r] = true
				}

				for arg, val := range track.Args {
					prop, known := schema.Properties[arg]
					if !known && len(schema.Properties) > 0 {
						warns = append(warns, fmt.Sprintf("arg %q not in schema", arg))
						continue
					}
					// skip type check for template values
					if s, ok := val.(string); ok && templateKeyRe.MatchString(s) {
						continue
					}
					if known && prop.Type != "" {
						if typeErr := validateType(val, prop.Type); typeErr != nil {
							errs = append(errs, fmt.Sprintf("arg %q: %v", arg, typeErr))
						}
					}
				}
			}

			// data-flow: {{key}} references must be available at this point
			for arg, val := range track.Args {
				s, ok := val.(string)
				if !ok {
					continue
				}
				for _, m := range templateKeyRe.FindAllStringSubmatch(s, -1) {
					key := m[1]
					if !available[key] {
						warns = append(warns, fmt.Sprintf("arg %q: {{%s}} not in notes and no prior track sets it via echo:", arg, key))
					}
				}
			}

			if len(errs) == 0 && len(warns) == 0 {
				fmt.Fprintf(os.Stdout, "%s  OK\n", prefix)
			} else {
				fmt.Fprintf(os.Stdout, "%s\n", prefix)
				for _, e := range errs {
					fmt.Fprintf(os.Stderr, "    ERROR %s\n", e)
				}
				for _, w := range warns {
					fmt.Fprintf(os.Stderr, "    WARN  %s\n", w)
				}
			}

			totalErrs += len(errs)
			totalWarns += len(warns)

			if track.Echo != "" {
				available[track.Echo] = true
			}
		}

		fmt.Fprintln(os.Stdout)
		if totalErrs == 0 && totalWarns == 0 {
			fmt.Fprintf(os.Stdout, "all %d track(s) valid\n", len(c.Tracks))
			return nil
		}
		fmt.Fprintf(os.Stdout, "%d error(s), %d warning(s)\n", totalErrs, totalWarns)
		if totalErrs > 0 {
			return fmt.Errorf("%d validation error(s)", totalErrs)
		}
		return nil
	},
}

func validateType(val any, schemaType string) error {
	switch schemaType {
	case "string":
		if _, ok := val.(string); !ok {
			return fmt.Errorf("expected string, got %T", val)
		}
	case "integer":
		switch val.(type) {
		case int, int64, float64:
		default:
			return fmt.Errorf("expected integer, got %T", val)
		}
	case "number":
		switch val.(type) {
		case int, int64, float64, float32:
		default:
			return fmt.Errorf("expected number, got %T", val)
		}
	case "boolean":
		if _, ok := val.(bool); !ok {
			return fmt.Errorf("expected boolean, got %T", val)
		}
	case "array":
		if _, ok := val.([]any); !ok {
			return fmt.Errorf("expected array, got %T", val)
		}
	case "object":
		if _, ok := val.(map[string]any); !ok {
			return fmt.Errorf("expected object, got %T", val)
		}
	}
	return nil
}

func init() {
	rootCmd.AddCommand(validateCmd)
}
