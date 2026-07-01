# Ocarina architecture

Ocarina is a YAML-driven automation framework for MCP servers. It sits in the same place relative to MCP that Ansible sits relative to SSH: an LLM-free execution engine that composes protocol primitives into repeatable workflows.

```
   rondo.yaml   keys / servers / rondo: [ steps... ]
        │   written by a human or an agent
        ▼
┌─────────────────────────────────────────────────────────┐
│  Ocarina  (single binary, no LLM)                       │
│                                                         │
│    Parser      YAML  →  File                            │
│    Engine      step runner · when · loop · retry        │
│                motif · block / rescue / always          │
│    Assert      grab · echo · expect · snapshot · --safe │
│    Report      text · json · junit · OTLP traces        │
│    MCP client  initialize → tools/list → tools/call     │
└────────────────────────────┬────────────────────────────┘
                             │   stdio  or  Streamable HTTP
                             ▼
   MCP server(s)   local subprocess  or  remote url + headers
   sqlite · github · fetch · blender · ...
```

The same engine backs two front ends. `ocarina play` runs a rondo from the command line. `ocarina serve` exposes one or more rondos as composite MCP tools, so the engine runs a rondo in response to an incoming tool call. A rondo can target more than one server; each is declared under `servers:` and selected per step with `server:`. Ocarina connects to each referenced server once and reuses the session.

---

## Protocol mapping

Every Ocarina feature maps to a specific MCP method. Nothing is invented outside the wire protocol.

### Step types

| Rondo field | MCP method | Notes |
|---|---|---|
| `tool:` | `tools/call` | The common step. Args are checked against `inputSchema` before the call. |
| `resource:` | `resources/read` | The URI is interpolated from `keys:` and prior captures. |
| `list_resources:` | `resources/list` | Returns a JSON array of URIs, usable by `grab:`, `echo:`, and `loop:`. |
| `sleep:` | none | A local pause to pace a run. No protocol call. |
| `set:` | none | Compute vars from CEL expressions without a call (Ansible `set_fact`). Keys evaluate in sorted order, so one can reference another. |
| `motif:` | none | Include another rondo file inline (see Composition). |
| `block:` / `rescue:` / `always:` | none | Error handling over a nested step list (see Composition). |

### Assertions (`expect:`)

| Assertion | Source |
|---|---|
| `contains: "str"` | substring of the joined text content |
| `matches: "regex"` | regex over the text content |
| `equals: "str"` | exact match (whitespace-trimmed) |
| `is_error: bool` | `CallToolResult.isError` |
| `rule: "CEL"` | a CEL boolean over `output` and the current variables. Structured JSON output binds `output` as the parsed object (`output.total == 2`); text output binds it as the string |
| `message: "str"` | the failure message printed when `rule` is false |
| `max_duration: "500ms"` | fail if the tool call took longer; times the successful attempt, excluding retry backoff |

`isError` is a protocol-level field on every `tools/call` response. A result with `isError: true` is a valid JSON-RPC response: the tool ran but reported failure. Ocarina treats it as a step failure by default, unless the step sets `ignore_errors: true` or asserts `expect: is_error: true`.

Many servers do not set `isError` and instead return an error as ordinary text. For those, pin the failure down with an `expect:` assertion.

### Variable capture (`grab:` and `echo:`)

