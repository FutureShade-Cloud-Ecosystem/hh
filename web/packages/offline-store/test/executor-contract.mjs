// The contract every SqlExecutor must meet, run against each engine the
// package ships with (node:sqlite for the Tauri stand-in, wa-sqlite for the
// OPFS engine). Not a test file itself: the suites import and call it.
import { test } from "node:test";
import assert from "node:assert/strict";

export function executorContract(name, make) {
  test(`${name}: run then all returns rows keyed by column`, async () => {
    const sql = await make();
    await sql.run("CREATE TABLE t (id INTEGER PRIMARY KEY, label TEXT NOT NULL, n INTEGER)");
    await sql.run("INSERT INTO t (id, label, n) VALUES (?, ?, ?)", [1, "a", null]);
    await sql.run("INSERT INTO t (id, label, n) VALUES (?, ?, ?)", [2, "b", 7]);
    const rows = await sql.all("SELECT id, label, n FROM t ORDER BY id");
    assert.deepEqual(rows, [
      { id: 1, label: "a", n: null },
      { id: 2, label: "b", n: 7 },
    ]);
  });

  test(`${name}: parameters are bound, never spliced`, async () => {
    const sql = await make();
    await sql.run("CREATE TABLE t (label TEXT NOT NULL)");
    const hostile = "x'); DROP TABLE t; --";
    await sql.run("INSERT INTO t (label) VALUES (?)", [hostile]);
    const rows = await sql.all("SELECT label FROM t");
    assert.deepEqual(rows, [{ label: hostile }]);
  });

  test(`${name}: atomic applies every statement in order`, async () => {
    const sql = await make();
    await sql.run("CREATE TABLE t (id INTEGER PRIMARY KEY, label TEXT)");
    await sql.atomic([
      { sql: "INSERT INTO t (id, label) VALUES (?, ?)", params: [1, "first"] },
      { sql: "UPDATE t SET label = ? WHERE id = ?", params: ["second", 1] },
    ]);
    assert.deepEqual(await sql.all("SELECT id, label FROM t"), [{ id: 1, label: "second" }]);
  });

  test(`${name}: atomic is all or nothing when a later statement fails`, async () => {
    const sql = await make();
    await sql.run("CREATE TABLE t (id INTEGER PRIMARY KEY, label TEXT NOT NULL)");
    await sql.run("INSERT INTO t (id, label) VALUES (1, 'kept')");
    await assert.rejects(
      sql.atomic([
        { sql: "INSERT INTO t (id, label) VALUES (?, ?)", params: [2, "lost"] },
        { sql: "INSERT INTO t (id, label) VALUES (?, ?)", params: [1, "duplicate id"] },
      ]),
    );
    assert.deepEqual(await sql.all("SELECT id, label FROM t ORDER BY id"), [{ id: 1, label: "kept" }]);
  });

  test(`${name}: a failed atomic leaves the executor usable`, async () => {
    const sql = await make();
    await sql.run("CREATE TABLE t (id INTEGER PRIMARY KEY)");
    await assert.rejects(sql.atomic([{ sql: "INSERT INTO nope (id) VALUES (1)" }]));
    await sql.run("INSERT INTO t (id) VALUES (5)");
    assert.deepEqual(await sql.all("SELECT id FROM t"), [{ id: 5 }]);
  });

  test(`${name}: concurrent atomics do not interleave`, async () => {
    const sql = await make();
    await sql.run("CREATE TABLE t (id INTEGER PRIMARY KEY AUTOINCREMENT, tag TEXT)");
    const batch = (tag) => sql.atomic([1, 2, 3].map((i) => ({ sql: "INSERT INTO t (tag) VALUES (?)", params: [`${tag}${i}`] })));
    await Promise.all([batch("a"), batch("b"), batch("c")]);
    const tags = (await sql.all("SELECT tag FROM t ORDER BY id")).map((r) => r.tag);
    assert.deepEqual(tags, ["a1", "a2", "a3", "b1", "b2", "b3", "c1", "c2", "c3"]);
  });
}
