package mcpclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TraceWriter, when set, logs every JSON-RPC frame to it (the --trace flag).
var TraceWriter io.Writer

func newClient() *mcp.Client {
	return mcp.NewClient(&mcp.Implementation{Name: "ocarina", Version: "0.2.0"}, nil)
}

// trace wraps a transport in a frame logger when --trace is on.
func trace(t mcp.Transport) mcp.Transport {
	if TraceWriter != nil {
		return &mcp.LoggingTransport{Transport: t, Writer: TraceWriter}
	}
	return t
}

// Connect starts a local MCP server over stdio. env is merged on top of the
// current process environment.
func Connect(ctx context.Context, command string, args []string, env map[string]string) (*mcp.ClientSession, error) {
	cmd := exec.CommandContext(ctx, command, args...) //#nosec G204 -- ocarina's purpose is launching user-specified MCP servers
	if len(env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}
	return newClient().Connect(ctx, trace(&mcp.CommandTransport{Command: cmd}), nil)
}

// ConnectHTTP connects to a remote MCP server over the Streamable HTTP
// transport. headers (e.g. Authorization) are sent on every request.
func ConnectHTTP(ctx context.Context, url string, headers map[string]string) (*mcp.ClientSession, error) {
	hc := &http.Client{Transport: headerTransport{base: http.DefaultTransport, headers: headers}}
	t := &mcp.StreamableClientTransport{
		Endpoint:   url,
		HTTPClient: hc,
		// A deterministic batch runner only needs request/response, not a
		// persistent server-initiated stream.
		DisableStandaloneSSE: true,
	}
	return newClient().Connect(ctx, trace(t), nil)
}

// headerTransport adds fixed headers to every request.
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (h headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if len(h.headers) > 0 {
		req = req.Clone(req.Context())
		for k, v := range h.headers {
			req.Header.Set(k, v)
		}
	}
	return h.base.RoundTrip(req)
}
