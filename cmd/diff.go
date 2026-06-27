package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/fatih/color"
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
		if len(r.Servers) == 0 {
			return fmt.Errorf("rondo is missing a server: block")
		}

		type liveTool struct {
			required   map[string]bool
			properties map[string]bool
		}
		// live[serverKey][toolName]
		live := make(map[string]map[string]liveTool)
		for key := range referencedServerKeys(r) {
			srv, ok := r.Servers[key]
			if !ok {
				continue // undefined reference; reported per-step below
			}
			sess, err := connectServer(ctx, srv, r.Keys)
			if err != nil {
				return fmt.Errorf("connect %q: %w", key, err)
			}
			defer sess.Close()

			res, err := sess.ListTools(ctx, nil)
			if err != nil {
				return fmt.Errorf("list tools (%s): %w", key, err)
			}

			live[key] = make(map[string]liveTool, len(res.Tools))
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
				live[key][t.Name] = lt
			}
		}

		multi := r.MultiServer()
		ref := func(key, tool string) string {
			if multi {
				return key + "." + tool
			}
			return tool
		}

		// collect tools used by the rondo, per server
		usedTools := make(map[string]bool) // key "serverKey\x00tool"
		for _, step := range r.Steps {
			if step.Tool == "" {
				continue
			}
			usedTools[r.StepServerKey(step)+"\x00"+step.Tool] = true
		}

		removed := false
		undefinedRef := false
		printed := make(map[string]bool)

		for _, step := range r.Steps {
			if step.Tool == "" {
				continue
			}
			key := r.StepServerKey(step)
			if printed[key+"\x00"+step.Tool] {
				continue
			}
			printed[key+"\x00"+step.Tool] = true

			if _, defined := r.Servers[key]; !defined {
				fmt.Fprintf(os.Stdout, "%s  %s (server %q not defined in servers:)\n", color.RedString("UNDEFINED"), ref(key, step.Tool), key)
				undefinedRef = true
				continue
			}

			lt, found := live[key][step.Tool]
			if !found {
				fmt.Fprintf(os.Stdout, "%s  %s\n", color.RedString("REMOVED "), ref(key, step.Tool))
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
				fmt.Fprintf(os.Stdout, "%s  %s\n", color.GreenString("OK      "), ref(key, step.Tool))
			} else {
				for _, iss := range issues {
					fmt.Fprintf(os.Stdout, "%s  %s: %s\n", color.YellowString("WARN    "), ref(key, step.Tool), iss)
				}
			}
		}

		// new tools each server has that the rondo doesn't use
		for key, serverLive := range live {
			for name := range serverLive {
				if !usedTools[key+"\x00"+name] {
					fmt.Fprintf(os.Stdout, "%s  %s\n", color.CyanString("+       "), ref(key, name))
				}
			}
		}

		if undefinedRef {
			return fmt.Errorf("rondo references a server not defined in the servers map")
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
