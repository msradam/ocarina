package playbook

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Cassette struct {
	Notes  map[string]string `yaml:"notes,omitempty"`
	Server Server            `yaml:"server"`
	Tracks []Track           `yaml:"tracks"`
	LLM    []LLMRound        `yaml:"llm,omitempty"`
}

type Server struct {
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
}

type Track struct {
	Name   string         `yaml:"name,omitempty"`
	Tool   string         `yaml:"tool"`
	Args   map[string]any `yaml:"args,omitempty"`
	Echo   string         `yaml:"echo,omitempty"`
	Grab   string         `yaml:"grab,omitempty"` // dot-path into JSON output: ".0.sha", ".name"
	Expect *Expect        `yaml:"expect,omitempty"`
	Result []ResultItem   `yaml:"result,omitempty"`
}

// Expect declares assertions checked during play. play exits non-zero if any fail.
type Expect struct {
	Contains string `yaml:"contains,omitempty"` // output must contain this substring
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
