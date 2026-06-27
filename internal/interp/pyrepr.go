package interp

import "strings"

// PyReprToJSON converts Python dict/list repr to JSON. Some MCP servers
// (e.g. mcp-server-sqlite) return str() of Python objects rather than JSON.
// Only handles the common case where string values contain no embedded single
// quotes. Returns "" if s doesn't look like a Python list/dict.
func PyReprToJSON(s string) string {
	s = strings.TrimSpace(s)
	if len(s) == 0 || (s[0] != '[' && s[0] != '{') {
		return ""
	}
	s = strings.NewReplacer(
		": True", ": true",
		": False", ": false",
		": None", ": null",
		"[True", "[true",
		"[False", "[false",
		"[None", "[null",
	).Replace(s)
	// swap single-quoted strings to double-quoted
	var b strings.Builder
	inSingle := false
	for _, c := range s {
		switch {
		case c == '\'' && !inSingle:
			inSingle = true
			b.WriteRune('"')
		case c == '\'' && inSingle:
			inSingle = false
			b.WriteRune('"')
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}
