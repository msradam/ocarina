package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	jschema "github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/msradam/ocarina/internal/rondo"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve <rondo.yaml>...",
	Short: "Expose rondos as composite MCP tools over stdio",
	Long: `Serves each rondo as a single MCP tool. The rondo's params: become the tool's
input schema; calling the tool runs the rondo's steps deterministically against
the server(s) it declares and returns the value named by return:.

This turns a multi-step workflow into one callable tool: an agent calls it once
instead of orchestrating the underlying calls itself, and the run is
deterministic with no LLM in the loop.

Each tool call executes the rondo's steps, which call real tools on the target
server. Only serve rondos you trust to run.

Example:
  ocarina serve motifs/provision.yaml
  ocarina serve ./tools/`,
	Args:         cobra.MinimumNArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// serve speaks MCP over stdout; keep all engine/human output off it.
		stdout = io.Discard

		files, err := collectServeFiles(args)
		if err != nil {
			return err
		}

		server := mcp.NewServer(&mcp.Implementation{Name: "ocarina", Version: resolveVersion()}, nil)
		var names []string
		for _, path := range files {
			mf, err := rondo.Load(path)
			if err != nil {
				return fmt.Errorf("load %s: %w", path, err)
			}
			if len(mf.Servers) == 0 {
				return fmt.Errorf("%s: a served rondo must declare a server: or servers: block", path)
			}
			tool, handler := motifTool(mf, filepath.Dir(path), serveToolName(path, mf))
			server.AddTool(tool, handler)
			names = append(names, tool.Name)
		}
		fmt.Fprintf(os.Stderr, "ocarina serve: %d tool(s) over stdio: %s\n", len(names), strings.Join(names, ", "))
		return server.Run(context.Background(), &mcp.StdioTransport{})
	},
}

// collectServeFiles expands directory arguments to their .yaml/.yml entries.
func collectServeFiles(args []string) ([]string, error) {
	var out []string
	for _, a := range args {
		info, err := os.Stat(a)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			out = append(out, a)
			continue
		}
		entries, err := os.ReadDir(a)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if n := e.Name(); strings.HasSuffix(n, ".yaml") || strings.HasSuffix(n, ".yml") {
				out = append(out, filepath.Join(a, n))
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no rondo files found in %v", args)
	}
	return out, nil
}

func serveToolName(path string, mf *rondo.File) string {
	if mf.Name != "" {
		return mf.Name
	}
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// motifInputSchema turns a rondo's params: into the tool's JSON Schema.
func motifInputSchema(params []rondo.Param) *jschema.Schema {
	s := &jschema.Schema{Type: "object", Properties: map[string]*jschema.Schema{}}
	for _, p := range params {
		typ := p.Type
		if typ == "" {
			typ = "string"
		}
		s.Properties[p.Name] = &jschema.Schema{Type: typ, Description: p.Description}
		if p.Required {
			s.Required = append(s.Required, p.Name)
		}
	}
	return s
}

// motifTool builds the MCP tool and its handler for one served rondo. Each call
// runs the rondo's steps with a fresh engine.
//
// ponytail: per-call connect (a fresh session each invocation). Pool sessions
// if call latency against stdio servers matters.
func motifTool(mf *rondo.File, dir, name string) (*mcp.Tool, mcp.ToolHandler) {
	desc := mf.Description
	if desc == "" {
		desc = "Runs the " + name + " rondo."
	}
	tool := &mcp.Tool{Name: name, Description: desc, InputSchema: motifInputSchema(mf.Params)}

	handler := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// scope: rondo keys: are defaults, param defaults next, caller args win.
		notes := make(map[string]string, len(mf.Keys)+len(mf.Params))
		for k, v := range mf.Keys {
			notes[k] = v
		}
		for _, p := range mf.Params {
			if p.Default != "" {
				notes[p.Name] = p.Default
			}
		}
		var rawArgs map[string]any
		if len(req.Params.Arguments) > 0 {
			_ = json.Unmarshal(req.Params.Arguments, &rawArgs)
		}
		for k, v := range rawArgs {
			notes[k] = fmt.Sprint(v)
		}

		eng := newEngine(ctx, mf, notes)
		defer eng.close()
		if fails := eng.runSteps(mf.Steps, notes, dir, 0); len(fails) > 0 {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: strings.Join(fails, "\n")}},
			}, nil
		}

		out := "ok"
		if mf.Return != "" {
			out = notes[mf.Return]
		}
		res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}
		// surface a JSON return value as structured content too
		var js any
		if json.Unmarshal([]byte(out), &js) == nil {
			if _, ok := js.(map[string]any); ok {
				res.StructuredContent = js
			}
		}
		return res, nil
	}
	return tool, handler
}

func init() {
	rootCmd.AddCommand(serveCmd)
}
