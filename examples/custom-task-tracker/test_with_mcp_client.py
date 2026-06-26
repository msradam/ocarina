"""
Equivalent of tasks.yaml using the MCP Python SDK directly.
Run with: uvx --from "mcp[cli]" python3 test_with_mcp_client.py
"""

import asyncio
import json
import sys

from mcp import ClientSession
from mcp.client.stdio import StdioServerParameters, stdio_client


SERVER_CMD = "uvx"
SERVER_ARGS = [
    "--from", "fastmcp", "python3",
    "/Users/amsrahman/ocarina/examples/custom-task-tracker/server.py",
]


async def call(session: ClientSession, tool: str, **kwargs) -> str:
    result = await session.call_tool(tool, kwargs)
    return "".join(
        part.text for part in result.content if hasattr(part, "text")
    )


async def main() -> None:
    params = StdioServerParameters(command=SERVER_CMD, args=SERVER_ARGS)

    async with stdio_client(params) as (read, write):
        async with ClientSession(read, write) as session:
            await session.initialize()

            # Add three tasks
            raw = await call(session, "add_task",
                             title="Buy groceries",
                             description="Milk, eggs, bread")
            task1_id = json.loads(raw)["id"]

            raw = await call(session, "add_task",
                             title="Write tests",
                             description="Unit tests for the auth module")
            task2_id = json.loads(raw)["id"]

            await call(session, "add_task",
                       title="Deploy app",
                       description="Push v1.2.0 to production")

            # List all tasks and assert Buy groceries is present
            raw = await call(session, "list_tasks")
            tasks = json.loads(raw)
            titles = [t["title"] for t in tasks]
            assert "Buy groceries" in titles, f"Expected 'Buy groceries' in {titles}"
            print("PASS: list contains 'Buy groceries'")

            # Complete task 1
            await call(session, "complete_task", task_id=task1_id)

            # List completed tasks and assert status is completed
            raw = await call(session, "list_tasks", status="completed")
            assert "completed" in raw, f"Expected 'completed' in {raw!r}"
            print("PASS: completed task list contains 'completed'")

            # Delete task 2
            await call(session, "delete_task", task_id=task2_id)

            # Final list: task 1 (completed) and task 3 remain
            raw = await call(session, "list_tasks")
            assert "Deploy app" in raw, f"Expected 'Deploy app' in {raw!r}"
            print("PASS: final list contains 'Deploy app'")


if __name__ == "__main__":
    asyncio.run(main())
    sys.exit(0)
