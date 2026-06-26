<div align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/whistle-dark.svg">
    <img src="assets/whistle-light.svg" alt="Ocarina" width="80">
  </picture>
  <h1>Ocarina</h1>
</div>

When an AI agent uses tools (querying a database, reading files, calling an API), those tool calls are deterministic. The LLM deciding which ones to make is not.

Ocarina sits between your LLM client and your MCP servers, records every tool call to a YAML cassette, and replays them exactly. No LLM needed. No API keys in CI.

```
ocarina record session.yaml uvx mcp-server-fetch
ocarina play   session.yaml
ocarina compose uvx mcp-server-fetch
```

![Ocarina demo](assets/demo.gif)

## Why

MCP (Model Context Protocol) is how LLMs connect to external tools: filesystems, databases, APIs, browsers. A session produces a sequence of tool calls. That sequence is reproducible. The LLM's reasoning about it is not.

Ocarina captures the tool-call layer. A recorded cassette is a YAML file you can commit to git, run in CI without an LLM, parameterize with variables, and use as a regression test. Same file, all of that.

## Install

```bash
go install github.com/msradam/ocarina@latest
```

Or download a binary from [releases](https://github.com/msradam/ocarina/releases).

Requires Go 1.26.4+.

## Quick start

Discover what tools a server exposes:

```bash
ocarina compose uvx mcp-server-fetch
# Server: uvx [mcp-server-fetch]
# 1 tool(s) available:
#
#   tool: fetch
#     description: Fetches a URL from the internet...
```

Configure your MCP host to run Ocarina instead of the server directly, then use it normally:

```bash
ocarina record session.yaml uvx mcp-server-fetch
# ... use your MCP host normally ...
# ocarina: recorded 3 track(s) to session.yaml
```

Play it back without an LLM:

```bash
ocarina play session.yaml
# ==> fetch (fetch)
# <html>...
```

## Cassette format

A cassette is a YAML file. Write one by hand or record it from a live session.

```yaml
# Investigate any GitHub repo. Swap notes.owner and notes.repo to change repos.
notes:
  owner: modelcontextprotocol
  repo: go-sdk

server:
  command: npx
  args: [-y, "@modelcontextprotocol/server-github"]

tracks:
  - name: list recent commits
    tool: list_commits
    args:
      owner: "{{owner}}"
      repo: "{{repo}}"
      per_page: 5
    echo: commits_json      # capture output into notes
    grab: ".0.sha"          # extract first SHA from JSON array

  - name: show latest commit
    tool: get_commit
    args:
      owner: "{{owner}}"
      repo: "{{repo}}"
      sha: "{{commits_json}}"  # value from previous track

  - name: list open issues
    tool: list_issues
    args:
      owner: "{{owner}}"
      repo: "{{repo}}"
      state: open
```

### Track fields

| Field | Description |
|---|---|
| `tool` | Tool name to call |
| `args` | Arguments. `{{key}}` interpolates from `notes`. |
| `echo` | Capture text output into `notes` under this key |
| `grab` | Dot-path into JSON output (`.0.sha`, `.name`), applied before `echo` captures |
| `expect.contains` | Assert output contains this string. `play` exits non-zero if not. |
| `result` | Recorded output, optional, kept for reference |

### Cassette-level fields

| Field | Description |
|---|---|
| `notes` | Static variables, interpolated as `{{key}}` throughout |
| `server` | Command and args to launch the MCP server |
| `llm` | Captured `sampling/createMessage` exchanges from agentic servers |

## Commands

### `ocarina record <output.yaml> <command> [args...]`

Sits as a transparent stdio proxy between your MCP host and server. Records every `tools/call` request and response. Also captures `sampling/createMessage` exchanges when an agentic server calls back to the LLM.

```bash
ocarina record out.yaml uvx mcp-server-sqlite --db-path /tmp/db.sqlite
ocarina record out.yaml npx -y @modelcontextprotocol/server-github
```

Flags:
- `--no-result`: omit result blocks from the cassette (smaller files, cleaner diffs)

### `ocarina play <cassette.yaml>`

Executes each track in order against the live server. No LLM involved. Notes from `echo:` feed into subsequent tracks. Exits non-zero if any `expect:` assertion fails.

```bash
ocarina play examples/github-investigation.yaml
ocarina play examples/mcp-smoke-test.yaml   # has assertions, works as a CI test
ocarina play examples/github-investigation.yaml --dry-run
```

Flags:
- `--dry-run`: print tracks without executing them

### `ocarina compose <command> [args...]`

Connects to a server and lists tools with their schemas. Servers that declare side-effect hints show badges: `[readonly]`, `[destructive]`, `[idempotent]`.

```bash
ocarina compose uvx mcp-server-fetch
ocarina compose uvx mcp-server-sqlite --db-path /tmp/db.sqlite
ocarina compose npx -y @modelcontextprotocol/server-filesystem /tmp
ocarina compose --yaml uvx mcp-server-fetch   # emit a skeleton cassette
```

## Examples

The `examples/` directory has working cassettes for:

| File | Server | What it shows |
|---|---|---|
| `fetch-demo.yaml` | `uvx mcp-server-fetch` | Basic fetch and content capture |
| `github-investigation.yaml` | `@modelcontextprotocol/server-github` | `echo:` + `grab:` to chain API calls |
| `sqlite-migration.yaml` | `uvx mcp-server-sqlite` | Stateful migration, seed, and query workflow |
| `git-repo-audit.yaml` | `uvx mcp-server-git` | Local repo audit: log, status, diff, branches |
| `mcp-smoke-test.yaml` | `@modelcontextprotocol/server-everything` | `expect:` assertions, usable as a CI smoke test |
| `sequential-thinking-demo.yaml` | `@modelcontextprotocol/server-sequential-thinking` | Multi-step reasoning chain as a recorded cassette |
| `knowledge-graph-demo.yaml` | `@modelcontextprotocol/server-memory` | Create, relate, search, and delete lifecycle |
| `puppeteer-scrape.yaml` | `@modelcontextprotocol/server-puppeteer` | Headless browser: navigate, evaluate JS, screenshot |
| `time-zones.yaml` | `uvx mcp-server-time` | Parameterized timezone conversions |

## Use in CI

```yaml
# .github/workflows/mcp-test.yml
- name: Run MCP smoke tests
  run: ocarina play examples/mcp-smoke-test.yaml
```

`play` exits 0 if all expectations pass, non-zero otherwise. No API keys needed.

## What ocarina is not

Ocarina does not score LLM outputs, compare models, or track token costs. It connects to the real MCP server on every `play` run, so a broken server breaks the replay. It has no test runner, no discovery, and no report format beyond stdout and exit codes.

## Development

```bash
git clone https://github.com/msradam/ocarina
cd ocarina
go build -o ~/bin/ocarina .
go test ./...
```

Validation: `gofmt`, `go vet`, `staticcheck`, `golangci-lint`, `gosec`, `govulncheck`.

## License

MIT

---

[Whistle](https://thenounproject.com/browse/icons/term/whistle/) icon by Alessio Capponi from [Noun Project](https://thenounproject.com) (CC BY 3.0)
