// The front door's role catalog: the decided matrix's answer to "who walks
// this box, and what does each role open".
//
// The source is ROLE-ACCESS-r3 (principal/planning/role-access-r3.json, DECIDED,
// read only; this branch does not carry a copy of it). The rule the D2 brief
// gives, applied to that record once, by hand, here:
//
//   - The roles whose logins the demo mints and whose screens this box
//     serves are LIVE: inside_sales, sales_manager, yard_manager, yard_team,
//     payables_receivables, procurement_manager, driver,
//     financial_controller and general_manager, labelled on the cards the
//     way the demo adapter's contract names them ("Sales", "Sales manager", "Yard manager", "Yard
//     Team", "Payables and Receivables", "Procurement manager", "Driver",
//     "Financial Controller", "General Manager" - the flows route's own
//     labels, the same rule the till's entry view follows, so every name-based selector keeps
//     reading exactly those words). The flow ids are the flows table's own:
//     inside_sales -> "sales", sales_manager -> "sales-manager",
//     yard_manager -> "yard-manager", yard_team -> "yard-team". Payables and
//     Receivables' flow is "payables-receivables" and its one live module is
//     the Money Desk; the Procurement manager's flow is
//     "procurement-manager" and its one live module is Buying and Stock; the
//     yard manager's live module is Manager Home under /home/; the Yard
//     Team's one live module is the Yard Pick app; the Driver's flow is
//     "driver" and its one live module is HH Drive; the Financial
//     Controller's flow is "financial-controller" and its one live module is
//     the same Money Desk tile, on the same path; the General Manager's flow
//     is "general-manager" and its live modules are the Money Desk and
//     Manager Home, with House Messages and Dealer Admin still href null;
//     HR's flow is "hr" and both its tiles (House Messages, Dealer Admin) are
//     still href null, so its home reads "coming" until those apps exist;
//     the Branch Manager's flow is "branch-manager" and its tiles are the
//     Counter Till, the Quote Desk and Manager Home (live) with Dispatch and
//     Load Building and House Messages still href null; the Logistics
//     Manager's flow is "logistics-manager" and its only live tile is Manager
//     Home, with Dispatch and Load Building and House Messages href null;
//     Outside Sales' flow is "outside-sales" and its tiles are the Quote Desk
//     (live) and House Messages (href null).
//   - Any others are COMING (none today). Each shows the modules the matrix
//     gives that role at work, manage or admin level, by the surfaces
//     table's own names; read cells (and none) show nothing. A coming role is
//     never tappable.
//   - The manager's five tiles are the brief's own list: the two live
//     modules of the box plus Manager Home, Dealer Admin and House Messages,
//     the modules the matrix gives him at work or manage level, shown as the
//     modules he will see and never navigable to nothing (they open the
//     polite coming-soon page). The yard manager's five follow the same rule:
//     Manager Home, the Yard Pick app and Buying and Stock (live, under
//     /home/, /yard/ and /buying-and-stock/) plus the two modules the matrix
//     gives the role that the box does not serve yet.
//
// A module the matrix gives a live role but the box does not serve yet is a
// coming tile for that role, not a silent omission: the chips and the tiles
// tell the same story.
//
// A role goes live by the steps in web/apps/_role-template/ROLE-GOES-LIVE.md
// (the flow row, the populate account, the move from COMING_ROLES to
// LIVE_ROLES, the app's prefix in the image and the common acceptance legs).

/** A module tile on a role's home. */
export interface ModuleTile {
  /** The surface's own name from the matrix's surfaces table. */
  readonly name: string;
  /** The ported registry id the tile draws. */
  readonly icon: string;
  /**
   * Where the tile goes. A path navigates the box (D1 wires the routes);
   * null is a module that is not on the box yet, which opens the polite
   * coming-soon page instead of nothing.
   */
  readonly href: string | null;
}

