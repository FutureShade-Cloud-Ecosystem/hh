// The front door's role catalog: the decided matrix's answer to "who walks
// this box, and what does each role open".
//
// The source is ROLE-ACCESS-r3 (principal/planning/role-access-r3.json, DECIDED,
// read only; this branch does not carry a copy of it). The rule the D2 brief
// gives, applied to that record once, by hand, here:
//
//   - The five roles whose logins the demo mints today are LIVE:
//     inside_sales, sales_manager, yard_manager, payables_receivables and
//     procurement_manager, labelled on the cards the way the demo
//     adapter's contract names them ("Sales", "Sales manager", "Yard
//     manager", "Payables and Receivables", "Procurement manager" - the
//     flows route's own labels, the same rule the till's entry view
//     follows, so every name-based selector keeps reading exactly those
//     words). The flow ids are the till's own mapping: inside_sales ->
//     "sales", sales_manager -> "sales-manager", yard_manager ->
//     "yard-manager". Payables and Receivables' flow is
//     "payables-receivables" and its one live module is the Money Desk;
//     the Procurement manager's flow is "procurement-manager" and its one
//     live module is Buying and Stock; the yard manager's live module is
//     Manager Home under /home/.
//   - The eight others are COMING. Each shows the modules the matrix gives
//     that role at work, manage or admin level, by the surfaces table's own
//     names; read cells (and none) show nothing. A coming role is never
//     tappable.
//   - The manager's five tiles are the brief's own list: the two live
//     modules of the box plus Manager Home, Dealer Admin and House Messages,
//     the modules the matrix gives him at work or manage level, shown as the
//     modules he will see and never navigable to nothing (they open the
//     polite coming-soon page). The yard manager's five follow the same rule:
//     Manager Home (live, under /home/) plus the four modules the matrix
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
const MANAGER_HOME_TILE: ModuleTile = { name: "Manager Home", icon: "lucide:house", href: "/home/" };
const DEALER_ADMIN_TILE: ModuleTile = { name: "Dealer Admin", icon: "lucide:settings", href: null };
const HOUSE_MESSAGES_TILE: ModuleTile = {
  name: "House Messages",
  icon: "lucide:message-square",
  href: null,
};
const YARD_PICK_TILE: ModuleTile = { name: "Yard Pick and Receive", icon: "lucide:forklift", href: null };
const DISPATCH_TILE: ModuleTile = { name: "Dispatch and Load Building", icon: "lucide:route", href: null };
const BUYING_AND_STOCK_TILE: ModuleTile = { name: "Buying and Stock", icon: "lucide:boxes", href: null };

/** A role the demo signs in today: a card on the picker, a home behind it. */
export interface LiveRole {
  readonly id: "inside_sales" | "sales_manager" | "yard_manager" | "payables_receivables" | "procurement_manager";
  /** The demo flow id the mint takes, the till's own mapping. */
  readonly flow: "sales" | "sales-manager" | "yard-manager" | "payables-receivables" | "procurement-manager";
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
];

/**
 * The eight coming roles, in the till's entry view's own order, each with
 * the modules ROLE-ACCESS-r3 gives it at work, manage or admin level (read
 * cells and none show nothing). Outside Sales has no till (rows 363 and 364);
 * HR's two are House Messages and Dealer Admin, and the matrix gives its
 * demo login nothing until the staff routes exist.
 */
export const COMING_ROLES: readonly ComingRole[] = [
  { id: "outside_sales", label: "Outside Sales", modules: ["Quote Desk", "House Messages"] },
  { id: "yard_team", label: "Yard Team", modules: ["Yard Pick and Receive", "House Messages"] },
  {
    id: "logistics_manager",
    label: "Logistics Manager",
    modules: ["Dispatch and Load Building", "Manager Home", "House Messages"],
  },
  { id: "driver", label: "Driver", modules: ["HH Drive", "House Messages"] },
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
    id: "financial_controller",
    label: "Financial Controller",
    modules: ["Money Desk", "Manager Home", "House Messages"],
  },
  {
    id: "general_manager",
    label: "General Manager",
    modules: ["Money Desk", "Manager Home", "House Messages", "Dealer Admin"],
  },
  { id: "hr", label: "HR", modules: ["House Messages", "Dealer Admin"] },
];
