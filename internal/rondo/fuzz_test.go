package rondo

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzLoad feeds arbitrary bytes to the loader. Malformed input must return an
// error, never panic.
func FuzzLoad(f *testing.F) {
	f.Add([]byte("server: {command: x}\nrondo:\n  - tool: t\n"))
	f.Add([]byte("servers:\n  a: {url: http://x}\nrondo:\n  - server: a\n    tool: t\n"))
	f.Add([]byte("tasks:\n  - register: r\n    motif: m.yaml\n    with: {k: v}\n"))
	f.Add([]byte("rondo:\n  - block:\n      - tool: a\n    rescue:\n      - tool: b\n"))
	f.Add([]byte("params:\n  - name: p\n    required: true\n"))
	f.Add([]byte(""))
	f.Add([]byte("\x00\x00not yaml"))

	f.Fuzz(func(t *testing.T, data []byte) {
		p := filepath.Join(t.TempDir(), "r.yaml")
		if err := os.WriteFile(p, data, 0600); err != nil {
			t.Skip()
		}
		_, _ = Load(p) // an error is fine; a panic is not
	})
}
