// The oneOf-of-references gate. The review's point 5: a oneOf of two
// references must end up typed and validated, never unknown. The three named
// sites in the document are MessagingMessage.ref (MessagingEventRef |
// MessagingApprovalRef) and the two finstmt report responses (the report or
// the comparative rows). This proves, against the generated descriptors, that
// the union validator holds each named leg and refuses a value that matches
// neither, and that the two finstmt operations' emitted signatures carry the
// union.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { SCHEMA_DESCRIPTORS } from "../src/testing.ts";
import { validateValue } from "./_validate.mjs";

const packageDir = dirname(dirname(fileURLToPath(import.meta.url)));
const genDir = join(packageDir, "src", "gen");

// Each leg exactly as the document requires it: MessagingEventRef requires
// topic; MessagingApprovalRef requires approval_id, policy_type, and status
// (PENDING, APPROVED, or REJECTED).
const EVENT_REF = {
  topic: "order.edited",
  entity: "#SO-TRT-43",
  summary: "line 2 quantity changed",
  amount_cents: 101700,
};
const APPROVAL_REF = {
  approval_id: "0dd4ec29-9616-4754-9f5f-667da1722018",
  policy_type: "hold_release",
  status: "APPROVED",
};

test("MessagingMessage.ref is the union of the two ref schemas, descriptor included", (t) => {
  const ref = SCHEMA_DESCRIPTORS.MessagingMessage.props.ref;
  assert.deepEqual(
    ref,
    { k: "union", of: [{ k: "ref", name: "MessagingEventRef" }, { k: "ref", name: "MessagingApprovalRef" }] },
    "the generated descriptor is the real union",
  );
});

test("the union validator holds the event leg and the approval leg, and refuses neither-or-garbage", (t) => {
  for (const [name, leg] of [["MessagingEventRef", EVENT_REF], ["MessagingApprovalRef", APPROVAL_REF]]) {
    const message = { ...minimalMessage(), ref: leg };
    assert.deepEqual(
      validateValue(message, SCHEMA_DESCRIPTORS.MessagingMessage, name),
      [],
      `a ${name} ref validates`,
    );
  }
  const garbage = { ...minimalMessage(), ref: { unexpected: true } };
  const problems = validateValue(garbage, SCHEMA_DESCRIPTORS.MessagingMessage, "garbage");
  assert.ok(problems.length > 0, "a ref matching no union leg is refused");
});

test("the two finstmt operations resolve the report-or-comparative-rows union", (t) => {
  const finstmt = readFileSync(join(genDir, "staff-finstmt.ts"), "utf8");
  assert.match(
    finstmt,
    /export async function finstmtIncomeStatement\(\n[\s\S]*?\): Promise<Result<FinStmtIncomeStatementReport \| FinStmtComparativeRows>> \{/,
  );
  assert.match(
    finstmt,
    /export async function finstmtBalanceSheet\(\n[\s\S]*?\): Promise<Result<FinStmtBalanceSheetReport \| FinStmtComparativeRows>> \{/,
  );
});

// The required frame of a MessagingMessage, per the document: id, channel_id,
// kind, body, reply_count, created_at. The created_at value is the epoch,
// computed rather than written as a literal: plainly a fixture, and no
// calendar date in the source.
function minimalMessage() {
  return {
    id: "0dd4ec29-9616-4754-9f5f-667da1722018",
    channel_id: "86b744fc-c0bf-4b07-a3c7-91b860d428f4",
    kind: "EVENT",
    body: "the event body",
    reply_count: 0,
    created_at: new Date(0).toISOString(),
  };
}