`grab:` takes a single [gjson](https://github.com/tidwall/gjson) path (`.0.sha`, `.name`, `#.title`) and extracts a value from the step's JSON output. `echo:` stores that value (or the whole output, if no `grab:`) into the key map under a name, for use in later steps via `{{key}}`. When a tool returns `structuredContent`, that typed JSON is the output `grab` and `expect` run against, rather than the text block. When a server instead returns a Python repr, Ocarina normalizes it to JSON before applying the path.

### Dynamic loops over server inventory

`list_resources:` returns the server's own inventory. Capture it and feed it into `loop:`, and the server defines its own iteration space.

```yaml
rondo:
  - name: list namespaces
    list_resources: k8s
    grab: "#.uri"
    echo: namespaces

  - name: pod health per namespace
    tool: kubectl_get_by_kind_in_namespace
    loop: "{{namespaces}}"
    args:
      kind: pod
      namespace: "{{item}}"
    expect:
      contains: Running
```

Ansible's inventory is external and static. Here the iteration space comes from the live server.

---

## Composition

### Motif (`motif:` + `with:`)

A motif is a reusable rondo fragment, the equivalent of an Ansible role or a pytest fixture. A `motif:` step loads another rondo file and runs its steps inline, recursively, with a depth guard against include cycles. The fragment declares its own `keys:` as defaults; the caller passes `with:` parameters, evaluated in the caller's scope. The motif is isolated: it sees only its own keys overlaid by `with:`, not the caller's captures, so it stays a clean building block. Motif steps run against the including rondo's servers.

### Error handling (`block:` / `rescue:` / `always:`)

Ocarina mirrors Ansible's error handling. `block:` runs its steps until one fails, then stops. On failure, `rescue:` runs; a clean rescue recovers, so the run continues and the failure is cleared from the exit status. `always:` runs regardless of outcome, which is where teardown of anything the block created belongs. The orchestration is a pure function (`runBlock`), so the recover semantics are unit-tested independently of any server.

---

## Serve: rondos as composite MCP tools

`ocarina serve` registers each rondo as a single MCP tool. The rondo's `params:` become the tool's JSON Schema `inputSchema`, `return:` names the captured key handed back as the result, and the rondo's own `server:` block is the downstream it drives. A call builds the variable scope from `keys:` (defaults), `params:` defaults, then the caller's arguments, and runs the rondo's steps through the same engine `play` uses. This mints a deterministic, higher-level tool over an existing server without changing that server.

Two transports:

- **stdio** (default): the host launches `ocarina serve` as a subprocess.
- **Streamable HTTP** (`--http <addr>`): a long-running server. `--token` (or `OCARINA_TOKEN`) requires a bearer token compared in constant time; `--tls-cert` / `--tls-key` enable HTTPS.

Each call runs under a hard `--timeout` and a `--max-concurrent` semaphore, with a panic guard that turns a failed call into an `isError` result instead of crashing the server. The HTTP server shuts down gracefully on SIGINT/SIGTERM.

---

## Safe mode (`--safe`)

`play --safe` and `serve --safe` refuse any tool whose MCP annotations do not mark it `readOnlyHint: true`. The posture is conservative: a tool with no annotations is treated as not read-only and refused. A step opts back in with `allow_destructive: true`. The engine captures each tool's annotations from `tools/list` and checks them before dispatch.

This is a guardrail, not a security boundary. MCP annotations are advisory, and a server can misreport them, so `--safe` protects against mistakes, not against a hostile server.

---

## Output

The engine records every leaf step's outcome (name, server, tool, status, message, duration) into a result object. `play` reports it two ways, plus an open telemetry path:

- **text** (default): a per-step trace and a final `N passed, M failed, K skipped` tally.
- **`--output junit`**: JUnit XML, the format CI test dashboards ingest (GitLab reports, Jenkins, the common GitHub Actions reporters). One rondo is a `<testsuite>`; each step is a `<testcase>`.
- **`--output json`**: the full structured result, per-step plus run totals.
- **OTLP traces**: when `OTEL_EXPORTER_OTLP_ENDPOINT` (or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`) is set, `play` exports the run as OpenTelemetry traces, a root span for the run and a child span per step, using the OTLP/JSON-over-HTTP encoding. This uses the standard library only and adds no dependencies. `OTEL_EXPORTER_OTLP_HEADERS` carries auth to a hosted collector.

The run's exit code derives from the failures list, kept separate from the per-step records. A step rescued by a `block:` shows `status: failed` in the report while the run still reports `ok: true` and exits 0.

---

## CLI surface

```
ocarina docs   <server...>                       # markdown docs from a live server
ocarina record <out.yaml> <server...>            # proxy a session into a rondo
ocarina play   <file.yaml>                        # execute a rondo
ocarina play   <file.yaml> --output junit         # JUnit XML for CI dashboards
ocarina play   <file.yaml> --snapshot             # assert output against recorded result: blocks
ocarina play   <file.yaml> --update               # re-baseline result: blocks
ocarina play   <file.yaml> --data rows.csv        # run once per data row (columns become keys)
ocarina play   <file.yaml> --safe                 # refuse non-read-only tools
ocarina play   <file.yaml> --trace                # log every JSON-RPC frame to stderr
ocarina play   <file.yaml> --tags smoke -e repo=acme
ocarina serve  <file.yaml>... [--http :8080]      # rondos as composite MCP tools
ocarina validate <file.yaml>                     # static + live-schema checks, no tool calls
ocarina diff   <file.yaml>                        # compare against current server schemas
ocarina lock   <file.yaml>                        # snapshot the schema; --check fails on drift
ocarina load   <file.yaml> --vus 20 --duration 30s   # concurrent load test
ocarina hum    <server...> -- <tool> [key=value] # ad-hoc single tool call
```

`ocarina hum` is the ad-hoc equivalent of `ansible -m <module>`: connect, call one tool, print the result, exit. No rondo file required.

---

## Internal components

### Parser (`internal/rondo/`)

Deserializes YAML into a `File`. Normalizes the input: a single `server:` block becomes a one-entry `Servers` map, `tasks:` and `steps:` are merged into the step list as aliases for `rondo:`, and `register:` is folded into `echo:`. Does not connect to any server.

```go
type File struct {
    Keys    map[string]string `yaml:"keys"`
    Servers map[string]Server `yaml:"servers"`
    Server  Server            `yaml:"server"`  // shorthand for a single server
    Steps   []Step            `yaml:"rondo"`

    // used when the rondo is served as a composite tool
    Name        string  `yaml:"name"`
    Description string  `yaml:"description"`
    Params      []Param `yaml:"params"`
    Return      string  `yaml:"return"`
}

type Step struct {
    Name             string         `yaml:"name"`
    Server           string         `yaml:"server"`     // which entry in Servers
    Motif            string         `yaml:"motif"`      // include another rondo inline
    With             map[string]string `yaml:"with"`    // params for the motif
    Tool             string         `yaml:"tool"`
    Resource         string         `yaml:"resource"`
    ListResources    string         `yaml:"list_resources"`
    Sleep            string         `yaml:"sleep"`
    Set              map[string]string `yaml:"set"`     // var -> CEL expression (set_fact)
    Args             map[string]any `yaml:"args"`
    Grab             string         `yaml:"grab"`       // a gjson path
    Echo             string         `yaml:"echo"`       // register: is an alias
    Expect           *Expect        `yaml:"expect"`
    When             string         `yaml:"when"`       // CEL
    Loop             string         `yaml:"loop"`
    Timeout          string         `yaml:"timeout"`
    Retry            *RetryConfig   `yaml:"retry"`
    Tags             []string       `yaml:"tags"`
    IgnoreErrors     bool           `yaml:"ignore_errors"`
    AllowDestructive bool           `yaml:"allow_destructive"`
    Block            []Step         `yaml:"block"`
    Rescue           []Step         `yaml:"rescue"`
    Always           []Step         `yaml:"always"`
}
```

### MCP client (`internal/mcpclient/`)

A thin wrapper over the MCP wire protocol. A server runs over stdio (a local subprocess) or the Streamable HTTP transport (a remote `url:`, with `headers:` such as a bearer token sent on every request). It runs the `initialize` handshake, lists tools (draining the paginated iterator), calls tools, reads resources, and lists resources and resource templates. Ocarina declares no `sampling`, `elicitation`, or `roots` client capabilities, so a server cannot issue requests Ocarina cannot service.

### Engine (`cmd/engine.go`)

The shared step runner. `play` and `serve` both build an `engine` and call `runSteps`, which returns the list of failures and records per-step results. For each leaf step it:

1. Matches tags against `--tags` / `--skip-tags`.
2. Expands `loop:` into one iteration per array element, binding `{{item}}`.
3. Evaluates `when:`; skips the step if it is false.
4. Connects to the step's server (lazily, once) and checks the tool exists with its required args present, and, under `--safe`, that it is read-only.
5. Interpolates `{{key}}` in args and URIs; an unresolved reference fails the step.
6. Dispatches `tools/call` or `resources/read`, honoring `timeout:` and `retry:`.
7. Treats a dispatch error or `isError: true` as a failure unless `ignore_errors:` is set.
8. Applies `grab:`, captures with `echo:`, then runs `expect:` assertions.

`motif:` and `block:`/`rescue:`/`always:` steps recurse through `runSteps`. A failure is recorded and the process exits non-zero at the end. `ignore_errors: true` keeps the run going past a failing step.

### Validator (`cmd/validate.go`)

Static analysis that connects to fetch schemas but calls no tools:

- every `tool:` exists on its server, with required `args:` present and types matching `inputSchema`
- every `{{key}}` resolves from `keys:`, `params:`, `{{env.X}}`, or a prior `echo:`
- `when:`, `retry.until:`, `expect.rule:`, and each `set:` expression parse as CEL; `timeout:`, `sleep:`, and `expect.max_duration:` parse as durations
- every `server:` reference exists in the `servers:` map
- `block:`/`rescue:`/`always:` groups are flattened so their sub-steps are checked

`ocarina diff` is the companion: it compares the rondo's tools against the server's current schemas and reports removed tools, newly required args, undefined server references, and new tools the server now offers.

---

## What Ocarina does not implement

| MCP feature | Reason omitted |
|---|---|
| `sampling/createMessage` | Requires an LLM. Ocarina is LLM-free by design. (Recorded exchanges are stored in `llm:` for reference but never replayed.) |
| `elicitation/create` | Requires interactive input mid-run. Incompatible with deterministic replay. |
| `roots/list` | A server-to-client capability. Ocarina exposes no filesystem to servers. |
| `prompts/*` | Prompt templates are LLM-facing. No role in deterministic tool execution. |
| `completion/complete` | Argument autocomplete for LLM UIs. Not relevant to authored rondos. |

---

## Key invariants

1. **A rondo is deterministic.** Given the same `keys:` and the same server state, `ocarina play` produces the same result every time. No LLM, no randomness.
2. **The rondo is a spec, not a recording.** It does not store outputs. This is what separates Ocarina from VCR-style replay tools.
3. **The server is the source of truth.** Tool schemas, resource URIs, and templates come from the live server at run time. The rondo does not duplicate them.
4. **Failures are explicit.** A dispatch error, `isError: true`, a failed `expect:`, an unresolved `{{key}}`, and a missing `grab:` path all fail the step unless `ignore_errors: true` is set.
5. **The engine is shared.** `play` and `serve` run rondos through the same code path, so a served composite tool behaves exactly like the command-line run.
