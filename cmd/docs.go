package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/msradam/ocarina/internal/mcpclient"
	"github.com/spf13/cobra"
)

// toolSchema is a minimal JSON Schema representation sufficient for docs generation.
type toolSchema struct {
	Type        string                 `json:"type"`
	Description string                 `json:"description"`
	Properties  map[string]*toolSchema `json:"properties"`
	Required    []string               `json:"required"`
	Enum        []any                  `json:"enum"`
	Items       *toolSchema            `json:"items"`
}

var docsCmd = &cobra.Command{
	Use:   "docs <command> [args...]",
	Short: "Generate markdown documentation for an MCP server's tools",
	Long: `Connects to an MCP server and generates markdown documentation for all tools.
Each tool gets a synopsis, argument table, and an example cassette track you
can drop straight into a cassette.

Example:
  ocarina docs uvx mcp-server-time
  ocarina docs npx -y @modelcontextprotocol/server-filesystem /tmp
  ocarina docs uvx mcp-server-fetch > docs/fetch.md`,
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

		out := os.Stdout
		if outPath, _ := cmd.Flags().GetString("out"); outPath != "" {
			f, err := os.Create(outPath) //#nosec G304
			if err != nil {
				return err
			}
			defer f.Close()
			out = f
		}

		serverLabel := strings.Join(args, " ")
		fmt.Fprintf(out, "# %s\n\n", serverLabel)
		fmt.Fprintf(out, "**%d tool(s)**\n\n", len(res.Tools))

		for _, t := range res.Tools {
			fmt.Fprintf(out, "- [%s](#%s)\n", t.Name, strings.ToLower(t.Name))
		}
		fmt.Fprintln(out)
		fmt.Fprintln(out, "---")
		fmt.Fprintln(out)

		for _, t := range res.Tools {
			fmt.Fprintf(out, "## %s\n\n", t.Name)

			if t.Annotations != nil {
				if b := toolBadges(t.Annotations); b != "" {
					fmt.Fprintf(out, "%s\n\n", strings.TrimSpace(b))
				}
			}

			if t.Description != "" {
				fmt.Fprintf(out, "%s\n\n", t.Description)
			}

			if t.InputSchema == nil {
				fmt.Fprintln(out, "---")
				fmt.Fprintln(out)
				continue
			}

			raw, err := json.Marshal(t.InputSchema)
			if err != nil {
				fmt.Fprintln(out, "---")
				fmt.Fprintln(out)
				continue
			}

			var schema toolSchema
			if err := json.Unmarshal(raw, &schema); err != nil || len(schema.Properties) == 0 {
				// No arguments — just emit the example with no args block.
				fmt.Fprint(out, "**Example:**\n\n")
				fmt.Fprintln(out, "```yaml")
				fmt.Fprintf(out, "- name: call %s\n", t.Name)
				fmt.Fprintf(out, "  tool: %s\n", t.Name)
				fmt.Fprint(out, "```\n\n---\n\n")
				continue
			}

			required := make(map[string]bool, len(schema.Required))
			for _, r := range schema.Required {
				required[r] = true
			}

			names := make([]string, 0, len(schema.Properties))
			for n := range schema.Properties {
				names = append(names, n)
			}
			sort.Strings(names)

			fmt.Fprint(out, "**Arguments:**\n\n")
			fmt.Fprintln(out, "| Name | Type | Required | Description |")
			fmt.Fprintln(out, "|------|------|----------|-------------|")
			for _, name := range names {
				prop := schema.Properties[name]
				typ := prop.Type
				if typ == "" {
					typ = "any"
				}
				req := "no"
				if required[name] {
					req = "**yes**"
				}
				desc := prop.Description
				if len(prop.Enum) > 0 {
					strs := make([]string, len(prop.Enum))
					for i, e := range prop.Enum {
						strs[i] = fmt.Sprintf("`%v`", e)
					}
					if desc != "" {
						desc += " "
					}
					desc += "One of: " + strings.Join(strs, ", ")
				}
				fmt.Fprintf(out, "| `%s` | %s | %s | %s |\n", name, typ, req, desc)
			}
			fmt.Fprintln(out)

			fmt.Fprint(out, "**Example:**\n\n")
			fmt.Fprintln(out, "```yaml")
			fmt.Fprintf(out, "- name: call %s\n", t.Name)
			fmt.Fprintf(out, "  tool: %s\n", t.Name)
			fmt.Fprintln(out, "  args:")
			for _, name := range names {
				fmt.Fprintf(out, "    %s: %s\n", name, docExampleValue(name, schema.Properties[name]))
			}
			fmt.Fprint(out, "```\n\n---\n\n")
		}

		return nil
	},
}

func docExampleValue(name string, s *toolSchema) string {
	if len(s.Enum) > 0 {
		return fmt.Sprintf("%v", s.Enum[0])
	}
	switch s.Type {
	case "integer", "number":
		return "1"
	case "boolean":
		return "false"
	case "array":
		return "[]"
	case "object":
		return "{}"
	default:
		return "<" + name + ">"
	}
}

func init() {
	docsCmd.Flags().String("out", "", "write output to a file instead of stdout")
	docsCmd.Flags().SetInterspersed(false)
	rootCmd.AddCommand(docsCmd)
}
