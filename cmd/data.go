package cmd

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// loadDataRows reads a CSV (header row + data rows) or JSON (array of objects)
// file into one string map per row, for data-driven play. Column names / object
// keys become {{keys}} for that iteration.
func loadDataRows(path string) ([]map[string]string, error) {
	data, err := os.ReadFile(path) //#nosec G304 -- user-supplied data file is the point
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		var arr []map[string]any
		if err := json.Unmarshal(data, &arr); err != nil {
			return nil, fmt.Errorf("data file %s: expected a JSON array of objects: %w", path, err)
		}
		rows := make([]map[string]string, len(arr))
		for i, obj := range arr {
			row := make(map[string]string, len(obj))
			for k, v := range obj {
				row[k] = fmt.Sprint(v)
			}
			rows[i] = row
		}
		return rows, nil
	case ".csv":
		records, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
		if err != nil {
			return nil, fmt.Errorf("data file %s: %w", path, err)
		}
		if len(records) < 2 {
			return nil, fmt.Errorf("data file %s: need a header row and at least one data row", path)
		}
		header := records[0]
		rows := make([]map[string]string, 0, len(records)-1)
		for _, rec := range records[1:] {
			row := make(map[string]string, len(header))
			for i, h := range header {
				if i < len(rec) {
					row[h] = rec[i]
				}
			}
			rows = append(rows, row)
		}
		return rows, nil
	default:
		return nil, fmt.Errorf("data file %s: unsupported extension (use .csv or .json)", path)
	}
}

// dataLabel identifies a data-driven run so recorded step names (and the JUnit
// testcases derived from them) map back to the input row that produced them.
func dataLabel(i, n int, row map[string]string) string {
	s := fmt.Sprintf("row %d/%d", i+1, n)
	if len(row) > 0 {
		parts := make([]string, 0, len(row))
		for _, k := range sortedKeys(row) {
			parts = append(parts, k+"="+row[k])
		}
		s += " " + strings.Join(parts, ",")
	}
	return s
}

// mergeNotes composes a run's variable scope: rondo keys (defaults), then the
// current data row, then -e overrides (which always win).
func mergeNotes(base, row, extras map[string]string) map[string]string {
	n := make(map[string]string, len(base)+len(row)+len(extras))
	for k, v := range base {
		n[k] = v
	}
	for k, v := range row {
		n[k] = v
	}
	for k, v := range extras {
		n[k] = v
	}
	return n
}
