# Ocarina cassette vs MCP Python SDK: head-to-head comparison

Both files drive the same workflow: create a three-table e-commerce schema
(users, products, orders), seed 10 rows of orders, run join and aggregate
queries, and verify two data-integrity assertions plus an echo chain.

---

## 1. Line counts

| File | Total lines | Non-blank / non-comment |
|---|---|---|
| `migration-and-verify.yaml` | 150 | 126 |
| `test_with_mcp_client.py` | 201 | 168 |

The Python file is ~33% longer in raw lines and ~33% longer in substantive
lines. A large share of the gap is fixed boilerplate that appears once and
doesn't scale with the number of tool calls.

---

## 2. What the Python test requires that the cassette doesn't

**Imports and runtime wiring (lines 1–14 in the Python file)**

```python
import asyncio, sys
import mcp.types as types
from mcp import ClientSession
from mcp.client.stdio import StdioServerParameters, stdio_client
```

The cassette's `server:` block does the same job in three lines of YAML.

**Async machinery**

Every function is `async def`. The entry point calls `asyncio.run(main())`.
The cassette has no concept of concurrency; the runner handles it internally.

**Session lifecycle**

```python
async with stdio_client(server) as (read, write):
    async with ClientSession(read, write) as session:
        await session.initialize()
```

Three explicit nesting levels before the first tool call. The cassette's
`server:` stanza implies all of this.

**Output extraction**

```python
def extract_text(result: types.CallToolResult) -> str:
    parts = [item.text for item in result.content
             if isinstance(item, types.TextContent)]
    return "\n".join(parts)
```

Necessary because `CallToolResult.content` is a heterogeneous list
(`TextContent`, `ImageContent`, `EmbeddedResource`, etc.). The cassette
runner handles this internally; the YAML author never sees it.

**Error handling**

```python
if result.isError:
    print(f"  error: {text}", file=sys.stderr)
    sys.exit(1)
```

Added once per call in the helper. The cassette silently continues on
errors (prints to stderr) with no termination logic.

**Assertion helper**

```python
def assert_contains(label: str, text: str, needle: str) -> None:
    if needle not in text:
        ...
        sys.exit(1)
    print(f"  PASS: ...")
```

Fourteen lines of boilerplate that replaces:

```yaml
expect:
  contains: "some string"
```

---

## 3. Side-by-side: one representative track

**Cassette**
```yaml
- name: top customer by total spend
  tool: read_query
  args:
    query: |
      SELECT u.name,
             COUNT(o.id)        AS orders,
             SUM(o.total_price) AS total_spent
      FROM   users u
      JOIN   orders o ON u.id = o.user_id
      GROUP  BY u.id
      ORDER  BY total_spent DESC
      LIMIT  3
  expect:
    contains: "Alice"
```

**Python**
```python
top_customers = await tool(
    session, "read_query", "top customer by total spend",
    query=(
        "SELECT u.name,"
        "       COUNT(o.id)        AS orders,"
        "       SUM(o.total_price) AS total_spent"
        "  FROM users u"
        "  JOIN orders o ON u.id = o.user_id"
        " GROUP BY u.id"
        " ORDER BY total_spent DESC"
        " LIMIT 3"
    ),
)
assert_contains("top customers", top_customers, "Alice")
```

The Python version is longer because: (a) multiline strings require
concatenation or triple-quotes with careful indentation, (b) the assertion
is a separate call with three arguments instead of two YAML lines.

---

## 4. The echo chain: where Ocarina wins and where it hits a wall

**The win**: the cassette captures a snapshot in one line:

```yaml
echo: order_count_snapshot
```

And references it later:

```yaml
expect:
  contains: "{{order_count_snapshot}}"
```

The Python equivalent requires a variable assignment and a second assertion
call — not hard, but more explicit.

**The wall**: `grab:` — Ocarina's dot-path extractor for pulling a field out
of JSON — does not work with `mcp-server-sqlite`. That server returns
Python dict notation with single quotes (`[{'name': 'Alice'}]`) rather than
JSON double quotes (`[{"name": "Alice"}]`). Ocarina's `grab:` uses
`json.Unmarshal`, so it fails:

```
grab: output is not JSON: invalid character '\'' looking for beginning of value
```

In the Python test, extracting `"Alice"` from the output and using it in a
second query would take ~3 lines of string parsing. In Ocarina, once `grab:`
works (i.e., the server returns valid JSON), it takes one line. The cassette
would look like:

```yaml
- name: top customer
  tool: read_query
  args:
    query: "SELECT name FROM users ... LIMIT 1"
  echo: top_name
  grab: ".0.name"

- name: orders for top customer
  tool: read_query
  args:
    query: "SELECT * FROM orders WHERE user_name = '{{top_name}}'"
```

This is genuinely cleaner than the Python equivalent. The `grab:` limitation
here is a server-compatibility issue, not a design flaw in Ocarina.

---

## 5. The honest ceiling: what the Python test can do that the cassette cannot

**Conditional logic**

The Python test can branch:

```python
if "error" in result:
    # retry, skip, or escalate
```

The cassette has no `if:` or `when:` field. Every track always runs.

**Loops and fan-out**

The Python test can iterate over query results and run further tool calls
for each row. The cassette has a fixed list of tracks known at write time.

**Rich assertion logic**

The Python test can parse the output (once the server returns valid JSON or
any structured format), compute derived values, and assert on them:

```python
import json
rows = json.loads(output)  # only if the server returns real JSON
assert rows[0]["total_spent"] > 1000
assert sum(r["revenue"] for r in rows) == pytest.approx(4639.86)
```

The cassette's `expect:contains` is a substring check. There is no
`expect:equals`, `expect:greater_than`, or multi-value assertion.

**Error recovery**

The Python test can catch tool errors and attempt remediation — retry on
transient failure, fall back to a different query, or clean up state. The
cassette prints the error and moves to the next track.

**Parameterisation and fixtures**

The Python test can be called with arguments, environment variables, or
pytest fixtures. The cassette has `notes:` for static substitution but no
way to accept runtime parameters without editing the file.

**Summary table**

| Capability | Cassette | Python SDK |
|---|---|---|
| Define a fixed sequence of tool calls | Yes (trivial) | Yes (more boilerplate) |
| Session lifecycle management | Implicit | Explicit |
| Substring assertions | `expect: contains:` | `assert_contains(...)` |
| Numeric / relational assertions | No | Yes |
| Capture output for later use | `echo:` | variable assignment |
| Extract a field from JSON output | `grab:` (when server returns JSON) | manual parse |
| Conditional branching | No | Yes |
| Loops over result sets | No | Yes |
| Error recovery | No | Yes |
| Parameterised at runtime | Only via `notes:` | Full argument passing |

Ocarina wins on the routine case: a fixed, deterministic sequence of tool
calls with simple assertions reads shorter, has less ceremony, and requires
no understanding of async Python or MCP session semantics. The Python SDK
wins as soon as the workflow needs to react to what the server returns.
