// THE BOX'S OWN MONTH, AND THE CORE'S OWN READS (W2-4 (c) and (d)).
//
// WHY THIS FILE EXISTS. Two acceptance-real specs need the same two things: a read of the core the way the desk
// reads it (the bearer the app itself sent, on the box's own base URL), and the box's own month, read at run
// time. The ledger spec (c) wrote them first; the bank reconciliation spec (d) needs the month's derivation too,
// because the world's anchor replaces it. One copy lives here and both specs import it, so the two cannot drift
// apart.
//
// THE BOX'S OWN MONTH, WHY IT IS READ AND NEVER WRITTEN DOWN. migration 0055 dates the seed's two demo
// counterparty invoices CURRENT_DATE, so the month the seed's movement lands in is the month the box was built in,
// and nobody can write that month down in a file: a box built in a later month makes every leg that names a month
// red for nobody's commit (FROM-PRINCIPAL 389, the DIB-427 time bomb class the repo already refuses in Go).
//
//   - THE SEEDED INVOICE'S OWN ISSUE DATE names the boot month. That date is fixed when the migrations ran, so it
//     names the boot month however long after that the spec runs, and it is the month the movement is in.
//   - THE CORE'S OWN REPORTED DATE is the fallback, for a box whose register does not carry that invoice. It is the
//     box's wall clock, so it drifts with the run, and the movement's own month does not.
//
// THE WORLD'S ANCHORS TAKE OVER. While an anchor in ./world is empty the specs read the box's own month; when an
// anchor carries a month the specs read that month. The anchor is the switch, and nothing else moves.
import type { APIRequestContext, Page } from "@playwright/test";
import { coreURL } from "./session.js";
import { BOOT_MONTH_INVOICE_NO, monthOfDate } from "./world.js";

/** The bearer and the org the app itself sent, so a direct read reads the core as the app reads it. */
export interface StaffHeaders {
  readonly authorization: string;
  readonly org: string;
}

/** The AR register's read: the rows it lists and the date it reports. */
export interface CoreARRegister {
  items: { invoice_no: string; issue_date: string }[];
  as_of: string;
}

/** The bearer and the org the app sent on its own first authorised call to the core. */
export function staffHeaders(page: Page): Promise<StaffHeaders> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("the app sent no authorised call to the core")), 60_000);
    page.on("request", (request) => {
      void request.allHeaders().then((headers) => {
        const authorization = headers["authorization"];
        if (authorization === undefined || authorization === "") return;
        clearTimeout(timer);
        resolve({ authorization, org: headers["x-org"] ?? "" });
      });
    });
  });
}

/** One read of the core, the way the app reads it: the bearer it sent, on the box's own base URL. */
export async function coreRead<T>(request: APIRequestContext, headers: StaffHeaders, path: string): Promise<T> {
  const response = await request.get(`${coreURL()}${path}`, {
    headers: {
      authorization: headers.authorization,
      ...(headers.org === "" ? {} : { "x-org": headers.org }),
    },
  });
  if (!response.ok()) {
    throw new Error(`the core refused ${path} with ${response.status()}: ${await response.text()}`);
  }
  return (await response.json()) as T;
}

/**
 * THE BOX'S OWN MONTH (YYYY-MM), read from the core at run time.
 *
 * The seed's movement lands in the month the box booted, and the seed names that month on the demo invoice it
 * plants dated CURRENT_DATE: that invoice's own issue date is fixed when the migrations ran, so it names the boot
 * month however long after that the spec runs. It is the month the seed's rows are in, so a trial balance read for
 * it carries them.
 *
 * The core's own reported date is the fallback, for a box whose register does not carry that invoice: it is the
 * box's wall clock, so it drifts with the run, and the movement's own month does not.
 */
export async function readBoxMonth(request: APIRequestContext, headers: StaffHeaders): Promise<string> {
  const register = await coreRead<CoreARRegister>(
    request,
    headers,
    `/api/v1/ar/register?q=${encodeURIComponent(BOOT_MONTH_INVOICE_NO)}&limit=100`,
  );
  const seeded = register.items.find((row) => row.invoice_no === BOOT_MONTH_INVOICE_NO);
  if (seeded !== undefined) return monthOfDate(seeded.issue_date);
  return monthOfDate(register.as_of);
}
