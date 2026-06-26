package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/msradam/ocarina/internal/mcpclient"
	"github.com/msradam/ocarina/internal/playbook"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var composeCmd = &cobra.Command{
	Use:   "compose <command> [args...]",
	Short: "Introspect an MCP server and print available tools",
	Long: `Connects to an MCP server and lists all available tools with their
schemas. Use this to discover what tools exist before cutting a cassette.
Tool annotations ([readonly], [destructive], [idempotent]) are shown where declared.

Example:
  ocarina compose uvx mcp-server-fetch
  ocarina compose uvx mcp-server-sqlite --db-path /tmp/db.sqlite
  ocarina compose npx -y @modelcontextprotocol/server-filesystem /tmp`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		sess, err := mcpclient.Connect(ctx, args[0], args[1:], nil)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		defer sess.Close()

		res, err := sess.ListTools(ctx, nil)
		if err != nil {
			return fmt.Errorf("list tools: %w", err)
		}

		outputYAML, _ := cmd.Flags().GetBool("yaml")

		if outputYAML {
			c := &playbook.Cassette{
				Server: playbook.Server{Command: args[0], Args: args[1:]},
			}
			for _, t := range res.Tools {
				c.Rondo = append(c.Rondo, playbook.Track{
					Name: "call " + t.Name,
					Tool: t.Name,
				})
			}
			return yaml.NewEncoder(os.Stdout).Encode(c)
		}

		fmt.Fprintf(os.Stdout, "Server: %s %v\n", args[0], args[1:])
		fmt.Fprintf(os.Stdout, "%d tool(s) available:\n\n", len(res.Tools))
		for _, t := range res.Tools {
			badges := toolBadges(t.Annotations)
			fmt.Fprintf(os.Stdout, "  tool: %s%s\n", badges, t.Name)
			if t.Description != "" {
				fmt.Fprintf(os.Stdout, "    description: %s\n", t.Description)
			}
			if t.InputSchema != nil {
				schema, _ := json.MarshalIndent(t.InputSchema, "    ", "  ")
				fmt.Fprintf(os.Stdout, "    schema: %s\n", schema)
			}
			fmt.Fprintln(os.Stdout)
		}
		return nil
	},
}

func toolBadges(ann *mcp.ToolAnnotations) string {
	if ann == nil {
		return ""
	}
	var b []string
	if ann.ReadOnlyHint {
		b = append(b, "[readonly]")
	}
	if ann.DestructiveHint != nil && *ann.DestructiveHint {
		b = append(b, "[destructive]")
	}
	if ann.IdempotentHint {
		b = append(b, "[idempotent]")
	}
	if len(b) == 0 {
		return ""
	}
	return strings.Join(b, " ") + " "
}

func init() {
	composeCmd.Flags().Bool("yaml", false, "emit a skeleton cassette YAML instead of human-readable output")
	composeCmd.Flags().SetInterspersed(false)
	rootCmd.AddCommand(composeCmd)
}
