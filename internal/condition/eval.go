package condition

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/ext"
)

// EvalBool evaluates a CEL boolean expression against the current variable set.
// output is the step's text output; pass "" for when: (which runs before execution).
// vars is the current notes map (rondo vars + prior register: captures).
//
// Variables are injected at the top level so authors write:
//
//	when: "deploy_status == 'ready'"
//	expect:
//	  rule: "output.contains('success') && count != ''"
func EvalBool(expr string, vars map[string]string, output string) (bool, error) {
	prg, activation, err := compile(expr, vars, output)
	if err != nil {
		return false, err
	}
	result, _, err := prg.Eval(activation)
	if err != nil {
		return false, fmt.Errorf("expression %q failed: %w", expr, err)
	}
	b, ok := result.Value().(bool)
	if !ok {
		return false, fmt.Errorf("expression %q returned %T, must return bool", expr, result.Value())
	}
	return b, nil
}

// EvalString evaluates a CEL expression and returns its result rendered as a
// string. set: uses this to compute a variable without calling a tool. Values
// are injected as strings (like EvalBool), so this composes and slices strings;
// arithmetic on numeric-looking vars is not yet supported.
func EvalString(expr string, vars map[string]string, output string) (string, error) {
	prg, activation, err := compile(expr, vars, output)
	if err != nil {
		return "", err
	}
	result, _, err := prg.Eval(activation)
	if err != nil {
		return "", fmt.Errorf("expression %q failed: %w", expr, err)
	}
	return fmt.Sprint(result.Value()), nil
}

// compile builds a CEL program binding each var as a string and output as the
// parsed object when it is structured JSON (else a string), matching how
// when:/until:/rule:/set: expressions are written.
func compile(expr string, vars map[string]string, output string) (cel.Program, map[string]any, error) {
	if expr == "" {
		return nil, nil, fmt.Errorf("empty conditional expression is not allowed")
	}
	if strings.Contains(expr, "{{") {
		return nil, nil, fmt.Errorf("expressions use bare variable names, not {{...}} — write e.g. %q not %q", "tz == 'UTC'", "{{tz}} == 'UTC'")
	}

	// When the step's output is structured JSON (an object or array), bind
	// `output` as that value so rule:/until: can select fields
	// (output.total == 2, output.hits.all(h, h.ok)). gjson-based grab: already
	// parses the same JSON; this makes CEL consistent with it. Plain text output
	// stays a string, so output.contains('OK') keeps working.
	outVal, outType := any(output), cel.StringType
	var parsed any
	if json.Unmarshal([]byte(output), &parsed) == nil {
		switch parsed.(type) {
		case map[string]any, []any:
			outVal, outType = parsed, cel.DynType
		}
	}

	opts := []cel.EnvOption{
		cel.Variable("output", outType),
		ext.Strings(),
		// JSON numbers decode to doubles; allow output.total == 2 (int literal)
		// to compare against them without forcing authors to write 2.0.
		cel.CrossTypeNumericComparisons(true),
	}
	activation := map[string]any{
		"output": outVal,
	}
	for k, v := range vars {
		opts = append(opts, cel.Variable(k, cel.StringType))
		activation[k] = v
	}

	env, err := cel.NewEnv(opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("cel env: %w", err)
	}
	ast, issues := env.Compile(expr)
	if issues != nil && issues.Err() != nil {
		return nil, nil, fmt.Errorf("invalid expression %q: %w", expr, issues.Err())
	}
	prg, err := env.Program(ast)
	if err != nil {
		return nil, nil, fmt.Errorf("cel program: %w", err)
	}
	return prg, activation, nil
}

// CheckSyntax parses a CEL expression for syntax errors without evaluating it.
// Used by ocarina validate to catch bad expressions before any MCP calls run.
func CheckSyntax(expr string) error {
	if expr == "" {
		return fmt.Errorf("empty conditional expression is not allowed")
	}
	if strings.Contains(expr, "{{") {
		return fmt.Errorf("expressions use bare variable names, not {{...}} — write e.g. %q not %q", "tz == 'UTC'", "{{tz}} == 'UTC'")
	}
	// Parse-only environment: we don't know the variable names at validate time,
	// so we skip type-checking and only verify syntactic correctness.
	env, err := cel.NewEnv(ext.Strings())
	if err != nil {
		return err
	}
	_, issues := env.Parse(expr)
	if issues != nil && issues.Err() != nil {
		return fmt.Errorf("invalid expression %q: %w", expr, issues.Err())
	}
	return nil
}
