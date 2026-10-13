// Gap 15 of the per role homes: each of the eight manager roles signs in by
// its card and lands on its own profile's board, with the real widget count
// (the unread tile rides beside the profile's widgets) and the needs sources
// the core files for it.
import { expect, test } from "@playwright/test";
import { resetStub, signInByCard } from "./helpers";

const ROLES = [
  { flow: "yard-manager", label: "yard manager", widgets: 7, sub: "The pick queue, the shortages and what is going out.", strip: ["Shortages · open", "Receiving · short", "Drivers", "Warranty"], needs: ["tasks.open_shortages", "tasks.stale_picks", "purchasing.receiving_differences", "weights.pending"] },
  { flow: "logistics-manager", label: "logistics manager", widgets: 7, sub: "The loads, the delivery dates and the trucks and drivers.", strip: ["Drivers", "Warranty"], needs: ["dispatch.unassigned", "dispatch.due_to_commit", "tasks.stale_picks", "weights.pending"] },
  { flow: "branch-manager", label: "branch manager", widgets: 8, sub: "What needs a person first, then the branch numbers.", strip: ["Shortages · open", "Receiving · short", "Drivers", "Warranty"], needs: ["dispatch.due_to_commit", "dispatch.unassigned", "ar.escalation", "pos.parked"] },
  { flow: "sales-manager", label: "sales manager", widgets: 8, sub: "How selling is going, and the quotes the counter cannot commit.", strip: ["Approvals", "Warranty"], needs: ["approvals.waiting", "ar.escalation", "pos.parked"] },
  { flow: "procurement-manager", label: "procurement manager", widgets: 8, sub: "The gaps the yard reports and where each purchase order got to.", strip: ["Shortages · open", "Receiving · short", "Warranty"], needs: ["tasks.open_shortages", "purchasing.receiving_differences"] },
  { flow: "payables-receivables", label: "payables and receivables", widgets: 8, sub: "The receivables book first, then the receipts waiting to be matched.", strip: ["Receiving · short", "Warranty"], needs: ["ar.escalation", "pos.parked"] },
  { flow: "financial-controller", label: "financial controller", widgets: 8, sub: "What carries money first, then the book.", strip: ["Approvals", "Warranty", "Quick actions"], needs: ["approvals.waiting", "ar.escalation", "pos.parked"] },
  { flow: "general-manager", label: "general manager", widgets: 9, sub: "The whole dealer at a glance: what needs you, then the numbers.", strip: ["Shortages · open", "Receiving · short", "Approvals", "Drivers", "Warranty", "Quick actions"], needs: ["approvals.waiting", "ar.escalation", "dispatch.unassigned", "pos.parked"] },
] as const;

for (const role of ROLES) {
  test(`the ${role.label} lands on their own board`, async ({ request, page }) => {
    await resetStub(request);
    await signInByCard(page, role.flow);
    await expect(page.getByRole("heading", { level: 1 })).toContainText(`Welcome back, ${role.label}`);
    // The profile's widgets, plus the one tile nothing measures.
    await expect(page.locator("[data-widget]")).toHaveCount(role.widgets + 1);
    const sources = await page.locator("[data-needs]").evaluateAll((nodes) => nodes.map((n) => n.getAttribute("data-needs")));
    expect(sources).toEqual([...role.needs]);
    await expect(page.locator(".page-sub")).toHaveText(role.sub);
    // The strip is the role's own; the reporting link rides the tier, not the profile.
    const strip = await page.locator(".view-links .view-link").evaluateAll((nodes) => nodes.map((n) => (n.textContent ?? "").replace("›", "").trim()));
    expect(strip.filter((w) => w !== "Reporting")).toEqual([...role.strip]);
  });
}
