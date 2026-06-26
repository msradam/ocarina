package interp

import "strings"

// Apply recursively replaces {{key}} in all string values of val using vars.
// Non-string leaves (int, bool, float64) pass through unchanged.
func Apply(val any, vars map[string]string) any {
	if val == nil || len(vars) == 0 {
		return val
	}
	switch v := val.(type) {
	case string:
		for k, replacement := range vars {
			v = strings.ReplaceAll(v, "{{"+k+"}}", replacement)
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

// Strings applies vars to each element of a string slice.
func Strings(ss []string, vars map[string]string) []string {
	if len(vars) == 0 {
		return ss
	}
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = Apply(s, vars).(string)
	}
	return out
}
