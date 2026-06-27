package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/msradam/ocarina/internal/interp"
	"github.com/msradam/ocarina/internal/mcpclient"
	"github.com/msradam/ocarina/internal/rondo"
	"github.com/spf13/cobra"
)

var diffCmd = &cobra.Command{
	Use:   "diff <rondo.yaml>",
	Short: "Compare a rondo against the live server's current tool schemas",
	Long: `Connects to the server declared in the rondo and compares every tool step
against the server's current schemas. Shows tools that were removed, args that
became required, and new tools the server now offers that the rondo doesn't use.

Exits non-zero if any tools used by the rondo no longer exist on the server.

Example:
  ocarina diff examples/github-investigation.yaml
  ocarina diff session.yaml`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r, err := rondo.Load(args[0])
		if err != nil {
			return fmt.Errorf("load rondo: %w", err)
		}

		ctx := context.Background()
		if err := resolveServer(&r.Server); err != nil {
			return err
		}
		serverArgs := interp.Strings(r.Server.Args, r.Keys)
		serverEnv := interp.StringMap(r.Server.Env, r.Keys)
		sess, err := mcpclient.Connect(ctx, r.Server.Command, serverArgs, serverEnv)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		defer sess.Close()

		res, err := sess.ListTools(ctx, nil)
		if err != nil {
			return fmt.Errorf("list tools: %w", err)
		}

		type liveTool struct {
			required   map[string]bool
			properties map[string]bool
		}
		live := make(map[string]liveTool, len(res.Tools))
		for _, t := range res.Tools {
			lt := liveTool{
				required:   make(map[string]bool),
				properties: make(map[string]bool),
			}
			if t.InputSchema != nil {
				raw, _ := json.Marshal(t.InputSchema)
				var s struct {
					Required   []string       `json:"required"`
					Properties map[string]any `json:"properties"`
				}
				if json.Unmarshal(raw, &s) == nil {
					for _, req := range s.Required {
						lt.required[req] = true
					}
					for k := range s.Properties {
						lt.properties[k] = true
					}
				}
			}
			live[t.Name] = lt
		}

		// collect tools used by the rondo
		usedTools := make(map[string]bool)
		for _, step := range r.Steps {
			if step.Tool != "" {
				usedTools[step.Tool] = true
			}
		}

		removed := false
		printed := make(map[string]bool)

		for _, step := range r.Steps {
			if step.Tool == "" {
				continue
			}
			if printed[step.Tool] {
				continue
			}
			printed[step.Tool] = true

			lt, found := live[step.Tool]
			if !found {
				fmt.Fprintf(os.Stdout, "%s  %s\n", color.RedString("REMOVED "), step.Tool)
				removed = true
				continue
			}

			var issues []string
			for req := range lt.required {
				if _, ok := step.Args[req]; !ok {
					issues = append(issues, fmt.Sprintf("arg %q now required", req))
				}
			}
			for arg := range step.Args {
				if len(lt.properties) > 0 && !lt.properties[arg] {
					issues = append(issues, fmt.Sprintf("arg %q not in schema", arg))
				}
			}

			if len(issues) == 0 {
				fmt.Fprintf(os.Stdout, "%s  %s\n", color.GreenString("OK      "), step.Tool)
			} else {
				for _, iss := range issues {
					fmt.Fprintf(os.Stdout, "%s  %s: %s\n", color.YellowString("WARN    "), step.Tool, iss)
				}
			}
		}

		// new tools the server has that the rondo doesn't use
		for name := range live {
			if !usedTools[name] {
				fmt.Fprintf(os.Stdout, "%s  %s\n", color.CyanString("+       "), name)
			}
		}

		if removed {
			return fmt.Errorf("rondo uses tools that no longer exist on the server")
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(diffCmd)
}
