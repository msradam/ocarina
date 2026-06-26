package interp

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Grab extracts a value from JSON text using a dot-path like ".name", ".0.sha", ".items.0.title".
func Grab(jsonText, path string) (string, error) {
	var root any
	if err := json.Unmarshal([]byte(strings.TrimSpace(jsonText)), &root); err != nil {
		return "", fmt.Errorf("grab: output is not JSON: %w", err)
	}

	path = strings.TrimPrefix(path, ".")
	if path == "" {
		return jsonText, nil
	}

	cur := root
	for _, part := range strings.Split(path, ".") {
		switch v := cur.(type) {
		case map[string]any:
			val, ok := v[part]
			if !ok {
				return "", fmt.Errorf("grab: key %q not found", part)
			}
			cur = val
		case []any:
			idx, err := strconv.Atoi(part)
			if err != nil || idx < 0 || idx >= len(v) {
				return "", fmt.Errorf("grab: index %q out of range (len %d)", part, len(v))
			}
			cur = v[idx]
		default:
			return "", fmt.Errorf("grab: cannot traverse %T with key %q", cur, part)
		}
	}

	switch v := cur.(type) {
	case string:
		return v, nil
	case nil:
		return "", nil
	default:
		b, _ := json.Marshal(v)
		return string(b), nil
	}
}
