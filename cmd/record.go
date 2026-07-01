package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/msradam/ocarina/internal/proxy"
	"github.com/msradam/ocarina/internal/rondo"
	"github.com/spf13/cobra"
)

var recordCmd = &cobra.Command{
	Use:   "record <output.yaml> <command> [args...]",
	Short: "Proxy an MCP server and record tool calls to a rondo",
	Long: `Sits transparently between an MCP host and server over stdio.
Every tools/call request and its response is recorded into output.yaml.
sampling/createMessage exchanges (LLM reasoning inside agentic servers) are
captured in the rondo's llm: block.

Configure your MCP host to run:
  ocarina record session.yaml uvx mcp-server-fetch
  ocarina record session.yaml uvx mcp-server-sqlite --db-path /tmp/db.sqlite

instead of running the server directly. ocarina forwards all traffic
and writes a rondo when the session ends.`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		output := args[0]
		// strip a stray "--" separator (e.g. `record out.yaml -- npx -y ...`)
		rest := args[1:]
		if len(rest) > 0 && rest[0] == "--" {
			rest = rest[1:]
		}
		if len(rest) == 0 {
			return fmt.Errorf("missing server command")
		}
		serverCmd, serverArgs, serverEnv, err := resolveServerArgs(rest)
		if err != nil {
			return err
		}

		srv := exec.Command(serverCmd, serverArgs...) //#nosec G204 -- ocarina's purpose is launching user-specified MCP servers
		srv.Env = os.Environ()
		for k, v := range serverEnv {
			srv.Env = append(srv.Env, k+"="+v)
		}
		serverStdin, err := srv.StdinPipe()
		if err != nil {
			return err
		}
		serverStdout, err := srv.StdoutPipe()
		if err != nil {
			return err
		}
		srv.Stderr = os.Stderr

		if err := srv.Start(); err != nil {
			return fmt.Errorf("start server: %w", err)
		}

		ic := proxy.NewInterceptor()

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			ic.TeeClientToServer(serverStdin, os.Stdin)
			_ = serverStdin.Close() //#nosec G104 -- close on shutdown, error irrelevant
		}()

		go func() {
			defer wg.Done()
			ic.TeeServerToClient(os.Stdout, serverStdout)
		}()

		var recorded []proxy.RecordedCall
		doneDrain := make(chan struct{})
		go func() {
			for call := range ic.Calls {
				recorded = append(recorded, call)
			}
			close(doneDrain)
		}()

		var sampled []proxy.SampledCall
		doneSampleDrain := make(chan struct{})
		go func() {
			for sc := range ic.SampledCalls {
				sampled = append(sampled, sc)
			}
			close(doneSampleDrain)
		}()

		wg.Wait()
		ic.Close()
		<-doneDrain
		<-doneSampleDrain
		_ = srv.Wait() //#nosec G104 -- server exit after proxy shutdown, error irrelevant

		if len(recorded) == 0 && len(sampled) == 0 {
			fmt.Fprintln(os.Stderr, "ocarina: no tool calls recorded")
			return nil
		}

		r := &rondo.File{
			Server: rondo.Server{Command: serverCmd, Args: serverArgs},
		}
		noResult, _ := cmd.Flags().GetBool("no-result")
		toolIdx := map[string]int{}
		toolCount := map[string]int{}
		for _, rc := range recorded {
			toolCount[rc.Tool]++
		}
		for _, rc := range recorded {
			toolIdx[rc.Tool]++
			name := rc.Tool
			if toolCount[rc.Tool] > 1 {
				name = fmt.Sprintf("%s_%d", rc.Tool, toolIdx[rc.Tool])
			}
			step := rondo.Step{
				Name: name,
				Tool: rc.Tool,
				Args: rc.Args,
			}
			if !noResult {
				var result struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
					Structured json.RawMessage `json:"structuredContent"`
				}
				if err := json.Unmarshal(rc.Result, &result); err == nil {
					// Mirror how play derives a step's output so a recorded baseline
					// matches on --snapshot: prefer structuredContent (unwrapped),
					// else the text content blocks.
					if len(result.Structured) > 0 {
						var sc any
						if json.Unmarshal(result.Structured, &sc) == nil {
							if b, mErr := json.Marshal(unwrapStructured(sc)); mErr == nil {
								step.Result = []rondo.ResultItem{{Type: "text", Text: string(b)}}
							}
						}
					} else {
						for _, item := range result.Content {
							step.Result = append(step.Result, rondo.ResultItem{
								Type: item.Type,
								Text: item.Text,
							})
						}
					}
				}
			}
			r.Steps = append(r.Steps, step)
		}

		for _, sc := range sampled {
			r.LLM = append(r.LLM, parseSampledCall(sc))
		}

		if err := rondo.Save(output, r); err != nil {
			return fmt.Errorf("save rondo: %w", err)
		}
		msg := fmt.Sprintf("ocarina: recorded %d step(s)", len(r.Steps))
		if len(r.LLM) > 0 {
			msg += fmt.Sprintf(", %d llm round(s)", len(r.LLM))
		}
		fmt.Fprintf(os.Stderr, "%s to %s\n", msg, output)
		return nil
	},
}

func parseSampledCall(sc proxy.SampledCall) rondo.LLMRound {
	var params struct {
		SystemPrompt string `json:"systemPrompt"`
		Messages     []struct {
			Role    string `json:"role"`
			Content struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	var result struct {
		Content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Model string `json:"model"`
	}
	_ = json.Unmarshal(sc.Params, &params)
	_ = json.Unmarshal(sc.Result, &result)

	var parts []string
	if params.SystemPrompt != "" {
		parts = append(parts, "[system] "+params.SystemPrompt)
	}
	for _, m := range params.Messages {
		parts = append(parts, "["+m.Role+"] "+m.Content.Text)
	}
	return rondo.LLMRound{
		Prompt:   strings.Join(parts, "\n"),
		Response: result.Content.Text,
		Model:    result.Model,
	}
}

func init() {
	recordCmd.Flags().SetInterspersed(false)
	recordCmd.Flags().Bool("no-result", false, "omit result blocks from the rondo (smaller files, cleaner diffs)")
	rootCmd.AddCommand(recordCmd)
}