/** The live roles' tiles, shared by both roles where the box serves them. */
const TILL_TILE: ModuleTile = { name: "Counter Till", icon: "lucide:receipt", href: "/till/" };
const BUYING_TILE: ModuleTile = { name: "Buying and Stock", icon: "lucide:package", href: "/buying-and-stock/" };
const QUOTE_TILE: ModuleTile = { name: "Quote Desk", icon: "lucide:file-pen-line", href: "/quote/" };
const MONEY_DESK_TILE: ModuleTile = { name: "Money Desk", icon: "lucide:landmark", href: "/money-desk/" };
const DRIVE_TILE: ModuleTile = { name: "HH Drive", icon: "lucide:truck", href: "/drive/" };
const MANAGER_HOME_TILE: ModuleTile = { name: "Manager Home", icon: "lucide:house", href: "/home/" };
const DEALER_ADMIN_TILE: ModuleTile = { name: "Dealer Admin", icon: "lucide:settings", href: null };
const HOUSE_MESSAGES_TILE: ModuleTile = {
  name: "House Messages",
  icon: "lucide:message-square",
  href: null,
};
// The tile says what it opens: the app under /yard/ serves the pick half, so
// the tile carries the app's own short name, not the matrix surface's fuller
// one ("Yard Pick and Receive", the receive half's module name, which the
// coming chips and the buying desk's prose keep using).
const YARD_PICK_TILE: ModuleTile = { name: "Yard Pick", icon: "lucide:forklift", href: "/yard/" };
const DISPATCH_TILE: ModuleTile = { name: "Dispatch and Load Building", icon: "lucide:route", href: null };
const BUYING_AND_STOCK_TILE: ModuleTile = { name: "Buying and Stock", icon: "lucide:boxes", href: null };

/** A role the demo signs in today: a card on the picker, a home behind it. */
export interface LiveRole {
  readonly id:
    | "inside_sales"
    | "sales_manager"
    | "yard_manager"
    | "yard_team"
    | "payables_receivables"
    | "procurement_manager"
    | "driver"
    | "financial_controller"
    | "general_manager"
    | "hr"
    | "branch_manager"
    | "logistics_manager"
    | "outside_sales";
  /** The demo flow id the mint takes, the flows table's own mapping. */
  readonly flow:
    | "sales"
    | "sales-manager"
    | "yard-manager"
    | "yard-team"
    | "payables-receivables"
    | "procurement-manager"
    | "driver"
    | "financial-controller"
    | "general-manager"
    | "hr"
    | "branch-manager"
    | "logistics-manager"
    | "outside-sales";
  /**
   * The card's label and its accessible name, alone: the demo adapter's
   * contract words, never decorated.
   */
  readonly label: string;
  /**
   * The card's one line of what the role does. The Sales line names both
   * modules the role works, the Quote Desk beside the counter (decision log
   * row 900); the manager's stays the till's entry words.
   */
  readonly line: string;
  /** The registry id the card draws. */
  readonly icon: string;
  /** The modules the role opens, as the card's chips and the home's tiles. */
  readonly tiles: readonly ModuleTile[];
}

/** The roles the decided matrix lists but this box does not mint yet. */
export interface ComingRole {
  readonly id: string;
  /** The matrix's own label for the role. */
  readonly label: string;
  /** The modules the matrix gives the role at work, manage or admin level. */
  readonly modules: readonly string[];
}

