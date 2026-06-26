"""
Equivalent workflow to migration-and-verify.yaml using the MCP Python SDK directly.

Run with:
  uvx --from "mcp[cli]" python3 test_with_mcp_client.py
"""

import asyncio
import sys

import mcp.types as types
from mcp import ClientSession
from mcp.client.stdio import StdioServerParameters, stdio_client

DB_PATH = "/tmp/ocarina-test-complex-py.db"


def extract_text(result: types.CallToolResult) -> str:
    parts = [item.text for item in result.content if isinstance(item, types.TextContent)]
    return "\n".join(parts)


def assert_contains(label: str, text: str, needle: str) -> None:
    if needle not in text:
        print(f"  FAIL: {label!r} — expected output to contain {needle!r}", file=sys.stderr)
        print(f"        got: {text!r}", file=sys.stderr)
        sys.exit(1)
    print(f"  PASS: {label!r} contains {needle!r}")


async def main() -> None:
    server = StdioServerParameters(
        command="uvx",
        args=["mcp-server-sqlite", "--db-path", DB_PATH],
    )

    async with stdio_client(server) as (read, write):
        async with ClientSession(read, write) as session:
            await session.initialize()
            await run_workflow(session)


async def tool(session: ClientSession, name: str, label: str, **kwargs) -> str:
    print(f"==> {label} ({name})")
    result = await session.call_tool(name, arguments=kwargs if kwargs else None)
    text = extract_text(result)
    if result.isError:
        print(f"  error: {text}", file=sys.stderr)
        sys.exit(1)
    print(text)
    print()
    return text


async def run_workflow(session: ClientSession) -> None:
    await tool(
        session, "create_table", "create users table",
        query=(
            "CREATE TABLE IF NOT EXISTS users ("
            "  id    INTEGER PRIMARY KEY AUTOINCREMENT,"
            "  name  TEXT NOT NULL,"
            "  email TEXT NOT NULL UNIQUE,"
            "  city  TEXT NOT NULL"
            ")"
        ),
    )

    await tool(
        session, "create_table", "create products table",
        query=(
            "CREATE TABLE IF NOT EXISTS products ("
            "  id       INTEGER PRIMARY KEY AUTOINCREMENT,"
            "  name     TEXT NOT NULL,"
            "  category TEXT NOT NULL,"
            "  price    REAL NOT NULL CHECK(price > 0)"
            ")"
        ),
    )

    await tool(
        session, "create_table", "create orders table",
        query=(
            "CREATE TABLE IF NOT EXISTS orders ("
            "  id          INTEGER PRIMARY KEY AUTOINCREMENT,"
            "  user_id     INTEGER NOT NULL REFERENCES users(id),"
            "  product_id  INTEGER NOT NULL REFERENCES products(id),"
            "  quantity    INTEGER NOT NULL DEFAULT 1 CHECK(quantity > 0),"
            "  total_price REAL NOT NULL,"
            "  ordered_at  TEXT NOT NULL"
            ")"
        ),
    )

    await tool(
        session, "write_query", "seed users",
        query=(
            "INSERT INTO users (name, email, city) VALUES"
            "  ('Alice',  'alice@example.com',  'New York'),"
            "  ('Bob',    'bob@example.com',    'Chicago'),"
            "  ('Carol',  'carol@example.com',  'Austin'),"
            "  ('Dave',   'dave@example.com',   'Seattle'),"
            "  ('Eve',    'eve@example.com',    'Denver')"
        ),
    )

    await tool(
        session, "write_query", "seed products",
        query=(
            "INSERT INTO products (name, category, price) VALUES"
            "  ('Laptop',       'Electronics', 999.99),"
            "  ('Phone',        'Electronics', 599.99),"
            "  ('Headphones',   'Electronics', 149.99),"
            "  ('Python Book',  'Books',        39.99),"
            "  ('Standing Desk','Furniture',   299.99)"
        ),
    )

    await tool(
        session, "write_query", "seed orders",
        query=(
            "INSERT INTO orders (user_id, product_id, quantity, total_price, ordered_at) VALUES"
            "  (1, 1, 1,  999.99, '2026-06-01T09:00:00Z'),"
            "  (1, 2, 1,  599.99, '2026-06-02T10:00:00Z'),"
            "  (1, 4, 2,   79.98, '2026-06-03T11:00:00Z'),"
            "  (2, 3, 1,  149.99, '2026-06-04T09:30:00Z'),"
            "  (2, 4, 1,   39.99, '2026-06-05T14:00:00Z'),"
            "  (3, 5, 1,  299.99, '2026-06-06T08:00:00Z'),"
            "  (3, 1, 1,  999.99, '2026-06-07T12:00:00Z'),"
            "  (4, 2, 2, 1199.98, '2026-06-08T15:00:00Z'),"
            "  (5, 3, 1,  149.99, '2026-06-09T10:00:00Z'),"
            "  (5, 4, 3,  119.97, '2026-06-10T16:00:00Z')"
        ),
    )

    await tool(session, "list_tables", "list tables")

    # Capture order count snapshot for later integrity check (mirrors cassette echo:)
    order_count_snapshot = await tool(
        session, "read_query", "snapshot order count",
        query="SELECT COUNT(*) AS total_orders FROM orders",
    )

    category_revenue = await tool(
        session, "read_query", "revenue by product category",
        query=(
            "SELECT p.category,"
            "       COUNT(o.id)         AS order_lines,"
            "       SUM(o.total_price)  AS revenue"
            "  FROM orders o"
            "  JOIN products p ON p.id = o.product_id"
            " GROUP BY p.category"
            " ORDER BY revenue DESC"
        ),
    )
    assert_contains("revenue by category", category_revenue, "Electronics")

    await tool(
        session, "read_query", "full order detail — join across all three tables",
        query=(
            "SELECT u.name AS customer,"
            "       p.name AS product,"
            "       o.quantity,"
            "       o.total_price,"
            "       o.ordered_at"
            "  FROM orders o"
            "  JOIN users    u ON u.id = o.user_id"
            "  JOIN products p ON p.id = o.product_id"
            " ORDER BY o.ordered_at"
        ),
    )

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

    # Mirror cassette's expect: contains: "{{order_count_snapshot}}"
    final_count = await tool(
        session, "read_query", "verify order count unchanged after reads",
        query="SELECT COUNT(*) AS total_orders FROM orders",
    )
    assert_contains("order count unchanged", final_count, order_count_snapshot)

    print("All assertions passed.")


if __name__ == "__main__":
    import os
    if os.path.exists(DB_PATH):
        os.remove(DB_PATH)
    asyncio.run(main())
