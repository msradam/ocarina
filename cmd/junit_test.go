package cmd

import (
	"strings"
	"testing"
)

func TestJUnitXML(t *testing.T) {
	r := runResult{
		Total: 3, Passed: 1, Failed: 1, Skipped: 1, DurationMS: 1500,
		Steps: []stepResult{
			{Name: "ok step", Server: "time", Status: "ok", DurationMS: 500},
			{Name: "bad step", Server: "time", Status: "failed", Message: "boom", DurationMS: 1000},
			{Name: "skip step", Status: "skipped", Message: "when false"},
		},
	}
	out, err := junitXML("suite.yaml", r)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		`<?xml version="1.0"`,
		`tests="3" failures="1" skipped="1"`,
		`name="ok step"`,
		`<failure message="boom">`,
		`<skipped message="when false">`,
		`classname="time"`,
		`classname="ocarina"`, // the skipped step has no server; falls back
	} {
		if !strings.Contains(s, want) {
			t.Errorf("junit output missing %q\n%s", want, s)
		}
	}
}
