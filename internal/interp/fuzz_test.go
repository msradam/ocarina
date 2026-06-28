package interp

import "testing"

// FuzzGrab feeds arbitrary JSON text and gjson paths. Bad input must error or
// return empty, never panic.
func FuzzGrab(f *testing.F) {
	f.Add(`{"a":{"b":1}}`, "a.b")
	f.Add(`[1,2,3]`, "0")
	f.Add(`{"items":[{"sha":"x"}]}`, "items.0.sha")
	f.Add(``, "")
	f.Add(`not json`, "..##")
	f.Fuzz(func(t *testing.T, jsonText, path string) {
		_, _ = Grab(jsonText, path)
	})
}

// FuzzApply feeds arbitrary template text and a value. Interpolation and the
// unresolved-key scan must never panic.
func FuzzApply(f *testing.F) {
	f.Add("hello {{name}}", "world")
	f.Add("{{a}}{{b}}{{env.X}}", "v")
	f.Add("{{", "v")
	f.Add("}}{{}}{{", "")
	f.Fuzz(func(t *testing.T, tmpl, val string) {
		out := Apply(tmpl, map[string]string{"name": val, "a": val})
		if s, ok := out.(string); ok {
			_ = Unresolved(s)
		}
		_ = PyReprToJSON(tmpl)
	})
}
