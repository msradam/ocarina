package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/msradam/ocarina/internal/mcpclient"
	"github.com/spf13/cobra"
)

var humCmd = &cobra.Command{
	Use:   "hum <command> [server-args...] -- <tool> [key=value ...]",
	Short: "Call a single tool on an MCP server",
	Long: `Connects to an MCP server, calls one tool, prints the result, and exits.
The -- separator divides server args from the tool call.

key=value pairs after the tool name are passed as tool arguments.
Values are coerced to bool, int, or float where unambiguous, otherwise string.

Example:
  ocarina hum uvx mcp-server-time -- get_current_time timezone=America/New_York
  ocarina hum uvx mcp-server-fetch -- fetch url=https://example.com`,
	Args:               cobra.MinimumNArgs(1),
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		sep := -1
		for i, a := range args {
			if a == "--" {
				sep = i
				break
			}
		}
		if sep < 0 || sep == len(args)-1 {
			return fmt.Errorf("missing -- <tool> separator\nUsage: ocarina hum <command> [server-args...] -- <tool> [key=value ...]")
		}

		toolName := args[sep+1]
		kvArgs := args[sep+2:]

		serverCmd, serverArgs, serverEnv, err := resolveServerArgs(args[:sep])
		if err != nil {
			return err
		}

		toolArgs := make(map[string]any, len(kvArgs))
		for _, kv := range kvArgs {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return fmt.Errorf("argument %q: expected key=value", kv)
			}
			toolArgs[k] = parseArgValue(v)
		}

		ctx := context.Background()
		sess, err := mcpclient.Connect(ctx, serverCmd, serverArgs, serverEnv)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		defer sess.Close()

		result, err := sess.CallTool(ctx, &mcp.CallToolParams{
			Name:      toolName,
			Arguments: toolArgs,
		})
		if err != nil {
			return fmt.Errorf("call %s: %w", toolName, err)
		}

		for _, content := range result.Content {
			switch v := content.(type) {
			case *mcp.TextContent:
				fmt.Fprintln(os.Stdout, v.Text)
			case *mcp.ImageContent:
				fmt.Fprintf(os.Stdout, "[image %s, %d bytes]\n", v.MIMEType, len(v.Data))
			case *mcp.ResourceLink:
				fmt.Fprintf(os.Stdout, "[resource %s]\n", v.URI)
			case *mcp.EmbeddedResource:
				if v.Resource != nil && v.Resource.Text != "" {
					fmt.Fprintln(os.Stdout, v.Resource.Text)
				}
			}
		}

		if result.IsError {
			return fmt.Errorf("tool reported an error")
		}
		return nil
	},
}

// parseArgValue converts a CLI string value to its most natural Go type.
func parseArgValue(s string) any {
	switch s {
	case "true":
		return true
	case "false":
		return false
	}
	var i int64
	if _, err := fmt.Sscanf(s, "%d", &i); err == nil && fmt.Sprintf("%d", i) == s {
		return i
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%g", &f); err == nil {
		return f
	}
	return s
}

func init() {
	rootCmd.AddCommand(humCmd)
}