export const LIVE_ROLES: readonly LiveRole[] = [
  {
    id: "inside_sales",
    flow: "sales",
    label: "Sales",
    line: "Ring up counter sales and build quotes.",
    icon: "lucide:receipt",
    tiles: [TILL_TILE, QUOTE_TILE],
  },
  {
    id: "sales_manager",
    flow: "sales-manager",
    label: "Sales manager",
    line: "The counter plus approvals and margins.",
    icon: "lucide:badge-check",
    tiles: [TILL_TILE, QUOTE_TILE, MANAGER_HOME_TILE, DEALER_ADMIN_TILE, HOUSE_MESSAGES_TILE],
  },
  {
    id: "yard_manager",
    flow: "yard-manager",
    label: "Yard manager",
    line: "The yard: picks, shortages and deliveries.",
    icon: "lucide:warehouse",
    tiles: [MANAGER_HOME_TILE, YARD_PICK_TILE, DISPATCH_TILE, BUYING_TILE, HOUSE_MESSAGES_TILE],
  },
  {
    id: "yard_team",
    flow: "yard-team",
    label: "Yard Team",
    line: "Pick and stage the orders dispatched to the yard.",
    icon: "lucide:forklift",
    tiles: [YARD_PICK_TILE, HOUSE_MESSAGES_TILE],
  },
  {
    id: "payables_receivables",
    flow: "payables-receivables",
    label: "Payables and Receivables",
    line: "Chase overdue accounts and work the bounced payments.",
    icon: "lucide:landmark",
    tiles: [MONEY_DESK_TILE, MANAGER_HOME_TILE, HOUSE_MESSAGES_TILE],
  },
  {
    id: "procurement_manager",
    flow: "procurement-manager",
    label: "Procurement manager",
    line: "Buy the branch's stock and track what is incoming.",
    icon: "lucide:package",
    tiles: [BUYING_TILE, MANAGER_HOME_TILE, HOUSE_MESSAGES_TILE],
  },
  {
    id: "driver",
    flow: "driver",
    label: "Driver",
    line: "The day's run, stop to proof of delivery.",
    icon: "lucide:truck",
    tiles: [DRIVE_TILE, HOUSE_MESSAGES_TILE],
  },
  {
    id: "financial_controller",
    flow: "financial-controller",
    label: "Financial Controller",
    line: "Decide approvals and keep the ledger.",
    icon: "lucide:landmark",
    tiles: [MONEY_DESK_TILE, MANAGER_HOME_TILE, HOUSE_MESSAGES_TILE],
  },
  {
    id: "general_manager",
    flow: "general-manager",
    label: "General Manager",
    line: "Decide what only leadership may decide, across both branches.",
    icon: "lucide:shield-check",
    tiles: [MONEY_DESK_TILE, MANAGER_HOME_TILE, HOUSE_MESSAGES_TILE, DEALER_ADMIN_TILE],
  },
  {
    id: "hr",
    flow: "hr",
    label: "HR",
    line: "People, messages and the dealer's staff settings.",
    icon: "lucide:users",
    tiles: [HOUSE_MESSAGES_TILE, DEALER_ADMIN_TILE],
  },
  {
    id: "branch_manager",
    flow: "branch-manager",
    label: "Branch Manager",
    line: "Run the branch: the counter, quotes, dispatch and approvals.",
    icon: "lucide:store",
    tiles: [TILL_TILE, QUOTE_TILE, DISPATCH_TILE, MANAGER_HOME_TILE, HOUSE_MESSAGES_TILE],
  },
  {
    id: "logistics_manager",
    flow: "logistics-manager",
    label: "Logistics Manager",
    line: "The dock and the day's deliveries.",
    icon: "lucide:route",
    tiles: [DISPATCH_TILE, MANAGER_HOME_TILE, HOUSE_MESSAGES_TILE],
  },
  {
    id: "outside_sales",
    flow: "outside-sales",
    label: "Outside Sales",
    line: "Build quotes in the field.",
    icon: "lucide:file-pen-line",
    tiles: [QUOTE_TILE, HOUSE_MESSAGES_TILE],
  },
];

/**
 * The roles the decided matrix lists but this box does not mint yet. None is
 * left: every role of ROLE-ACCESS-r3 is live (Outside Sales has no till,
 * rows 363 and 364). The list and the disclosure that draws it stay, so a
 * role the matrix adds later goes in here first and moves to LIVE_ROLES by
 * the same steps.
 */
export const COMING_ROLES: readonly ComingRole[] = [];

const COUNT_WORDS = ["No", "One", "Two", "Three", "Four", "Five", "Six", "Seven", "Eight", "Nine"];

/**
 * The coming disclosure's heading: the count of roles still coming, written
 * out, so it moves as each role goes live and never carries a stale number.
 */
export function comingHeading(count: number): string {
  const word = COUNT_WORDS[count] ?? String(count);
  return count === 1 ? `${word} more role is coming` : `${word} more roles are coming`;
}
