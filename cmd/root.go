package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is overridden at build time via -ldflags "-X github.com/msradam/ocarina/cmd.version=v0.1.0"
var version = "dev"

var rootCmd = &cobra.Command{
	Use:     "ocarina",
	Short:   "MCP rondo recorder and player",
	Long:    `ocarina records and plays back MCP tool call rondos. Record a live session, play it back without an LLM.`,
	Version: version,
	// Execute() prints the error itself; stop cobra from printing it a second time.
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true // don't dump usage text on runtime failures
		return nil
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
