// The front door's role catalog: the decided matrix's answer to "who walks
// this box, and what does each role open".
//
// The source is ROLE-ACCESS-r3 (principal/planning/role-access-r3.json, DECIDED,
// read only; this branch does not carry a copy of it). The rule the D2 brief
// gives, applied to that record once, by hand, here:
//
//   - The eight roles whose logins the demo mints and whose screens this box
//     serves are LIVE: inside_sales, sales_manager, yard_manager, yard_team,
//     payables_receivables, procurement_manager, driver and
//     financial_controller, labelled on the cards the way the demo adapter's
//     contract names them ("Sales", "Sales manager", "Yard manager", "Yard
//     Team", "Payables and Receivables", "Procurement manager", "Driver",
//     "Financial Controller" - the flows route's own labels, the same rule
//     the till's entry view follows, so every name-based selector keeps
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
//     the same Money Desk tile, on the same path.
//   - The five others are COMING. Each shows the modules the matrix gives
//     that role at work, manage or admin level, by the surfaces table's own
//     names; read cells (and none) show nothing. A coming role is never
//     tappable.
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
const DEALER_ADMIN_TILE: ModuleTile = { name: "Dealer Admin", icon: "lucide:settings", href: "/dealer-admin/" };
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
    | "financial_controller";
  /** The demo flow id the mint takes, the flows table's own mapping. */
  readonly flow:
    | "sales"
    | "sales-manager"
    | "yard-manager"
    | "yard-team"
    | "payables-receivables"
    | "procurement-manager"
    | "driver"
    | "financial-controller";
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
    tiles: [MONEY_DESK_TILE, MANAGER_HOME_TILE, DEALER_ADMIN_TILE, HOUSE_MESSAGES_TILE],
  },
];

/**
 * The five coming roles, in the till's entry view's own order, each with
 * the modules ROLE-ACCESS-r3 gives it at work, manage or admin level (read
 * cells and none show nothing). Outside Sales has no till (rows 363 and 364);
 * HR's two are House Messages and Dealer Admin, and the matrix gives its
 * demo login nothing until the staff routes exist.
 */
export const COMING_ROLES: readonly ComingRole[] = [
  { id: "outside_sales", label: "Outside Sales", modules: ["Quote Desk", "House Messages"] },
  {
    id: "logistics_manager",
    label: "Logistics Manager",
    modules: ["Dispatch and Load Building", "Manager Home", "House Messages"],
  },
  {
    id: "branch_manager",
    label: "Branch Manager",
    modules: [
      "Counter Till",
      "Quote Desk",
      "Dispatch and Load Building",
      "Manager Home",
      "House Messages",
    ],
  },
  {
    id: "general_manager",
    label: "General Manager",
    modules: ["Money Desk", "Manager Home", "House Messages", "Dealer Admin"],
  },
  { id: "hr", label: "HR", modules: ["House Messages", "Dealer Admin"] },
];

/**
 * The contractor roles: Summit Ridge Builders' five personas (the flows listing's rows of class
 * "tenant"), each routed to the Pro app for its role. The label is the Pro kit's own word for the
 * role (the card and the Pro frame say the same thing); the listing's own label ("Contractor
 * owner") is the demo table's and is never shown here. A card does not mint: it opens the Pro app
 * at its prefix with the role code (`?as=`), and the Pro app signs that persona in itself in the
 * demo build, because a Pro app keeps its bearer in memory and nothing in web storage.
 */
export interface ContractorRole {
  readonly roleCode: "account_admin" | "buyer" | "buyer_no_pay" | "ap_finance" | "field_crew";
  readonly label: string;
  readonly line: string;
  /** The Pro app the role lands in, in the words a person reads. */
  readonly app: "Pro Procurement" | "Pro Pay" | "Pro Field";
  /** That app's prefix on the box. */
  readonly prefix: "/pro-procurement/" | "/pro-pay/" | "/pro-field/";
  readonly icon: string;
}

export const CONTRACTOR_ROLES: readonly ContractorRole[] = [
  { roleCode: "account_admin", label: "Owner", line: "Run the account: buy, pay and see every job.", app: "Pro Procurement", prefix: "/pro-procurement/", icon: "lucide:key-round" },
  { roleCode: "buyer", label: "Buyer", line: "Plan the jobs, order the material and pay for it.", app: "Pro Procurement", prefix: "/pro-procurement/", icon: "lucide:shopping-cart" },
  { roleCode: "buyer_no_pay", label: "Project manager", line: "Plan the jobs and order, with payment left to accounts.", app: "Pro Procurement", prefix: "/pro-procurement/", icon: "lucide:clipboard-list" },
  { roleCode: "ap_finance", label: "Accounts payable", line: "Pay the invoices and keep the payment methods.", app: "Pro Pay", prefix: "/pro-pay/", icon: "lucide:landmark" },
  { roleCode: "field_crew", label: "Field crew", line: "Track the deliveries and pickups for the job.", app: "Pro Field", prefix: "/pro-field/", icon: "lucide:truck" },
];

/** One contractor card: the role and where it opens. */
export interface ContractorEntry extends ContractorRole {
  /** The Pro app's prefix with the persona's role code. */
  readonly href: string;
}

/**
 * The contractor cards the flows listing offers: a row counts only when its class is "tenant" and its
 * role code is one the table knows; every other row (a staff flow, a class that is missing, a role the
 * table does not hold) offers nothing. The order is the table's, never the listing's.
 */
export function contractorEntries(flows: unknown): readonly ContractorEntry[] {
  if (!Array.isArray(flows)) return [];
  const offered = new Set<string>();
  for (const row of flows as Array<Record<string, unknown> | null>) {
    if (row !== null && typeof row === "object" && row["class"] === "tenant" && typeof row["role_code"] === "string") offered.add(row["role_code"]);
  }
  return CONTRACTOR_ROLES.filter((role) => offered.has(role.roleCode)).map((role) => ({ ...role, href: `${role.prefix}?as=${role.roleCode}` }));
}
