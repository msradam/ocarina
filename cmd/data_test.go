package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDataRows(t *testing.T) {
	dir := t.TempDir()

	csv := filepath.Join(dir, "d.csv")
	if err := os.WriteFile(csv, []byte("tz,label\nUTC,zulu\nAsia/Tokyo,jp\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := loadDataRows(csv)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0]["tz"] != "UTC" || rows[1]["label"] != "jp" {
		t.Fatalf("csv rows wrong: %+v", rows)
	}

	js := filepath.Join(dir, "d.json")
	if err := os.WriteFile(js, []byte(`[{"tz":"UTC","n":1},{"tz":"CET"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err = loadDataRows(js)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0]["n"] != "1" || rows[1]["tz"] != "CET" {
		t.Fatalf("json rows wrong: %+v", rows)
	}
}

func TestMergeNotesPrecedence(t *testing.T) {
	base := map[string]string{"a": "base", "b": "base"}
	row := map[string]string{"b": "row", "c": "row"}
	extras := map[string]string{"c": "extra"}
	n := mergeNotes(base, row, extras)
	if n["a"] != "base" || n["b"] != "row" || n["c"] != "extra" {
		t.Fatalf("precedence wrong (want base/row/extra): %+v", n)
	}
}
