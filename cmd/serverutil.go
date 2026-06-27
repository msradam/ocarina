package cmd

import (
	"fmt"

	"github.com/msradam/ocarina/internal/playbook"
	"github.com/msradam/ocarina/internal/servers"
)

// resolveServer fills in Command/Args/Env on s when s.Name is set.
// Returns an error if the name is not found in any mcp.json.
func resolveServer(s *playbook.Server) error {
	if s.Name == "" {
		return nil
	}
	entry, ok := servers.Lookup(s.Name)
	if !ok {
		return fmt.Errorf("server %q not found in any mcp.json (searched .mcp.json, ~/.mcp.json, Claude Desktop config)", s.Name)
	}
	s.Command = entry.Command
	s.Args = entry.Args
	s.Env = entry.Env
	s.Name = ""
	return nil
}

// resolveServerArgs resolves a CLI-specified server: either a known name from
// mcp.json or a literal command with its args. Returns command, args, env.
func resolveServerArgs(args []string) (cmd string, sArgs []string, env map[string]string, err error) {
	if len(args) == 0 {
		return "", nil, nil, fmt.Errorf("no server specified")
	}
	if entry, ok := servers.Lookup(args[0]); ok {
		return entry.Command, append(entry.Args, args[1:]...), entry.Env, nil
	}
	return args[0], args[1:], nil, nil
}
