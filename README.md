<div align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/whistle-dark.svg">
    <img src="assets/whistle-light.svg" alt="Ocarina" width="80">
  </picture>
  <h1>Ocarina</h1>
  <p><strong>An automation framework for MCP servers.</strong> Write a YAML script, replay it deterministically, no LLM in the loop.</p>

  <a href="https://github.com/msradam/blender-mcp-ocarina">
    <img src="assets/blender-demo.gif" alt="An Ocarina rondo building a 3D scene in Blender, one step at a time" width="100%">
  </a>
  <p><sub>A rondo driving <a href="https://github.com/ahujasid/blender-mcp">blender-mcp</a>: lay down a plane, drop a cube, stack a sphere, add a cone, then verify the scene. Same YAML, same result, every run. No model involved. Clone <a href="https://github.com/msradam/blender-mcp-ocarina">blender-mcp-ocarina</a> to run it yourself.</sub></p>
</div>

The MCP ecosystem is already enormous: thousands of servers exposing real services through typed tools, readable resources, and schema-checked contracts, deployed and ready to call. Ocarina is an automation framework for all of it. Write a YAML script that drives tools across one or more servers, pipes values between steps, branches, loops, and retries, and runs the same way every time. No LLM in the loop, so every run is reproducible and costs nothing.

These tools were built to be read by language models, and language models are trained on human language, so the tools read cleanly to people too. A server exposes named contracts like `get_issues` or `query_database`, not endpoints you wire up in code. Every server someone built for an AI assistant is one you can drive.

What you write is a playbook: a portable artifact that captures an automation workflow over those servers, with MCP as the wire protocol. You can read it, review it in a pull request, version it, and run it anywhere the servers are reachable. Write it by hand or have an agent generate it. Either way it runs the same on every execution, with no sampling, no tokens, and nothing inferring between the file and the result.

## Install

```bash
go install github.com/msradam/ocarina@latest
```

