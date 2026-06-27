package mcpclient

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// env is merged on top of the current process environment.
func Connect(ctx context.Context, command string, args []string, env map[string]string) (*mcp.ClientSession, error) {
	cmd := exec.CommandContext(ctx, command, args...) //#nosec G204 -- ocarina's purpose is launching user-specified MCP servers
	if len(env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "ocarina", Version: "0.1.0"}, nil)
	cs, err := c.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, err
	}
	return cs, nil
}
