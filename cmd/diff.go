package cmd

import (
	"fmt"
	"os"

	"github.com/gonvenience/ytbx"
	"github.com/homeport/dyff/pkg/dyff"
	"github.com/spf13/cobra"
)

var diffCmd = &cobra.Command{
	Use:   "diff <cassette-a.yaml> <cassette-b.yaml>",
	Short: "Show semantic diff between two cassettes",
	Long: `Compares two cassette files and shows what changed: tracks added or removed,
args that differ, expect assertions that changed, and result blocks that shifted.

Useful for comparing a cassette recorded against v1 of a server with one
recorded against v2, or for reviewing changes to a hand-edited cassette.

Example:
  ocarina diff examples/time-zones.yaml examples/time-zones-updated.yaml
  ocarina diff cassette-v1.yaml cassette-v2.yaml`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		from, to, err := ytbx.LoadFiles(args[0], args[1])
		if err != nil {
			return fmt.Errorf("load: %w", err)
		}

		report, err := dyff.CompareInputFiles(from, to,
			dyff.IgnoreOrderChanges(false),
			dyff.AdditionalIdentifiers("name", "tool"),
		)
		if err != nil {
			return fmt.Errorf("compare: %w", err)
		}

		if len(report.Diffs) == 0 {
			fmt.Fprintln(os.Stdout, "no differences")
			return nil
		}

		hr := &dyff.HumanReport{
			Report:               report,
			DoNotInspectCerts:    true,
			NoTableStyle:         false,
			OmitHeader:           true,
			UseGoPatchPaths:      false,
			MinorChangeThreshold: 0.1,
		}
		return hr.WriteReport(os.Stdout)
	},
}

func init() {
	rootCmd.AddCommand(diffCmd)
}
