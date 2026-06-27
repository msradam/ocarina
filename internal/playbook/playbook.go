package playbook

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Cassette struct {
	Keys   map[string]string `yaml:"keys,omitempty"`
	Server Server            `yaml:"server"`
	Rondo  []Step            `yaml:"rondo"`
	LLM    []LLMRound        `yaml:"llm,omitempty"`
}

type Server struct {
	// Name is set when the rondo uses `server: <name>` (string form).
	// The caller is responsible for resolving Name to Command/Args/Env.
	Name    string
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
}

func (s *Server) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		s.Name = value.Value
		return nil
	}
	if value.Kind == yaml.MappingNode {
		type plain Server
		return value.Decode((*plain)(s))
	}
	return fmt.Errorf("server: expected a name (string) or a map with command/args/env")
}

type Step struct {
	Name          string         `yaml:"name,omitempty"`
	Tool          string         `yaml:"tool,omitempty"`
	Resource      string         `yaml:"resource,omitempty"`       // calls resources/read with this URI
	ListResources string         `yaml:"list_resources,omitempty"` // calls resources/list; echoes JSON URI array
	Sleep         string         `yaml:"sleep,omitempty"`          // pause execution, e.g. "2s", "500ms"
	Args          map[string]any `yaml:"args,omitempty"`
	Echo          string         `yaml:"echo,omitempty"`
	Grab          string         `yaml:"grab,omitempty"` // dot-path into JSON output: ".0.sha", ".name"
	Loop          string         `yaml:"loop,omitempty"` // {{key}} that resolves to a JSON array; sets {{item}} per iteration
	Tags          []string       `yaml:"tags,omitempty"`
	IgnoreErrors  bool           `yaml:"ignore_errors,omitempty"`
	Expect        *Expect        `yaml:"expect,omitempty"`
	Result        []ResultItem   `yaml:"result,omitempty"`
}

// Expect declares assertions checked during play. play exits non-zero if any fail.
type Expect struct {
	Contains string `yaml:"contains,omitempty"`
	Matches  string `yaml:"matches,omitempty"`
	Equals   string `yaml:"equals,omitempty"`
	IsError  *bool  `yaml:"is_error,omitempty"` // tool steps only
}

// LLMRound captures a sampling/createMessage exchange recorded during a session.
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
	return &c, yaml.Unmarshal(data, &c)
}

func Save(path string, c *Cassette) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
