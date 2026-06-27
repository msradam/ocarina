package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"

	"github.com/fatih/color"
	"github.com/msradam/ocarina/internal/rondo"
	"github.com/spf13/cobra"
)

// lockFile is a snapshot of every referenced server's full tool schema. It
// captures descriptions, not just types, so a reworded tool description (a
// breaking change for an agent, and a tool-poisoning signal) is caught.
type lockFile struct {
	Servers map[string]map[string]lockTool `json:"servers"`
}

type lockTool struct {
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

var lockCmd = &cobra.Command{
	Use:   "lock <rondo.yaml>",
	Short: "Snapshot a server's full tool schema, or check it for drift",
	Long: `Connects to each server the rondo uses and records its complete tool schema,
including tool descriptions, to a lock file.

Without --check, writes the lock file. With --check, compares the live server
against the lock and exits non-zero if any tool was removed or its description
or input schema changed. A changed tool description is a breaking change for an
agent and a possible tool-poisoning signal, so drift is a failure.

Example:
  ocarina lock audit.yaml                 # write audit.yaml.lock
  ocarina lock audit.yaml --check         # fail if the live schema drifted`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r, err := rondo.Load(args[0])
		if err != nil {
			return fmt.Errorf("load rondo: %w", err)
		}
		if len(r.Servers) == 0 {
			return fmt.Errorf("rondo is missing a server: block")
		}

		path, _ := cmd.Flags().GetString("out")
		if path == "" {
			path = args[0] + ".lock"
		}
		check, _ := cmd.Flags().GetBool("check")

		live, err := captureSchema(r)
		if err != nil {
			return err
		}

		if !check {
			data, _ := json.MarshalIndent(live, "", "  ")
			if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "wrote %s (%d server(s))\n", path, len(live.Servers))
			return nil
		}

		raw, err := os.ReadFile(path) //#nosec G304 -- caller-supplied lock path
		if err != nil {
			return fmt.Errorf("read lock %q: %w (run `ocarina lock %s` first)", path, err, args[0])
		}
		var locked lockFile
		if err := json.Unmarshal(raw, &locked); err != nil {
			return fmt.Errorf("parse lock %q: %w", path, err)
		}
		return reportDrift(locked, live)
	},
}

func captureSchema(r *rondo.File) (lockFile, error) {
	ctx := context.Background()
	out := lockFile{Servers: map[string]map[string]lockTool{}}
	for key := range referencedServerKeys(r) {
		srv, ok := r.Servers[key]
		if !ok {
			continue
		}
		sess, err := connectServer(ctx, srv, r.Keys)
		if err != nil {
			return out, fmt.Errorf("connect %q: %w", key, err)
		}
		tools, err := listAllTools(ctx, sess)
		sess.Close()
		if err != nil {
			return out, fmt.Errorf("list tools (%s): %w", key, err)
		}
		m := map[string]lockTool{}
		for _, t := range tools {
			lt := lockTool{Description: t.Description}
			if t.InputSchema != nil {
				lt.InputSchema, _ = json.Marshal(t.InputSchema)
			}
			m[t.Name] = lt
		}
		out.Servers[key] = m
	}
	return out, nil
}

// reportDrift prints differences between the locked and live schemas. Removed
// tools and changed descriptions or schemas are drift (non-zero exit); new
// tools are informational.
func reportDrift(locked, live lockFile) error {
	drift := false
	for key, lockedTools := range locked.Servers {
		liveTools := live.Servers[key]
		for name, lt := range lockedTools {
			ref := lockRef(key, name, len(locked.Servers) > 1)
			cur, ok := liveTools[name]
			if !ok {
				fmt.Fprintf(os.Stdout, "%s  %s\n", color.RedString("REMOVED    "), ref)
				drift = true
				continue
			}
			if cur.Description != lt.Description {
				fmt.Fprintf(os.Stdout, "%s  %s: description changed\n", color.RedString("DESCRIPTION"), ref)
				drift = true
			}
			if !schemaEqual(lt.InputSchema, cur.InputSchema) {
				fmt.Fprintf(os.Stdout, "%s  %s: input schema changed\n", color.RedString("SCHEMA     "), ref)
				drift = true
			}
		}
		for name := range liveTools {
			if _, ok := lockedTools[name]; !ok {
				fmt.Fprintf(os.Stdout, "%s  %s\n", color.CyanString("NEW        "), lockRef(key, name, len(locked.Servers) > 1))
			}
		}
	}
	if drift {
		return fmt.Errorf("server schema drifted from the lock file")
	}
	fmt.Fprintf(os.Stdout, "%s\n", color.GreenString("no drift"))
	return nil
}

func lockRef(key, tool string, multi bool) string {
	if multi {
		return key + "." + tool
	}
	return tool
}

func schemaEqual(a, b json.RawMessage) bool {
	var av, bv any
	_ = json.Unmarshal(a, &av)
	_ = json.Unmarshal(b, &bv)
	return reflect.DeepEqual(av, bv)
}

func init() {
	lockCmd.Flags().Bool("check", false, "compare the live schema against the lock and fail on drift")
	lockCmd.Flags().StringP("out", "o", "", "lock file path (default <rondo>.lock)")
	rootCmd.AddCommand(lockCmd)
}
