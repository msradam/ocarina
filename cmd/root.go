package cmd

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// version is overridden at build time via -ldflags "-X github.com/msradam/ocarina/cmd.version=v0.1.0".
// GoReleaser sets it for release archives.
var version = "dev"

// resolveVersion prefers the ldflags value, then falls back to the module
// version stamped by `go install module@vX.Y.Z`, which does not pass ldflags.
func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}

var rootCmd = &cobra.Command{
	Use:     "ocarina",
	Short:   "MCP rondo recorder and player",
	Long:    `ocarina records and plays back MCP tool call rondos. Record a live session, play it back without an LLM.`,
	Version: resolveVersion(),
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
