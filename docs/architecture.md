# Ocarina architecture

Ocarina is a YAML-driven automation framework for MCP servers. It occupies the same layer relative to MCP that Ansible occupies relative to SSH: a declarative, LLM-free execution engine that composes protocol primitives into repeatable workflows.

```
┌─────────────────────────────────────────────┐
│                  rondo.yaml                 │  authored by humans
│  keys / server / rondo: [tracks...]         │
└────────────────┬────────────────────────────┘
                 │
┌────────────────▼────────────────────────────┐
│              Ocarina (single binary)         │
│                                             │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  │
│  │  Parser  │  │ Executor │  │  Assert  │  │
│  │ (YAML→   │  │ (track   │  │ (expect/ │  │
│  │  Rondo)  │  │  runner) │  │  grab)   │  │
│  └──────────┘  └────┬─────┘  └──────────┘  │
│                     │                       │
│  ┌──────────────────▼──────────────────┐    │
│  │           MCP Client                │    │
│  │  initialize → tools/list →          │    │
│  │  tools/call / resources/read        │    │
│  └──────────────────────────────────────┘    │
└─────────────────────────┬───────────────────┘
                          │  stdio / SSE / Streamable HTTP
┌─────────────────────────▼───────────────────┐
│              MCP Server                      │
│  (sqlite, postgres, kubernetes, ...)         │
└─────────────────────────────────────────────┘
```

---

## Protocol mapping

Every Ocarina feature maps to a specific MCP protocol primitive. Nothing is invented outside the wire protocol.

### Track types

| Rondo field | MCP method | Notes |
|---|---|---|
| `tool:` | `tools/call` | Primary track type. Args validated against `inputSchema`. |
| `resource:` | `resources/read` | URI is the only argument. MIME type from response informs `grab:` handling. |

### Assertions (`expect:`)

| Assertion | Source | Spec version |
|---|---|---|
| `contains: "str"` | `content[].text` substring | 2024-11-05 |
| `matches: "regex"` | `content[].text` regex | 2024-11-05 |
| `equals: "str"` | `content[].text` exact | 2024-11-05 |
| `is_error: bool` | `CallToolResult.isError` | 2024-11-05 |
| `structured: {k: v}` | `CallToolResult.structuredContent` | **2025-06-18 only** |

`isError` is a protocol-level field on every `tools/call` response. A result with `isError: true` is a valid, successful JSON-RPC response — the tool ran but reported failure. Ocarina checks this field explicitly; it is not an exception.

`structuredContent` carries typed JSON alongside the human-readable `content` array. When `Tool.outputSchema` is present, `structuredContent` conforms to it. Ocarina prefers `structuredContent` for `expect: structured:` when available, falls back to text parsing otherwise.

### Variable capture (`grab:`)

`grab:` extracts values from tool or resource output and stores them in the rondo's key map for use in subsequent tracks via `{{key}}` interpolation. JSONPath expressions select into `content[].text` (parsed as JSON) or `structuredContent`.

### Safety gates (tool annotations)

MCP 2025-03-26 introduced `ToolAnnotations` on each tool definition. Defaults matter:

| Annotation | Default | Meaning |
|---|---|---|
| `readOnlyHint` | `false` | Tool may modify state |
| `destructiveHint` | `true` | Tool may destroy data |
| `idempotentHint` | `false` | Repeated calls have additional effect |
| `openWorldHint` | `true` | Tool reaches external systems |

Because `destructiveHint` defaults to `true`, most tools in the ecosystem are assumed destructive unless the server explicitly declares otherwise. Ocarina surfaces this:

- `ocarina validate` reports which tracks call destructive tools
- `ocarina play rondo --safe` refuses to execute any track whose tool has `destructiveHint: true` and `readOnlyHint: false`
- `ocarina docs` renders warning badges from annotations automatically, pulled from the protocol, not from prose

These are advisory hints only per the spec. Ocarina uses them for confirmation gates, not security enforcement.

### Resource URI templates

`resources/templates/list` returns RFC 6570 URI templates (e.g. `"postgres:///{database}/schema/{table}"`). The `resource:` track type supports template interpolation from `keys:`:

```yaml
keys:
  database: analytics
  table: events

rondo:
  - name: read table schema
    resource: "postgres:///{{database}}/schema/{{table}}"
    grab:
      schema: "."
```

The server's own template list informs `ocarina compose` when scaffolding resource tracks.

### Dynamic loops via resources

The central novel capability: `resources/list` returns the server's own inventory. Feed it into `loop:` and the server defines its own iteration space.

```yaml
rondo:
  - name: list namespaces
    resource: "k8s://namespaces"
    grab:
      namespaces: "."

  - name: pod health per namespace
    tool: kubectl_get_by_kind_in_namespace
    loop: "{{namespaces}}"
    args:
      kind: pod
      namespace: "{{item}}"
    expect:
      contains: Running
```

