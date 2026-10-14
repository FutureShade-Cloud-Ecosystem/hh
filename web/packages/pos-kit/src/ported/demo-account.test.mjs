// The staff role cards list the staff flows only. The demo flows route also lists the contractor
// flows (class "tenant", routed to the Pro apps by the front door); a staff app's picker must not
// show them, and a row with no class is a staff row.
import { test } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "@hardscapehouse/api-client";
import { DemoAccount } from "./demo-account.ts";

function client(flows) {
  return new ApiClient({
    baseUrl: "http://127.0.0.1:1",
    fetch: async () =>
      new Response(JSON.stringify({ flows }), { status: 200, headers: { "content-type": "application/json" } }),
  });
}

test("listFlows keeps the staff rows and drops the contractor ones", async () => {
  const account = new DemoAccount({
    client: client([
      { flow: "sales", role_code: "inside_sales", label: "Sales", class: "staff" },
      { flow: "driver", role_code: "driver", label: "Driver" },
      { flow: "contractor-buyer", role_code: "buyer", label: "Contractor buyer", class: "tenant" },
    ]),
  });
  const result = await account.listFlows();
  assert.equal(result.ok, true);
  assert.deepEqual(result.value.map((row) => row.flow), ["sales", "driver"]);
});
