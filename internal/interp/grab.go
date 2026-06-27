package interp

import (
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

// Grab extracts a value from JSON text using a gjson path.
//
// The leading dot convention from Ocarina's original syntax is preserved:
// ".0.sha" and "0.sha" both work. The full gjson path syntax is supported:
//
//	".name"                       — object key
//	".0"                          — array index
//	".items.0.title"              — nested path
//	"#[state==\"open\"].title"    — filter: first match
//	"#[state==\"open\"]#.title"   — filter: all matches
//	"#.name"                      — wildcard: all name values
func Grab(jsonText, path string) (string, error) {
	path = strings.TrimPrefix(path, ".")
	if path == "" {
		return jsonText, nil
	}

	trimmed := strings.TrimSpace(jsonText)
	if !gjson.Valid(trimmed) {
		// Some servers (e.g. mcp-server-sqlite) emit Python repr, not JSON.
		// Normalize it the same way the display layer does before giving up.
		if converted := PyReprToJSON(trimmed); converted != "" && gjson.Valid(converted) {
			trimmed = converted
		} else {
			return "", fmt.Errorf("grab: output is not valid JSON")
		}
	}

	result := gjson.Get(trimmed, path)
	if !result.Exists() {
		return "", fmt.Errorf("grab: path %q not found", path)
	}

	switch result.Type {
	case gjson.String:
		return result.Str, nil
	case gjson.Null:
		return "", nil
	default:
		// objects, arrays, bools, numbers — return raw JSON
		return result.Raw, nil
	}
}
