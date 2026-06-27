package servers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
)

type Entry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type configFile struct {
	MCPServers map[string]Entry `json:"mcpServers"`
}

// Lookup searches standard mcp.json locations for a named server and returns
// the first match. Search order:
//  1. .mcp.json in the current directory
//  2. ~/.mcp.json
//  3. Claude Desktop config (platform-specific path)
func Lookup(name string) (*Entry, bool) {
	for _, path := range searchPaths() {
		if e, ok := fromFile(path, name); ok {
			return e, true
		}
	}
	return nil, false
}

func searchPaths() []string {
	paths := []string{".mcp.json"}
	home, err := os.UserHomeDir()
	if err != nil {
		return paths
	}
	paths = append(paths, filepath.Join(home, ".mcp.json"))

	switch runtime.GOOS {
	case "darwin":
		paths = append(paths,
			filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json"),
		)
	case "windows":
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			paths = append(paths, filepath.Join(appdata, "Claude", "claude_desktop_config.json"))
		}
	default:
		paths = append(paths,
			filepath.Join(home, ".config", "Claude", "claude_desktop_config.json"),
		)
	}
	return paths
}

func fromFile(path, name string) (*Entry, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var c configFile
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, false
	}
	e, ok := c.MCPServers[name]
	if !ok {
		return nil, false
	}
	return &e, true
}
