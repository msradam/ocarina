package mcpclient

import (
	"context"
	"os/exec"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Session struct {
	*mcp.ClientSession
}

// Connect spawns the given command and connects to it as an MCP client.
func Connect(ctx context.Context, command string, args []string) (*Session, error) {
	cmd := exec.CommandContext(ctx, command, args...) //#nosec G204 -- ocarina's purpose is launching user-specified MCP servers
	c := mcp.NewClient(&mcp.Implementation{Name: "ocarina", Version: "0.1.0"}, nil)
	cs, err := c.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, err
	}
	return &Session{cs}, nil
}
