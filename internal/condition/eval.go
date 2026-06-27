package condition

import (
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
	if expr == "" {
		return false, fmt.Errorf("empty conditional expression is not allowed")
	}
	if strings.Contains(expr, "{{") {
		return false, fmt.Errorf("expressions use bare variable names, not {{...}} — write e.g. %q not %q", "tz == 'UTC'", "{{tz}} == 'UTC'")
	}

	opts := []cel.EnvOption{
		cel.Variable("output", cel.StringType),
		ext.Strings(),
	}
	activation := map[string]any{
		"output": output,
	}
	for k, v := range vars {
		opts = append(opts, cel.Variable(k, cel.StringType))
		activation[k] = v
	}

	env, err := cel.NewEnv(opts...)
	if err != nil {
		return false, fmt.Errorf("cel env: %w", err)
	}

	ast, issues := env.Compile(expr)
	if issues != nil && issues.Err() != nil {
		return false, fmt.Errorf("invalid expression %q: %w", expr, issues.Err())
	}

	prg, err := env.Program(ast)
	if err != nil {
		return false, fmt.Errorf("cel program: %w", err)
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
