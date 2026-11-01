// The drive-run sync fence leg compares the row's own stop reference, never the row's text.
import { test } from "node:test";
import assert from "node:assert/strict";
import { rowOnDriverStop } from "./acceptance/row-fence.mjs";

const MINE = "11111111-1111-4111-8111-111111111111";
const OTHER = "22222222-2222-4222-8222-222222222222";

test("a pod row is on the driver's stop only through its own stop_id", () => {
  assert.equal(rowOnDriverStop({ id: "p1", data: { stop_id: MINE, caption: "ok" } }, [MINE]), true);
  assert.equal(rowOnDriverStop({ id: "p2", data: { stop_id: OTHER, caption: `left at ${MINE}` } }, [MINE]), false);
  assert.equal(rowOnDriverStop({ id: "p3", data: { caption: "no stop" } }, [MINE]), false);
});

test("an attachment row is on the driver's stop only through a stop link", () => {
  const link = (kind, id) => ({ attachment_id: "a", owner_kind: kind, owner_id: id, role: "r" });
  assert.equal(rowOnDriverStop({ id: "a1", data: { links: [link("stop", MINE)] } }, [MINE]), true);
  assert.equal(rowOnDriverStop({ id: "a2", data: { links: [link("stop", OTHER)], checksum: MINE } }, [MINE]), false);
  assert.equal(rowOnDriverStop({ id: "a3", data: { links: [link("order", MINE)] } }, [MINE]), false);
  assert.equal(rowOnDriverStop({ id: "a4", data: { links: [] } }, [MINE]), false);
});
