package cmd

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"sync/atomic"
	"testing"

	jschema "github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// objectSchema is the minimal input schema AddTool requires. These engine tests
// drive dispatchStep directly, which does not consult the schema, so an empty
// object is enough.
var objectSchema = &jschema.Schema{Type: "object"}

// TestMain silences the human progress output so engine tests print only their
// own failures. checkExpect and the play loop write PASS/output lines to stdout.
func TestMain(m *testing.M) {
	stdout = io.Discard
	os.Exit(m.Run())
}

// newFakeSession stands up an in-process MCP server with a fixed set of tools
// and a resource, connects the real Ocarina client to it over an in-memory
// pipe, and returns a live session. No subprocess, no network: the engine runs
// against a controlled server deterministically.
func newFakeSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "fake", Version: "0.0.1"}, nil)

	server.AddTool(&mcp.Tool{Name: "echo", Description: "echo back the text arg", InputSchema: objectSchema},
		func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(req.Params.Arguments, &args)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: args.Text}}}, nil
		})

	server.AddTool(&mcp.Tool{Name: "boom", Description: "always returns isError:true", InputSchema: objectSchema},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "kaboom"}},
				IsError: true,
			}, nil
		})

	// profile returns structured content plus a contradicting text block, so a
	// test can prove the engine reads structuredContent, not the text.
	server.AddTool(&mcp.Tool{Name: "profile", Description: "returns structured JSON", InputSchema: objectSchema},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content:           []mcp.Content{&mcp.TextContent{Text: "ignore this text"}},
				StructuredContent: map[string]any{"name": "ocarina", "stars": 42},
			}, nil
		})

	// count returns an incrementing call number as text, for retry-until tests.
	var counter int64
	server.AddTool(&mcp.Tool{Name: "count", Description: "returns an incrementing call number", InputSchema: objectSchema},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			n := atomic.AddInt64(&counter, 1)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strconv.FormatInt(n, 10)}}}, nil
		})

	// flaky returns isError:true until its third call, for default-retry tests.
	var attempts int64
	server.AddTool(&mcp.Tool{Name: "flaky", Description: "fails until the third call", InputSchema: objectSchema},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if atomic.AddInt64(&attempts, 1) < 3 {
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: "not ready"}},
					IsError: true,
				}, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ready"}}}, nil
		})

	// peek is annotated read-only, for testing the --safe gate. echo/write are not.
	server.AddTool(&mcp.Tool{Name: "peek", Description: "read-only probe", InputSchema: objectSchema,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "peeked"}}}, nil
		})

	server.AddResource(&mcp.Resource{URI: "test://greeting", Name: "greeting", MIMEType: "text/plain"},
		func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
				{URI: req.Params.URI, MIMEType: "text/plain", Text: "hello resource"},
			}}, nil
		})

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "ocarina-test", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() {
		session.Close()
		serverSession.Close()
	})
	return session
}
