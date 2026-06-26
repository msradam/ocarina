from fastmcp import FastMCP
import json

mcp = FastMCP("Task Tracker")

_tasks: dict[int, dict] = {}
_next_id = 1


@mcp.tool()
def add_task(title: str, description: str = "") -> str:
    """Add a new task. Returns the created task as JSON."""
    global _next_id
    task = {
        "id": _next_id,
        "title": title,
        "description": description,
        "status": "pending",
    }
    _tasks[_next_id] = task
    _next_id += 1
    return json.dumps(task)


@mcp.tool()
def list_tasks(status: str = "") -> str:
    """List tasks. Pass status='pending' or status='completed' to filter."""
    result = list(_tasks.values())
    if status:
        result = [t for t in result if t["status"] == status]
    return json.dumps(result)


@mcp.tool()
def complete_task(task_id: int) -> str:
    """Mark a task as completed. Returns the updated task as JSON."""
    if task_id not in _tasks:
        return json.dumps({"error": f"task {task_id} not found"})
    _tasks[task_id]["status"] = "completed"
    return json.dumps(_tasks[task_id])


@mcp.tool()
def delete_task(task_id: int) -> str:
    """Delete a task by ID. Returns the deleted task as JSON."""
    if task_id not in _tasks:
        return json.dumps({"error": f"task {task_id} not found"})
    deleted = _tasks.pop(task_id)
    return json.dumps({"deleted": deleted})


if __name__ == "__main__":
    mcp.run()
