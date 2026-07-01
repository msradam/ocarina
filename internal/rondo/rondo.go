package rondo

import (
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
)

type File struct {
	Keys    map[string]string `yaml:"keys,omitempty"`
	Servers map[string]Server `yaml:"servers,omitempty"`
	Server  Server            `yaml:"server,omitempty"`
	Steps   []Step            `yaml:"rondo,omitempty"`
	Tasks   []Step            `yaml:"tasks,omitempty"` // Ansible-style alias; merged into Steps in Load
	Steps2  []Step            `yaml:"steps,omitempty"` // common alias for rondo:; merged into Steps in Load
	LLM     []LLMRound        `yaml:"llm,omitempty"`

	// These describe a rondo when it is served as a composite MCP tool via
	// `ocarina serve`. They are ignored during play.
	Name        string  `yaml:"name,omitempty"`        // tool name (defaults to the file's base name)
	Description string  `yaml:"description,omitempty"` // tool description shown to the agent
	Params      []Param `yaml:"params,omitempty"`      // typed inputs; become the tool's inputSchema
	Return      string  `yaml:"return,omitempty"`      // a key (set via echo:) to return as the tool result

	// ServerOrder preserves the servers: insertion order for deterministic
	// default selection when a step omits server:. Not serialized.
	ServerOrder []string `yaml:"-"`
}

// Param declares one input of a served rondo. Type is a JSON Schema type
// (string, number, integer, boolean); empty defaults to string.
type Param struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type,omitempty"`
	Description string `yaml:"description,omitempty"`
	Required    bool   `yaml:"required,omitempty"`
	Default     string `yaml:"default,omitempty"`
}

// DefaultServerKey returns the server key used by steps that omit server:.
func (f *File) DefaultServerKey() string {
	if len(f.ServerOrder) > 0 {
		return f.ServerOrder[0]
	}
	return "default"
}

// StepServerKey returns the server key a step targets, defaulting when omitted.
func (f *File) StepServerKey(step Step) string {
	if step.Server != "" {
		return step.Server
	}
	return f.DefaultServerKey()
}

// MultiServer reports whether more than one server is in play, so callers can
// namespace tool names (e.g. time.get_current_time) in output.
func (f *File) MultiServer() bool {
	return len(f.Servers) > 1
}

type Server struct {
	// Name is set when the rondo uses `server: <name>` (string form).
	// Callers must resolve Name to Command/Args/Env before connecting.
	// Never serialized: it is an input-only convenience, not part of the schema.
	Name string `yaml:"-"`

	// stdio transport: a local subprocess.
	Command string            `yaml:"command,omitempty"`
	Args    []string          `yaml:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`

	// Streamable HTTP transport: a remote server. URL is mutually exclusive
	// with Command. Headers (e.g. Authorization) are sent on every request.
	URL     string            `yaml:"url,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty"`
}

// IsHTTP reports whether the server uses the Streamable HTTP transport.
func (s Server) IsHTTP() bool { return s.URL != "" }

// UnmarshalYAML handles both scalar (`server: github`) and map forms.
func (s *Server) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var name string
	if err := unmarshal(&name); err == nil {
		s.Name = name
		return nil
	}
	type plain Server
	return unmarshal((*plain)(s))
}

