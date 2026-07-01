# Testing MCP servers in CI

Ocarina replays a rondo against an MCP server with no model in the loop, so a
rondo is a deterministic test: it produces the same result today and next month,
and it spends no tokens. This guide covers the CI workflow, from a first
assertion to a published test report.

For the field and flag reference, see the [README](../README.md) and
[architecture](architecture.md). This page is the full workflow.

## Prerequisites

- An MCP server you can start from a command (stdio) or reach at a URL (HTTP).
- Ocarina installed. In CI: `go install github.com/msradam/ocarina@v0.5.0`.
  Pin the version so the toolchain is reproducible.

## 1. Get a rondo

Write one by hand, or record a real session. Recording captures what a model
did, including arguments you would not have written yourself:

```bash
# point your MCP host's config at this instead of the server directly
ocarina record session.yaml uvx mcp-server-fetch
```

`record` proxies stdio and writes every `tools/call` into `session.yaml`, args
exactly as sent, with each tool's output stored in a `result:` block.

Commit the rondo. It is now both a spec and a test.

## 2. Assert on replay with snapshots

`--snapshot` compares each step's live output against its recorded `result:`
block and fails on any drift. Capture a baseline once, then assert on every run:

```bash
ocarina play session.yaml --update      # capture or re-baseline the result: blocks
ocarina play session.yaml --snapshot    # assert; exits non-zero on drift
```

In CI, run `--snapshot` only, never `--update`. A drift then fails the build
instead of being silently rewritten. A step with no baseline fails rather than
passing green, so a forgotten `--update` cannot hide a gap.

Snapshots suit deterministic output. For a value that changes every call (a
timestamp, a fresh id), assert the stable part with `expect:` instead:

```yaml
- name: fetch the homepage
  tool: fetch
  args: { url: "https://example.com" }
  expect:
    contains: "Example Domain"
```

## 3. Gate latency

`expect.max_duration` fails a step whose tool call runs longer than the budget.
It times the successful attempt only, excluding retry backoff, so the budget
applies to a single call, not the retry loop's total:

```yaml
- name: search stays snappy
  tool: search
  args: { q: "widgets" }
  expect:
    contains: "results"
    max_duration: 500ms
```

One `ocarina play` now catches both "the tool broke" and "the tool got slow."
For throughput and percentiles under concurrency, use `ocarina load` with a
`--threshold` gate.

## 4. Run across many inputs

`--data` plays the whole rondo once per row of a CSV or JSON file, with each
column injected as a `{{key}}`:

```yaml
# zone-check.yaml
- name: time is reported for the zone
  tool: get_current_time
  args: { timezone: "{{tz}}" }
  expect:
    contains: "{{tz}}"
```

```bash
ocarina play zone-check.yaml --data timezones.csv
```

Each row is a separate case, and its values ride along in the report so a
failure maps back to the input that caused it.

## 5. Catch schema drift and bad arguments before running

`validate` checks a rondo against the live tool schemas without calling
anything. `--strict` turns an out-of-schema argument (the kind a real model
invents) into a build failure:

```bash
ocarina validate session.yaml --strict
```

For schema drift over time, `ocarina diff` compares a rondo against current
schemas, and `ocarina lock --check` fails when a locked schema changes.

## 6. Publish a test report

`--output junit` emits JUnit XML, the format CI test dashboards ingest (GitLab
reports, the Jenkins JUnit plugin, the common GitHub Actions reporters). One
rondo is a test suite; each step is a test case:

```bash
ocarina play session.yaml --snapshot --output junit > results.xml
```

The XML goes to stdout on its own; human progress and failures go to stderr, so
the redirect stays clean.

## Full GitHub Actions workflow

```yaml
name: MCP tests
on: [push, pull_request]

jobs:
  mcp:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - run: go install github.com/msradam/ocarina@v0.5.0

      # pre-flight: fail on out-of-schema args before any tool runs
      - run: ocarina validate tests/session.yaml --strict

      # replay with snapshot assertions, emit a JUnit report
      - run: ocarina play tests/session.yaml --snapshot --output junit > results.xml

      # publish the report even when the play step failed
      - if: always()
        uses: dorny/test-reporter@v1
        with:
          name: MCP replay
          path: results.xml
          reporter: java-junit
```

For a smoke test with no report, the composite action is enough:

```yaml
- uses: msradam/ocarina@v0.5.0
  with:
    rondo: tests/mcp-smoke.yaml
```

See [`action.yml`](../action.yml).

## How failures gate the build

`play` exits 0 when every assertion passes and non-zero otherwise, so it gates a
pipeline without extra wiring. A step fails on a failed `expect:`, a snapshot
drift, a missing baseline, an exceeded `max_duration`, a tool error
(`isError: true`) unless the step opts out, or an unresolved `{{key}}`. A rondo
that resolves to zero steps is an error too, so a mistyped top-level key fails
loudly instead of passing as a no-op.
