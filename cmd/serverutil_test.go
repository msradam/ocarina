package cmd

import (
	"testing"

	"github.com/msradam/ocarina/internal/rondo"
)

// A rondo whose only top-level step is a block must still report the servers
// its sub-steps target, or validate/diff connect to nothing and then report
// those sub-steps' server as undefined.
func TestReferencedServerKeysDescendsBlocks(t *testing.T) {
	f := &rondo.File{
		Servers:     map[string]rondo.Server{"default": {Command: "x"}},
		ServerOrder: []string{"default"},
		Steps: []rondo.Step{
			{
				Block:  []rondo.Step{{Tool: "a"}},
				Rescue: []rondo.Step{{Tool: "b"}},
				Always: []rondo.Step{{Resource: "r"}},
			},
		},
	}
	keys := referencedServerKeys(f)
	if !keys["default"] {
		t.Fatalf("block sub-steps target the default server; got %v", keys)
	}

	// nested blocks resolve too
	f.Steps = []rondo.Step{{Block: []rondo.Step{{Block: []rondo.Step{{Tool: "deep"}}}}}}
	if keys := referencedServerKeys(f); !keys["default"] {
		t.Fatalf("nested block sub-steps should resolve; got %v", keys)
	}
}
