package interp

import (
	"os"
	"testing"
)

func TestApplyString(t *testing.T) {
	vars := map[string]string{"name": "world", "x": "42"}
	got := Apply("hello {{name}}", vars).(string)
	if got != "hello world" {
		t.Fatalf("got %q", got)
	}
}

func TestApplyMultiple(t *testing.T) {
	vars := map[string]string{"a": "foo", "b": "bar"}
	got := Apply("{{a}}-{{b}}", vars).(string)
	if got != "foo-bar" {
		t.Fatalf("got %q", got)
	}
}

func TestApplyMap(t *testing.T) {
	vars := map[string]string{"k": "v"}
	got := Apply(map[string]any{"key": "{{k}}"}, vars).(map[string]any)
	if got["key"] != "v" {
		t.Fatalf("got %v", got)
	}
}

func TestApplyNonString(t *testing.T) {
	vars := map[string]string{}
	if Apply(42, vars).(int) != 42 {
		t.Fatal("int should pass through")
	}
	if Apply(true, vars).(bool) != true {
		t.Fatal("bool should pass through")
	}
}

func TestApplyEnvVar(t *testing.T) {
	t.Setenv("TEST_OCARINA_VAR", "envvalue")
	got := Apply("{{env.TEST_OCARINA_VAR}}", map[string]string{}).(string)
	if got != "envvalue" {
		t.Fatalf("got %q", got)
	}
}

func TestApplyEnvVarMissing(t *testing.T) {
	os.Unsetenv("TEST_OCARINA_MISSING")
	got := Apply("{{env.TEST_OCARINA_MISSING}}", map[string]string{}).(string)
	if got != "" {
		t.Fatalf("missing env var should resolve to empty string, got %q", got)
	}
}

func TestApplyEnvBeforeKeys(t *testing.T) {
	t.Setenv("TEST_OCARINA_PRIO", "fromenv")
	// env.X is a different namespace from X — both should resolve
	got := Apply("{{env.TEST_OCARINA_PRIO}} {{key}}", map[string]string{"key": "fromkey"}).(string)
	if got != "fromenv fromkey" {
		t.Fatalf("got %q", got)
	}
}

func TestStrings(t *testing.T) {
	vars := map[string]string{"v": "x"}
	got := Strings([]string{"a", "{{v}}", "c"}, vars)
	if got[1] != "x" {
		t.Fatalf("got %v", got)
	}
}

func TestStringMap(t *testing.T) {
	vars := map[string]string{"k": "replaced"}
	got := StringMap(map[string]string{"a": "{{k}}", "b": "static"}, vars)
	if got["a"] != "replaced" || got["b"] != "static" {
		t.Fatalf("got %v", got)
	}
}

func TestGrab(t *testing.T) {
	tests := []struct {
		json, path, want string
	}{
		{`{"name":"alice"}`, ".name", "alice"},
		{`[{"sha":"abc"}]`, ".0.sha", "abc"},
		{`[1,2,3]`, ".1", "2"},
		{`{"a":{"b":"deep"}}`, ".a.b", "deep"},
		{`"direct"`, ".", `"direct"`},
	}
	for _, tc := range tests {
		got, err := Grab(tc.json, tc.path)
		if err != nil {
			t.Errorf("Grab(%q, %q): %v", tc.json, tc.path, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Grab(%q, %q) = %q, want %q", tc.json, tc.path, got, tc.want)
		}
	}
}

func TestGrabErrors(t *testing.T) {
	if _, err := Grab("not json", ".x"); err == nil {
		t.Fatal("expected error for non-JSON")
	}
	if _, err := Grab(`{"a":1}`, ".missing"); err == nil {
		t.Fatal("expected error for missing key")
	}
	if _, err := Grab(`[1,2]`, ".5"); err == nil {
		t.Fatal("expected error for out-of-range index")
	}
}
