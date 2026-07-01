package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/msradam/ocarina/internal/interp"
	"github.com/msradam/ocarina/internal/load"
	"github.com/msradam/ocarina/internal/rondo"
	"github.com/spf13/cobra"
)

var loadCmd = &cobra.Command{
	Use:   "load <rondo.yaml>",
	Short: "Load-test a server by running a rondo with many concurrent virtual users",
	Long: `Runs the rondo's tool steps as a load scenario: --vus virtual users each loop
the scenario concurrently, either for --duration or until --iterations total.
Reports throughput and latency percentiles, like k6. --threshold (e.g.
p95<500ms) fails the run, so it works as a CI performance gate.

Each virtual user is a goroutine with its own session, so a remote (url:) server
scales to far more users than a local stdio one. This measures behavior under
concurrency on purpose, so unlike play it is not deterministic.

Example:
  ocarina load smoke.yaml --vus 50 --duration 30s
  ocarina load smoke.yaml --vus 20 --iterations 1000 --threshold p95<500ms`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r, err := rondo.Load(args[0])
		if err != nil {
			return fmt.Errorf("load rondo: %w", err)
		}
		if len(r.Servers) == 0 {
			return fmt.Errorf("rondo is missing a server: block")
		}
		if len(r.Steps) == 0 {
			return fmt.Errorf("rondo has no steps (put them under rondo:, tasks:, or steps:)")
		}
		stdout = io.Discard // silence per-call output during the run

		vus, _ := cmd.Flags().GetInt("vus")
		iters, _ := cmd.Flags().GetInt("iterations")
		durStr, _ := cmd.Flags().GetString("duration")
		thrStr, _ := cmd.Flags().GetString("threshold")

		var threshold *load.Threshold
		if thrStr != "" {
			t, err := load.ParseThreshold(thrStr)
			if err != nil {
				return err
			}
			threshold = &t
		}

		ctx := context.Background()
		var cancel context.CancelFunc
		if iters <= 0 {
			d := 10 * time.Second
			if durStr != "" {
				if parsed, perr := time.ParseDuration(durStr); perr == nil {
					d = parsed
				} else {
					return fmt.Errorf("invalid duration %q: %w", durStr, perr)
				}
			}
			ctx, cancel = context.WithTimeout(ctx, d)
			defer cancel()
		}

		coll := &load.Collector{}
		var started int64 // total iterations started across all VUs

		fmt.Fprintf(os.Stdout, "load: %d VUs", vus)
		if iters > 0 {
			fmt.Fprintf(os.Stdout, ", %d iterations\n", iters)
		} else {
			fmt.Fprintf(os.Stdout, ", %s\n", durStr)
		}

		start := time.Now()
		var wg sync.WaitGroup
		for v := 0; v < vus; v++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				runVU(ctx, r, coll, &started, iters)
			}()
		}
		wg.Wait()
		elapsed := time.Since(start)

		summary := coll.Summarize(elapsed)
		fmt.Fprintln(os.Stdout, summary.String())

		if threshold != nil {
			if threshold.Met(summary) {
				fmt.Fprintf(os.Stdout, "threshold %s: pass\n", threshold)
				return nil
			}
			return fmt.Errorf("threshold %s: failed", threshold)
		}
		if summary.Failed > 0 {
			return fmt.Errorf("%d of %d calls failed", summary.Failed, summary.Calls)
		}
		return nil
	},
}

// runVU is one virtual user: its own sessions, looping the rondo's tool steps.
func runVU(ctx context.Context, r *rondo.File, coll *load.Collector, started *int64, iters int) {
	sessions := map[string]*mcp.ClientSession{}
	defer func() {
		for _, s := range sessions {
			s.Close()
		}
	}()
	get := func(key string) (*mcp.ClientSession, error) {
		if s, ok := sessions[key]; ok {
			return s, nil
		}
		srv, ok := r.Servers[key]
		if !ok {
			return nil, fmt.Errorf("server %q not defined", key)
		}
		s, err := connectServer(ctx, srv, r.Keys)
		if err != nil {
			return nil, err
		}
		sessions[key] = s
		return s, nil
	}

	for {
		if iters > 0 && atomic.AddInt64(started, 1) > int64(iters) {
			return
		}
		if ctx.Err() != nil {
			return
		}
		notes := make(map[string]string, len(r.Keys))
		for k, v := range r.Keys {
			notes[k] = v
		}
		for _, step := range r.Steps {
			if step.Tool == "" {
				continue // load runs tool steps; resources/sleep/loop are out of scope for now
			}
			sess, err := get(r.StepServerKey(step))
			if err != nil {
				coll.Record(0, false)
				return
			}
			t0 := time.Now()
			out, isErr, derr := dispatchStep(ctx, sess, step, notes)
			d := time.Since(t0)
			if derr != nil && ctx.Err() != nil {
				return // duration elapsed mid-call; don't count the shutdown
			}
			ok := derr == nil
			if ok && isErr {
				expected := step.Expect != nil && step.Expect.IsError != nil && *step.Expect.IsError
				ok = expected
			}
			if ok && step.Grab != "" {
				if g, gerr := interp.Grab(out, step.Grab); gerr == nil {
					out = g
				} else {
					ok = false
				}
			}
			if ok && step.Echo != "" {
				notes[step.Echo] = out
			}
			if ok && step.Expect != nil {
				ok = checkExpect(step.Expect, out, isErr, d, notes) == ""
			}
			coll.Record(d, ok)
		}
	}
}

func init() {
	loadCmd.Flags().Int("vus", 1, "number of concurrent virtual users")
	loadCmd.Flags().String("duration", "10s", "how long to run (ignored when --iterations is set)")
	loadCmd.Flags().Int("iterations", 0, "total scenario iterations across all VUs (overrides --duration)")
	loadCmd.Flags().String("threshold", "", "fail if a latency percentile is exceeded, e.g. p95<500ms")
	rootCmd.AddCommand(loadCmd)
}