Ansible has no equivalent. Its inventory is always external and static. Ocarina's iteration space is authoritative from the server.

---

## CLI surface

```
ocarina play rondo <file.yaml>          # execute a rondo
ocarina play rondo <file.yaml> --tags lint,fast
ocarina play rondo <file.yaml> --safe   # block destructive tools
ocarina play rondo <file.yaml> --dry-run

ocarina play note <server...> -- <tool> [key=value ...]   # ad-hoc single tool call

ocarina docs <server...>                # markdown docs from live server
ocarina compose <server...>            # scaffold a rondo from server introspection
ocarina validate <file.yaml>           # static lint + data-flow analysis
```

`ocarina play note` is the ad-hoc equivalent of `ansible -m <module>`. It connects to a server, calls one tool, prints the result, and exits. No rondo file required.

---

## Internal components

### Parser (`internal/playbook/`)

Deserializes YAML into `Rondo` structs. Validates required fields. Resolves `keys:` defaults. Does not connect to any server.

```go
type Rondo struct {
    Keys   map[string]string `yaml:"keys"`
    Server ServerConfig      `yaml:"server"`
    Tracks []Track           `yaml:"rondo"`
}

type Track struct {
    Name         string            `yaml:"name"`
    Tool         string            `yaml:"tool"`
    Resource     string            `yaml:"resource"`   // mutually exclusive with Tool
    Args         map[string]any    `yaml:"args"`
    Grab         map[string]string `yaml:"grab"`
    Expect       Expect            `yaml:"expect"`
    Loop         string            `yaml:"loop"`       // {{key}} or static list
    When         string            `yaml:"when"`       // {{key}} > 0, etc.
    Tags         []string          `yaml:"tags"`
    IgnoreErrors bool              `yaml:"ignore_errors"`
}
```

### MCP client (`internal/mcpclient/`)

Thin wrapper over the MCP wire protocol. Handles:
- `initialize` / `initialized` handshake
- `ClientCapabilities` declaration — Ocarina declares **no** `sampling`, `elicitation`, or `roots` capabilities. This prevents servers from issuing requests Ocarina cannot service.
- `tools/list` with cursor pagination — follows `nextCursor` until exhausted
- `tools/call` — returns `CallToolResult`; caller checks `isError`
- `resources/list` with cursor pagination
- `resources/templates/list`
- `resources/read`
- Logging notifications (`notifications/message`) — surfaced as debug output during play

Transports supported: stdio (primary), SSE (2024 legacy), Streamable HTTP (2025-03-26).

### Executor (`cmd/play.go`)

Walks `Rondo.Tracks` in order. For each track:

1. Evaluate `when:` — skip track if false
2. Check tags against `--tags` / `--skip-tags` flags
3. Expand `loop:` — if set, run steps 4–7 for each item
4. Interpolate `{{key}}` in args and resource URI from current key map
5. Dispatch: `tools/call` or `resources/read`
6. Check `isError` — halt (or continue if `ignore_errors: true`)
7. Run `expect:` assertions — halt on first failure
8. Apply `grab:` — merge captured values into key map

### Validator (`cmd/validate.go`)

Static analysis without connecting to a server:
- Schema validation of the rondo YAML
- Data-flow analysis: every `{{key}}` referenced in args or `when:` must be defined in `keys:` or reachable via a prior `grab:`
- Reports undefined references by track name and field
- Reports tracks that can never be reached (e.g. `when:` over a key that is never grabbed)

When a server is available, `validate` also connects to check:
- Every `tool:` name exists on the server
- Every `args:` key is present in the tool's `inputSchema`
- Every `resource:` URI is reachable (or matches a URI template)
- Annotation warnings for destructive tools

---

## What Ocarina does not implement

| MCP feature | Reason omitted |
|---|---|
| `sampling/createMessage` | Requires an LLM. Ocarina is LLM-free by design. |
| `elicitation/create` | Requires interactive user input mid-execution. Incompatible with deterministic replay. |
| `roots/list` | Server-to-client capability. Ocarina does not expose a filesystem to servers. |
| `prompts/list`, `prompts/get` | Prompt templates are LLM-facing. No use in deterministic tool execution. |
| `completion/complete` | Argument autocomplete for LLM UIs. Not relevant to YAML-authored rondos. |

---

## Key invariants

1. **A rondo is deterministic.** Given the same `keys:` and the same server state, `ocarina play` produces the same result every time. No LLM, no randomness.
2. **The rondo file does not contain output.** It is a spec, not a recording. This distinguishes Ocarina from VCR-style replay tools.
3. **The server is the source of truth for what exists.** Tool schemas, resource URIs, and annotations come from the live server at runtime via the MCP protocol. The rondo does not duplicate them.
4. **Failures are explicit.** `isError: true` from the protocol, assertion failures from `expect:`, and missing `grab:` keys all halt execution immediately unless `ignore_errors: true` is set.
