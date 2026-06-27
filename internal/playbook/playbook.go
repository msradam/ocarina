package playbook

import (
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
)

type Cassette struct {
	Keys   map[string]string `yaml:"keys,omitempty"`
	Server Server            `yaml:"server"`
	Rondo  []Step            `yaml:"rondo"`
	LLM    []LLMRound        `yaml:"llm,omitempty"`
}

type Server struct {
	// Name is set when the rondo uses `server: <name>` (string form).
	// Callers must resolve Name to Command/Args/Env before connecting.
	Name    string
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
}

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
	Name          string         `yaml:"name,omitempty"`
	Tool          string         `yaml:"tool,omitempty"`
	Resource      string         `yaml:"resource,omitempty"`
	ListResources string         `yaml:"list_resources,omitempty"`
	Sleep         string         `yaml:"sleep,omitempty"`
	Args          map[string]any `yaml:"args,omitempty"`
	When          string         `yaml:"when,omitempty"`
	Timeout       string         `yaml:"timeout,omitempty"`
	Retry         *RetryConfig   `yaml:"retry,omitempty"`
	Echo          string         `yaml:"echo,omitempty"`
	Grab          string         `yaml:"grab,omitempty"`
	Loop          string         `yaml:"loop,omitempty"`
	Tags          []string       `yaml:"tags,omitempty"`
	IgnoreErrors  bool           `yaml:"ignore_errors,omitempty"`
	Expect        *Expect        `yaml:"expect,omitempty"`
	Result        []ResultItem   `yaml:"result,omitempty"`
}

// RetryConfig mirrors Ansible's retry/until/delay pattern.
// Attempts is the number of retries after the first attempt (total = 1 + Attempts).
// When Until is set but Attempts is 0, defaults to 3 retries (4 total) matching Ansible.
// Delay defaults to 5s when unset.
type RetryConfig struct {
	Attempts int    `yaml:"attempts,omitempty"`
	Delay    string `yaml:"delay,omitempty"`
	Until    string `yaml:"until,omitempty"`
}

// Expect declares assertions checked after a step runs. play exits non-zero if any fail.
type Expect struct {
	Contains string `yaml:"contains,omitempty"`
	Matches  string `yaml:"matches,omitempty"`
	Equals   string `yaml:"equals,omitempty"`
	IsError  *bool  `yaml:"is_error,omitempty"`
	Rule     string `yaml:"rule,omitempty"`    // CEL boolean expression; `output` and all vars are in scope
	Message  string `yaml:"message,omitempty"` // custom failure message for Rule
}

// LLMRound captures a sampling/createMessage exchange recorded during a session.
// Note: sampling is deprecated in the MCP 2026-07-28 spec; this block has a ~12-month window.
type LLMRound struct {
	Prompt   string `yaml:"prompt"`
	Response string `yaml:"response"`
	Model    string `yaml:"model,omitempty"`
}

type ResultItem struct {
	Type string `yaml:"type"`
	Text string `yaml:"text,omitempty"`
}

func Load(path string) (*Cassette, error) {
	data, err := os.ReadFile(path) //#nosec G304 -- caller-supplied path is the point of this CLI tool
	if err != nil {
		return nil, err
	}
	var c Cassette
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("%w\n%s", err, yaml.FormatError(err, true, true))
	}
	return &c, nil
}

func Save(path string, c *Cassette) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