Binaries are available on the [releases page](https://github.com/msradam/ocarina/releases). Building from source requires Go 1.26+.

## Use

Generate markdown docs for a server:

```bash
ocarina docs uvx mcp-server-sqlite --db-path mydb.sqlite
ocarina docs npx -y @modelcontextprotocol/server-github > docs/github.md
```

Run a rondo:

```bash
ocarina play db-audit.yaml
ocarina play db-audit.yaml --dry-run
ocarina play db-audit.yaml -e db=/tmp/other.sqlite  # override a key at runtime
```

Validate a rondo against the live server without running any tools:

```bash
ocarina validate db-audit.yaml
```

## Design principles

- **Deterministic.** The same rondo produces the same result on every run. No sampling, no randomness.
- **Protocol-native.** Talks MCP directly via `tools/call`, `resources/read`, and `resources/list`. Works with any compliant server.
- **Assertions are first-class.** `play` exits non-zero if any `expect:` check fails. Rondos work as CI health checks out of the box.
- **No credentials in scripts.** Server connection and environment variables stay outside the rondo file.
- **One rondo, any machine.** If the MCP server is available, the rondo runs.

## Rondo format

A rondo is a YAML file with three sections.

```yaml
keys:
  owner: acme
  repo: api

server:
  command: npx
  args: [-y, "@modelcontextprotocol/server-github"]

rondo:
  - name: recent commits
    tool: list_commits
    args:
      owner: "{{owner}}"
      repo: "{{repo}}"
    grab: ".0.sha"
    echo: latest_sha

  - name: commit detail
    tool: get_commit
    args:
      owner: "{{owner}}"
      repo: "{{repo}}"
      sha: "{{latest_sha}}"
    expect:
      contains: "feat"
```

### Step fields

| Field | Description |
|---|---|
| `server` | Which server to run this step against (a key in the `servers:` map); defaults to the only/first server |
| `tool` | Tool name to call |
| `resource` | Resource URI to read (`resources/read`) |
| `list_resources` | Server prefix to list resources from; output is a JSON URI array |
| `args` | Tool arguments. `{{key}}` interpolates from `keys` or prior `echo` captures |
| `echo` | Store this step's output under a key for later steps |
| `grab` | Dot-path into JSON output before storing: `.0.sha`, `.name`, `.items.0.id` |
| `loop` | Expand a JSON array key into repeated iterations; sets `{{item}}` each time |
| `expect.contains` | Assert output contains this string |
| `expect.matches` | Assert output matches this regex |
| `expect.equals` | Assert output equals this string (whitespace-trimmed) |
| `expect.is_error` | Assert whether the tool returned `isError: true` |
| `ignore_errors` | Continue past failures instead of halting |
| `tags` | Tag this step for `--tags` / `--skip-tags` filtering |

`{{env.NAME}}` resolves from the process environment and works anywhere `{{key}}` does.

Coming from Ansible? `tasks:` is accepted as an alias for `rondo:`, and `register:` as an alias for `echo:`.

## Multiple servers

A single rondo can talk to more than one server. Declare them under `servers:` and set `server:` on each step. Steps that omit `server:` use the first entry.

```yaml
servers:
  time: {command: uvx, args: [mcp-server-time]}
  fetch: {command: uvx, args: [-y, "@modelcontextprotocol/server-fetch"]}

rondo:
  - name: get time
    server: time
    tool: get_current_time
    args: {timezone: UTC}

  - name: fetch page
    server: fetch
    tool: fetch
    args: {url: "https://example.com"}
```

Output and `diff` namespace tool names by server (`time.get_current_time`). The single `server:` block still works for one-server rondos.

## Commands

**`ocarina docs <command> [args...]`**: generate markdown documentation for every tool, resource, and resource template a server exposes.

**`ocarina play <rondo.yaml>`**: execute each step against the live server.

**`ocarina validate <rondo.yaml>`**: check tool names, required args, schema types, and `{{key}}` data flow without making any calls.

**`ocarina hum <command> [args...] -- <tool> [key=value ...]`**: call a single tool and print the result.

**`ocarina record <output.yaml> <command> [args...]`**: proxy mode; records every tool call from a live MCP client session into a rondo file.

## Server names

Create a `.mcp.json` (or `~/.mcp.json` for credentials) and reference servers by name in rondos and on the command line:

```json
{
  "mcpServers": {
    "github": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": { "GITHUB_PERSONAL_ACCESS_TOKEN": "ghp_..." }
    }
  }
}
```

```yaml
server: github
```

```bash
ocarina hum github -- list_commits owner=pytorch repo=pytorch per_page=1
```

See `mcp.json.example` for a starter template. Ocarina also discovers servers from the Claude Desktop config (`~/Library/Application Support/Claude/claude_desktop_config.json`).

## Examples

Working rondos for 50+ MCP servers are in [`examples/`](examples/). A selection:

| Rondo | Server | What it does |
|---|---|---|
| `sqlite/data-quality-audit.yaml` | `mcp-server-sqlite` | Schema check, row counts, referential integrity assertions |
| `github-investigation/repo-health.yaml` | `github-mcp-server` | Commit history, open issues, contributor activity |
| `github-investigation/resource-audit.yaml` | `github-mcp-server` | Read repo files directly via `resource:` steps |
| `postgres/query-workflow.yaml` | `mcp-server-postgres` | Multi-step query and result validation |
| `docker/docker.yaml` | `mcp-server-docker` | Container list, image audit, resource usage check |
| `elasticsearch/cluster-search.yaml` | `mcp-server-elasticsearch` | Index health, search, document count assertions |
| `playwright-browser/page-audit.yaml` | `mcp-server-playwright` | Navigate, extract content, assert on page state |
| `yahoo-finance/portfolio-health.yaml` | `mcp-yahoo-finance` | Price fetch, income statements, parameterized by ticker |

See [docs/tested-servers.md](docs/tested-servers.md) for the full list.

## Showcases

Standalone repositories you can clone and run, each a real working environment for a different MCP server:

- [duckdb-mcp-ocarina](https://github.com/msradam/duckdb-mcp-ocarina): data integrity, migration, and regression tests against a DuckDB database. Clone and run, no credentials.
- [chrome-devtools-mcp-ocarina](https://github.com/msradam/chrome-devtools-mcp-ocarina): synthetic web health checks through Google's Chrome DevTools MCP. Fail on a console error or a failed request.
- [github-mcp-ocarina](https://github.com/msradam/github-mcp-ocarina): repo governance as tests through the GitHub MCP server. Assert a repo ships a license, is documented, and has history.
- [blender-mcp-ocarina](https://github.com/msradam/blender-mcp-ocarina): automate and snapshot-test a 3D scene in Blender, an app with no external API at all.

## Use in CI

`play` exits 0 if all `expect:` assertions pass, non-zero otherwise. Drop a rondo into any CI pipeline:

```yaml
- name: Database health check
  run: ocarina play rondos/db-audit.yaml
```

A composite GitHub Action installs Ocarina and replays a rondo:

```yaml
- uses: msradam/ocarina@v1
  with:
    rondo: tests/mcp-smoke.yaml
```

See [`action.yml`](action.yml) and [`.github/workflows/example.yml`](.github/workflows/example.yml).

## License

MIT. [Whistle](https://thenounproject.com/browse/icons/term/whistle/) icon by Alessio Capponi from [Noun Project](https://thenounproject.com) (CC BY 3.0).
