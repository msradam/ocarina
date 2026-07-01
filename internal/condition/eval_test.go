package condition

import "testing"

func TestEvalBool(t *testing.T) {
	cases := []struct {
		name    string
		expr    string
		vars    map[string]string
		output  string
		want    bool
		wantErr bool
	}{
		{"var equality", "status == 'ready'", map[string]string{"status": "ready"}, "", true, false},
		{"var inequality", "status == 'ready'", map[string]string{"status": "pending"}, "", false, false},
		{"output contains", "output.contains('OK')", nil, "all OK here", true, false},
		{"string ext startsWith", "name.startsWith('oca')", map[string]string{"name": "ocarina"}, "", true, false},
		{"compound and", "a == '1' && b == '2'", map[string]string{"a": "1", "b": "2"}, "", true, false},
		{"empty expr errors", "", nil, "", false, true},
		{"mustache rejected", "{{tz}} == 'UTC'", map[string]string{"tz": "UTC"}, "", false, true},
		{"non-bool result errors", "output", nil, "text", false, true},
		{"unknown identifier errors", "nope == 'x'", nil, "", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EvalBool(tc.expr, tc.vars, tc.output)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEvalBoolStructured(t *testing.T) {
	obj := `{"total":2,"items":["a","b"],"hits":[{"ok":true},{"ok":true}]}`
	cases := []struct {
		name string
		expr string
		out  string
		want bool
	}{
		{"field select string method", "output.items.size() == 2", obj, true},
		{"list all", "output.hits.all(h, h.ok)", obj, true},
		{"nested index", "output.items[0] == 'a'", obj, true},
		{"numeric double literal", "output.total == 2.0", obj, true},
		{"numeric int literal", "output.total == 2", obj, true},
		{"numeric compare", "output.total > 1", obj, true},
		{"array output", "output.size() == 3", `[1,2,3]`, true},
		{"plain text stays string", "output.contains('OK')", "all OK", true},
		{"json scalar stays string", "output == 'hello'", `"hello"`, false}, // unmarshals to string, not treated as structured
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EvalBool(tc.expr, nil, tc.out)
			if err != nil {
				t.Fatalf("%s: %v", tc.expr, err)
			}
			if got != tc.want {
				t.Fatalf("%s = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

func TestEvalString(t *testing.T) {
	cases := []struct {
		name   string
		expr   string
		vars   map[string]string
		output string
		want   string
	}{
		{"concat vars", "'Asia/' + city", map[string]string{"city": "Tokyo"}, "", "Asia/Tokyo"},
		{"ternary", "n == '1' ? 'one' : 'many'", map[string]string{"n": "1"}, "", "one"},
		{"upper ext", "name.upperAscii()", map[string]string{"name": "oca"}, "", "OCA"},
		{"from output", "output + '!'", nil, "done", "done!"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EvalString(tc.expr, tc.vars, tc.output)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckSyntax(t *testing.T) {
	// CheckSyntax runs at validate time without knowing variable names, so it
	// accepts unknown identifiers but rejects malformed syntax.
	if err := CheckSyntax("a == 'b' && c.contains('d')"); err != nil {
		t.Fatalf("valid expression rejected: %v", err)
	}
	if err := CheckSyntax("a == == b"); err == nil {
		t.Fatal("malformed expression should fail syntax check")
	}
	if err := CheckSyntax("{{a}} == 'b'"); err == nil {
		t.Fatal("mustache syntax should be rejected with a helpful message")
	}
}
