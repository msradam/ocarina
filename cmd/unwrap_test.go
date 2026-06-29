package cmd

import (
	"encoding/json"
	"testing"
)

func TestUnwrapStructured(t *testing.T) {
	parse := func(s string) any {
		var v any
		_ = json.Unmarshal([]byte(s), &v)
		return v
	}

	cases := []struct {
		name string
		in   any
		want string // JSON of expected output
	}{
		{"fastmcp json-string envelope", parse(`{"result":"{\"rows\":[[7]]}"}`), `{"rows":[[7]]}`},
		{"fastmcp json-array string", parse(`{"result":"[1,2,3]"}`), `[1,2,3]`},
		{"plain string under result is left alone", parse(`{"result":"hello"}`), `{"result":"hello"}`},
		{"genuine result object is left alone", parse(`{"result":{"a":1}}`), `{"result":{"a":1}}`},
		{"multi-key object untouched", parse(`{"rows":[[1]],"cols":["x"]}`), `{"cols":["x"],"rows":[[1]]}`},
		{"array untouched", parse(`[1,2,3]`), `[1,2,3]`},
		{"single non-result key untouched", parse(`{"data":"x"}`), `{"data":"x"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := json.Marshal(unwrapStructured(tc.in))
			if string(got) != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}
