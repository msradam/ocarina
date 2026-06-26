package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"

	"github.com/fatih/color"
	jschema "github.com/google/jsonschema-go/jsonschema"
	"github.com/msradam/ocarina/internal/interp"
	"github.com/msradam/ocarina/internal/mcpclient"
	"github.com/msradam/ocarina/internal/playbook"
	"github.com/spf13/cobra"
)

var templateKeyRe = regexp.MustCompile(`\{\{(\w+)\}\}`)

var (
	okLabel   = color.New(color.FgGreen, color.Bold).Sprint("OK")
	errLabel  = color.New(color.FgRed, color.Bold).Sprint("ERROR")
	warnLabel = color.New(color.FgYellow, color.Bold).Sprint("WARN ")
)

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
		serverEnv := interp.StringMap(c.Server.Env, c.Notes)
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

			entry, found := schemas[track.Tool]
			if !found {
				errs = append(errs, fmt.Sprintf("tool %q not found on server", track.Tool))
			} else {
				// required args present
				for _, req := range entry.required {
					if _, ok := track.Args[req]; !ok {
						errs = append(errs, fmt.Sprintf("missing required arg %q", req))
					}
				}

				// unknown args
				for arg := range track.Args {
					if _, ok := entry.properties[arg]; !ok && len(entry.properties) > 0 {
						warns = append(warns, fmt.Sprintf("arg %q not in schema", arg))
					}
				}

				// type validation via JSON Schema (skips template values)
				if entry.raw != nil {
					filtered := make(map[string]any)
					for k, v := range track.Args {
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

			if track.Echo != "" {
				available[track.Echo] = true
			}
		}

		fmt.Fprintln(os.Stdout)
		if totalErrs == 0 && totalWarns == 0 {
			fmt.Fprintf(os.Stdout, "%s\n", color.GreenString("all %d track(s) valid", len(c.Tracks)))
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
