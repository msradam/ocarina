package interp

import (
	"os"
	"regexp"
	"sort"
	"strings"
)

var (
	envRe    = regexp.MustCompile(`\{\{env\.([^}]+)\}\}`)
	varRefRe = regexp.MustCompile(`\{\{[\w.]+\}\}`)
)

// Apply recursively replaces {{key}} in all string values of val using vars.
// {{env.NAME}} is resolved directly from the calling process environment.
// Non-string leaves (int, bool, float64) pass through unchanged.
func Apply(val any, vars map[string]string) any {
	if val == nil {
		return val
	}
	switch v := val.(type) {
	case string:
		// resolve {{env.X}} from the process environment first
		v = envRe.ReplaceAllStringFunc(v, func(m string) string {
			return os.Getenv(envRe.FindStringSubmatch(m)[1])
		})
		// Iterate to a fixpoint so nested refs ({{a}} whose value is {{b}})
		// resolve fully and deterministically regardless of map order. The
		// cap bounds cyclic definitions (a->b->a).
		for i := 0; i <= len(vars); i++ {
			prev := v
			for k, replacement := range vars {
				v = strings.ReplaceAll(v, "{{"+k+"}}", replacement)
			}
			if v == prev {
				break
			}
		}
		return v
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, v2 := range v {
			out[k] = Apply(v2, vars)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, v2 := range v {
			out[i] = Apply(v2, vars)
		}
		return out
	default:
		return val
	}
}

// Unresolved returns the distinct {{var}} references still present in val
// after Apply, i.e. references that no key or prior echo: defined. Callers use
// it to fail a step rather than silently send literal braces to a tool.
func Unresolved(val any) []string {
	seen := map[string]bool{}
	var walk func(any)
	walk = func(x any) {
		switch v := x.(type) {
		case string:
			for _, m := range varRefRe.FindAllString(v, -1) {
				seen[m] = true
			}
		case map[string]any:
			for _, v2 := range v {
				walk(v2)
			}
		case []any:
			for _, v2 := range v {
				walk(v2)
			}
		}
	}
	walk(val)
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// Strings applies vars to each element of a string slice.
func Strings(ss []string, vars map[string]string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = Apply(s, vars).(string)
	}
	return out
}

// StringMap applies vars to each value of a string map.
func StringMap(m map[string]string, vars map[string]string) map[string]string {
	if len(m) == 0 {
		return m
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = Apply(v, vars).(string)
	}
	return out
}