type Step struct {
	Name             string            `yaml:"name,omitempty"`
	Server           string            `yaml:"server,omitempty"` // references a key in servers:
	Motif            string            `yaml:"motif,omitempty"`  // path to a reusable rondo fragment; its steps run inline
	With             map[string]string `yaml:"with,omitempty"`   // parameters passed to a motif, evaluated in the caller's scope
	Tool             string            `yaml:"tool,omitempty"`
	Resource         string            `yaml:"resource,omitempty"`
	ListResources    string            `yaml:"list_resources,omitempty"`
	Sleep            string            `yaml:"sleep,omitempty"`
	Args             map[string]any    `yaml:"args,omitempty"`
	When             string            `yaml:"when,omitempty"`
	Timeout          string            `yaml:"timeout,omitempty"`
	Retry            *RetryConfig      `yaml:"retry,omitempty"`
	Echo             string            `yaml:"echo,omitempty"`
	Register         string            `yaml:"register,omitempty"` // Ansible-style alias for echo; merged in Load
	Set              map[string]string `yaml:"set,omitempty"`      // var -> CEL expression; computed without calling a tool (Ansible set_fact)
	Grab             string            `yaml:"grab,omitempty"`
	Loop             string            `yaml:"loop,omitempty"`
	Tags             []string          `yaml:"tags,omitempty"`
	IgnoreErrors     bool              `yaml:"ignore_errors,omitempty"`
	AllowDestructive bool              `yaml:"allow_destructive,omitempty"` // run this step even under --safe
	Expect           *Expect           `yaml:"expect,omitempty"`
	Result           []ResultItem      `yaml:"result,omitempty"`

	// block/rescue/always mirror Ansible's error handling. block runs until a
	// step fails; rescue runs on failure (a clean rescue recovers); always runs
	// regardless. Each is a nested step list.
	Block  []Step `yaml:"block,omitempty"`
	Rescue []Step `yaml:"rescue,omitempty"`
	Always []Step `yaml:"always,omitempty"`
}

// RetryConfig mirrors Ansible's retry/until/delay pattern.
// Retries is the number of additional attempts after the first (total = 1 + Retries).
// When Until is set but Retries is 0, defaults to 3 retries (4 total) matching Ansible.
// Delay defaults to 5s when unset.
type RetryConfig struct {
	Retries int    `yaml:"retries,omitempty"`
	Delay   string `yaml:"delay,omitempty"`
	Until   string `yaml:"until,omitempty"`
}

// Expect declares assertions checked after a step runs. play exits non-zero if any fail.
type Expect struct {
	Contains    string `yaml:"contains,omitempty"`
	Matches     string `yaml:"matches,omitempty"`
	Equals      string `yaml:"equals,omitempty"`
	IsError     *bool  `yaml:"is_error,omitempty"`
	Rule        string `yaml:"rule,omitempty"`         // CEL boolean expression; `output` and all vars are in scope
	Message     string `yaml:"message,omitempty"`      // custom failure message for Rule
	MaxDuration string `yaml:"max_duration,omitempty"` // fail if the tool call took longer than this (e.g. 500ms)
}

// LLMRound captures a sampling/createMessage exchange recorded during a session.
// Note: sampling is deprecated in the MCP 2026-07-28 spec; this block has a ~12-month window.
// Spec: https://modelcontextprotocol.io/specification
type LLMRound struct {
	Prompt   string `yaml:"prompt"`
	Response string `yaml:"response"`
	Model    string `yaml:"model,omitempty"`
}

type ResultItem struct {
	Type string `yaml:"type"`
	Text string `yaml:"text,omitempty"`
}

func Load(path string) (*File, error) {
	data, err := os.ReadFile(path) //#nosec G304 -- caller-supplied path is the point of this CLI tool
	if err != nil {
		return nil, err
	}
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		// FormatError already renders the source snippet; don't also print %w,
		// which would duplicate it.
		return nil, fmt.Errorf("%s", yaml.FormatError(err, true, true))
	}

	if len(f.Steps) == 0 && len(f.Tasks) > 0 {
		f.Steps = f.Tasks
	}
	if len(f.Steps) == 0 && len(f.Steps2) > 0 {
		f.Steps = f.Steps2
	}
	f.Tasks, f.Steps2 = nil, nil
	for i := range f.Steps {
		if f.Steps[i].Echo == "" && f.Steps[i].Register != "" {
			f.Steps[i].Echo = f.Steps[i].Register
		}
		f.Steps[i].Register = ""
	}

	if len(f.Servers) == 0 {
		if f.Server.Command != "" || f.Server.Name != "" || f.Server.URL != "" {
			f.Servers = map[string]Server{"default": f.Server}
			f.ServerOrder = []string{"default"}
		}
	} else {
		// Preserve servers: insertion order; Go maps don't, so re-read the keys.
		var ord struct {
			Servers yaml.MapSlice `yaml:"servers"`
		}
		if yaml.Unmarshal(data, &ord) == nil {
			for _, item := range ord.Servers {
				f.ServerOrder = append(f.ServerOrder, fmt.Sprint(item.Key))
			}
		}
	}

	return &f, nil
}

func Save(path string, f *File) error {
	data, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
