package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ocarina",
	Short: "MCP cassette recorder and player",
	Long: `ocarina records, composes, and plays back MCP tool call cassettes.

Record a live session. Play it back without an LLM. Every tool call is a track.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
