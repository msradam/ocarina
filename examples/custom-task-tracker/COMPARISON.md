# Ocarina Cassette vs Python MCP Client: Direct Comparison

This example tests the same task-tracker workflow two ways: an Ocarina cassette (`tasks.yaml`) and a raw Python MCP client script (`test_with_mcp_client.py`). Both were run against the same FastMCP server and all assertions pass.

## 1. Line Count

| File | Lines |
|------|-------|
| `tasks.yaml` (cassette) | 55 |
| `test_with_mcp_client.py` (Python) | 76 |

The cassette is 28% shorter. The gap would widen for longer workflows because each additional tool call adds ~5 YAML lines vs ~3-6 Python lines, but the Python file carries a fixed overhead of ~25 lines (imports, boilerplate, helper, async setup, `asyncio.run`).

## 2. What the Python Test Requires That the Cassette Does Not

| Requirement | Python test | Cassette |
|---|---|---|
| Know the MCP SDK import paths | Yes (`mcp.client.stdio`, `ClientSession`, `StdioServerParameters`) | No |
| Understand async/await | Yes (`asyncio.run`, `async with`, `await`) | No |
| Parse JSON manually | Yes (`json.loads(raw)["id"]`) | No (done by `grab: ".id"`) |
| Know how to iterate MCP result content | Yes (`part.text for part in result.content`) | No |
| Write assertion logic | Yes (`assert "x" in y, f"..."`) | No (`expect: contains:`) |

The Python test requires working knowledge of four distinct APIs: `asyncio`, the MCP client SDK, JSON parsing, and the Python assertion pattern. The cassette requires only knowing the tool names and their arguments, which you can get from `ocarina compose`.

## 3. Side-by-Side Code

**Cassette (`tasks.yaml`) — the "complete task 1" sequence:**

```yaml
  - name: add task 1 - Buy groceries
    tool: add_task
    args:
      title: Buy groceries
      description: Milk, eggs, bread
    echo: task1_id
    grab: ".id"

  - name: list all tasks
    tool: list_tasks
    expect:
      contains: "Buy groceries"

  - name: complete task 1
    tool: complete_task
    args:
      task_id: "{{task1_id}}"

  - name: list completed tasks - verify task 1 is done
    tool: list_tasks
    args:
      status: completed
    expect:
      contains: "completed"
```

**Python (`test_with_mcp_client.py`) — the same sequence:**

```python
async with stdio_client(params) as (read, write):
    async with ClientSession(read, write) as session:
        await session.initialize()

        raw = await call(session, "add_task",
                         title="Buy groceries",
                         description="Milk, eggs, bread")
        task1_id = json.loads(raw)["id"]

        raw = await call(session, "list_tasks")
        tasks = json.loads(raw)
        titles = [t["title"] for t in tasks]
        assert "Buy groceries" in titles, f"Expected 'Buy groceries' in {titles}"

        await call(session, "complete_task", task_id=task1_id)

        raw = await call(session, "list_tasks", status="completed")
        assert "completed" in raw, f"Expected 'completed' in {raw!r}"
```

The cassette's data-chaining story (`echo:` + `grab:`) is the clearest win: capturing a field from one call and threading it into the next is two YAML keys. In Python you do it manually with `json.loads(raw)["id"]`, which is fine but requires you to know the response schema ahead of time in code form.

## 4. What Each Approach Gives You That the Other Does Not

**Cassette advantages:**
- Runs from any shell with no Python environment: `ocarina play tasks.yaml`.
- `grab:` dot-path extraction eliminates explicit JSON parsing.
- `notes:` top-level variables make the server command and paths easily swappable without touching the test logic.
- Readable as a specification: a non-engineer can follow the workflow step by step.
- `--dry-run` flag prints every call without executing it.

**Python test advantages:**
- Full language expressiveness: you can assert on structure (e.g., check `len(tasks) == 3`), not just substring presence.
- Can be run under `pytest` and integrated into CI with coverage, parametrize, and fixtures.
- Error messages are under your control and can be as specific as you want.
- Can import shared fixtures or test utilities from the rest of your project.
- No dependency on the `ocarina` binary.

## 5. Does Ocarina's "Live Server" Limitation Matter Here?

Ocarina always calls the real server during `play`; it is not hermetic. There is no cassette-level mock or snapshot of responses. This matters in two scenarios:

**Where it matters:** If the server has external side effects (writes to a database, sends an email, calls a third-party API), every `ocarina play` run triggers those effects. You cannot use Ocarina as a pure regression test that validates against a previously recorded response without touching production.

**Where it does not matter:** For a server like this one, in-memory state resets on each run because the server process starts fresh. Ocarina is well-suited to development-time smoke testing: "does this tool sequence work against my current server implementation?" The test is fast, deterministic given the server, and produces clear output.

The Python MCP client test has the same property: it also calls the live server. Neither approach is hermetic by default. If you want hermetic tests, you need a separate fixture layer in Python (mocking `stdio_client`) or a separate Ocarina feature that does not yet exist (replaying recorded responses instead of replaying calls).

**Bottom line:** For developer-facing workflow tests against local or staging servers, the cassette is the faster path. For CI assertions requiring structural checks, integration with `pytest`, or hermetic behavior, the Python test is the right tool.
