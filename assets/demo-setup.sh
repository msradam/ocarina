#!/usr/bin/env bash
set -e
DB=/tmp/ocarina-demo.db
rm -f "$DB"
sqlite3 "$DB" <<'SQL'
CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE orders (id INTEGER PRIMARY KEY, customer_id INTEGER, product TEXT, amount REAL);
INSERT INTO customers VALUES (1,'Alice'),(2,'Bob'),(3,'Carol');
INSERT INTO orders VALUES
  (1,1,'Widget Pro',49.99),
  (2,1,'Gadget Plus',129.00),
  (3,2,'Widget Pro',49.99),
  (4,3,'Deluxe Bundle',249.95),
  (5,1,'Deluxe Bundle',249.95);
SQL
