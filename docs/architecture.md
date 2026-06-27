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
│    Executor    step runner · when · loop · retry        │
│    Assert      grab · echo · expect                     │
│    MCP client  initialize → tools/list → tools/call     │
└────────────────────────────┬────────────────────────────┘
                             │   stdio  or  Streamable HTTP
                             ▼
   MCP server(s)   local subprocess  or  remote url + headers
   sqlite · github · fetch · blender · ...
```

A rondo can target more than one server. Each is declared under `servers:` and selected per step with `server:`. Ocarina connects to each referenced server once and reuses the session.

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

### Assertions (`expect:`)

| Assertion | Source |
|---|---|
| `contains: "str"` | substring of the joined text content |
| `matches: "regex"` | regex over the text content |
| `equals: "str"` | exact match (whitespace-trimmed) |
| `is_error: bool` | `CallToolResult.isError` |
| `rule: "CEL"` | a CEL boolean over `output` and the current variables |
| `message: "str"` | the failure message printed when `rule` is false |

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

## CLI surface

```
ocarina docs   <server...>                       # markdown docs from a live server
ocarina record <out.yaml> <server...>            # proxy a session into a rondo
ocarina play   <file.yaml>                        # execute a rondo
ocarina play   <file.yaml> --output json          # machine-readable report for CI
ocarina play   <file.yaml> --trace                # log every JSON-RPC frame to stderr
ocarina play   <file.yaml> --tags smoke -e repo=acme
ocarina validate <file.yaml>                     # static + live-schema checks, no tool calls
ocarina diff   <file.yaml>                        # compare against current server schemas
ocarina lock   <file.yaml>                        # snapshot the schema; --check fails on drift
ocarina hum    <server...> -- <tool> [key=value] # ad-hoc single tool call
```

`ocarina hum` is the ad-hoc equivalent of `ansible -m <module>`: connect, call one tool, print the result, exit. No rondo file required.

---

## Internal components

### Parser (`internal/rondo/`)

Deserializes YAML into a `File`. Normalizes the input: a single `server:` block becomes a one-entry `Servers` map, `tasks:` is merged into the step list as an alias for `rondo:`, and `register:` is folded into `echo:`. Does not connect to any server.

```go
type File struct {
    Keys    map[string]string `yaml:"keys"`
    Servers map[string]Server `yaml:"servers"`
    Server  Server            `yaml:"server"`  // shorthand for a single server
    Steps   []Step            `yaml:"rondo"`
}

type Step struct {
    Name         string         `yaml:"name"`
    Server       string         `yaml:"server"`   // which entry in Servers
    Tool         string         `yaml:"tool"`
    Resource     string         `yaml:"resource"`
    Args         map[string]any `yaml:"args"`
    Grab         string         `yaml:"grab"`     // a gjson path
    Echo         string         `yaml:"echo"`     // register: is an alias
    Expect       *Expect        `yaml:"expect"`
    When         string         `yaml:"when"`     // CEL
    Loop         string         `yaml:"loop"`
    Timeout      string         `yaml:"timeout"`
    Retry        *RetryConfig   `yaml:"retry"`
    Tags         []string       `yaml:"tags"`
    IgnoreErrors bool           `yaml:"ignore_errors"`
}
```

### MCP client (`internal/mcpclient/`)

A thin wrapper over the MCP wire protocol. A server runs over stdio (a local subprocess) or the Streamable HTTP transport (a remote `url:`, with `headers:` such as a bearer token sent on every request). It runs the `initialize` handshake, lists tools (draining the paginated iterator), calls tools, reads resources, and lists resources and resource templates. Ocarina declares no `sampling`, `elicitation`, or `roots` client capabilities, so a server cannot issue requests Ocarina cannot service.

### Executor (`cmd/play.go`)

Walks the steps in order. For each step:

1. Match tags against `--tags` / `--skip-tags`.
2. Expand `loop:` into one iteration per array element, binding `{{item}}`.
3. Evaluate `when:`; skip the step if it is false.
4. Connect to the step's server (lazily, once) and check the tool exists with its required args present.
5. Interpolate `{{key}}` in args and URIs; an unresolved reference fails the step.
6. Dispatch `tools/call` or `resources/read`, honoring `timeout:` and `retry:`.
7. Treat a dispatch error or `isError: true` as a failure unless `ignore_errors:` is set.
8. Apply `grab:`, capture with `echo:`, then run `expect:` assertions.

A failure is recorded and the process exits non-zero at the end. `ignore_errors: true` keeps the run going past a failing step.

### Validator (`cmd/validate.go`)

Static analysis that connects to fetch schemas but calls no tools:

- every `tool:` exists on its server, with required `args:` present and types matching `inputSchema`
- every `{{key}}` resolves from `keys:`, `{{env.X}}`, or a prior `echo:`
- `when:`, `retry.until:`, and `expect.rule:` parse as CEL; `timeout:` parses as a duration
- every `server:` reference exists in the `servers:` map

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
