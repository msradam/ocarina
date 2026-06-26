package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/msradam/ocarina/internal/interp"
	"github.com/msradam/ocarina/internal/mcpclient"
	"github.com/msradam/ocarina/internal/playbook"
	"github.com/spf13/cobra"
)

var (
	boldCyan   = color.New(color.FgCyan, color.Bold).SprintfFunc()
	green      = color.New(color.FgGreen).SprintfFunc()
	red        = color.New(color.FgRed).SprintfFunc()
	yellowPlay = color.New(color.FgYellow).SprintfFunc()
)

var playCmd = &cobra.Command{
	Use:   "play <cassette.yaml>",
	Short: "Play back a cassette against an MCP server",
	Long: `Executes each track in the cassette by calling the specified tool
with the given arguments. No LLM involved — purely deterministic replay.

notes: values are interpolated as {{key}} throughout all track args.
echo: captures a track's text output into a note for use in later tracks.

Example:
  ocarina play session.yaml
  ocarina play session.yaml --dry-run`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := playbook.Load(args[0])
		if err != nil {
			return fmt.Errorf("load cassette: %w", err)
		}

		notes := make(map[string]string)
		for k, v := range c.Notes {
			notes[k] = v
		}

		ctx := context.Background()
		serverArgs := interp.Strings(c.Server.Args, notes)
		sess, err := mcpclient.Connect(ctx, c.Server.Command, serverArgs)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		defer sess.Close()

		dryRun, _ := cmd.Flags().GetBool("dry-run")
		var failures []string

		for i, track := range c.Tracks {
			name := track.Name
			if name == "" {
				name = fmt.Sprintf("track %d", i+1)
			}
			fmt.Fprintf(os.Stdout, "%s %s\n", boldCyan("==>"), fmt.Sprintf("%s (%s)", name, track.Tool))

			if dryRun {
				fmt.Fprintf(os.Stdout, "    [dry-run] args: %v\n\n", track.Args)
				continue
			}

			var callArgs map[string]any
			if track.Args != nil {
				callArgs, _ = interp.Apply(track.Args, notes).(map[string]any)
			}
			if callArgs == nil {
				callArgs = map[string]any{}
			}

			result, err := sess.CallTool(ctx, &mcp.CallToolParams{
				Name:      track.Tool,
				Arguments: callArgs,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "    %s %v\n\n", red("error:"), err)
				continue
			}

			var textParts []string
			for _, content := range result.Content {
				switch v := content.(type) {
				case *mcp.TextContent:
					fmt.Fprintf(os.Stdout, "%s\n", v.Text)
					textParts = append(textParts, v.Text)
				case *mcp.ImageContent:
					fmt.Fprintf(os.Stdout, "[image %s, %d bytes]\n", v.MIMEType, len(v.Data))
				case *mcp.ResourceLink:
					fmt.Fprintf(os.Stdout, "[resource %s]\n", v.URI)
				case *mcp.EmbeddedResource:
					fmt.Fprintf(os.Stdout, "[embedded resource]\n")
				default:
					fmt.Fprintf(os.Stdout, "[%T]\n", content)
				}
			}

			output := strings.Join(textParts, "\n")

			if track.Echo != "" {
				captured := output
				if track.Grab != "" {
					extracted, err := interp.Grab(captured, track.Grab)
					if err != nil {
						fmt.Fprintf(os.Stderr, "    %s %v\n", yellowPlay("grab:"), err)
					} else {
						captured = extracted
					}
				}
				notes[track.Echo] = captured
			}

			if track.Expect != nil && track.Expect.Contains != "" {
				want := interp.Apply(track.Expect.Contains, notes).(string)
				if strings.Contains(output, want) {
					fmt.Fprintf(os.Stdout, "    %s contains %q\n", green("PASS:"), want)
				} else {
					fmt.Fprintf(os.Stderr, "    %s expected output to contain %q\n", red("FAIL:"), want)
					failures = append(failures, fmt.Sprintf("track %q: expected output to contain %q", name, want))
				}
			}

			fmt.Fprintln(os.Stdout)
		}
		if len(failures) > 0 {
			return fmt.Errorf("%d expectation(s) failed", len(failures))
		}
		return nil
	},
}

func init() {
	playCmd.Flags().Bool("dry-run", false, "print tracks without executing them")
	rootCmd.AddCommand(playCmd)
}
