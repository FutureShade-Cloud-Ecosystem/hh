// The team words: the core's five stored roles as the words a person reads
// (the kit's role labels), and one member row's readable words. The stored
// strings never reach a screen; the server stays the only boundary on what a
// role change may do.
import type { TenantUser } from "@hardscapehouse/api-client";
import { roleLabel, statusWords } from "@hardscapehouse/pro-kit";

export interface RoleOption {
  value: string;
  label: string;
}

/** The picker's options, in the core's stored order, under the kit's role labels. */
export const ROLE_OPTIONS: readonly RoleOption[] = [
  { value: "account_admin", label: "Owner" },
  { value: "buyer", label: "Buyer" },
  { value: "buyer_no_pay", label: "Project manager" },
  { value: "ap_finance", label: "Accounts payable" },
  { value: "field_crew", label: "Field crew" },
];

/** One member row's words, for the tests and for nothing else. */
export function memberRowWords(user: TenantUser): string {
  return [user.name, user.email, roleLabel(user.role), statusWords("team", user.status)].join(" ");
}

/**
 * Whether changing this member's role would demote the last active owner on
 * the account.
 */
export function isLastActiveOwnerDemotion(users: readonly Pick<TenantUser, "id" | "role" | "status">[], userId: string, nextRole: string): boolean {
  const target = users.find((user) => user.id === userId);
  if (target === undefined || target.role !== "account_admin" || target.status !== "active" || nextRole === "account_admin") return false;
  const activeOwners = users.filter((user) => user.role === "account_admin" && user.status === "active");
  return activeOwners.length <= 1;
}

/**
 * The confirmation words before a role change is sent: names the person and
 * the new role, and warns an owner demoting themself that Team access ends as
 * soon as the change saves.
 */
export function roleChangeConfirmWords(user: Pick<TenantUser, "id" | "name" | "role">, nextRole: string, currentUserId: string): string {
  const from = roleLabel(user.role);
  const to = roleLabel(nextRole);
  if (user.id === currentUserId && user.role === "account_admin" && nextRole !== "account_admin") {
    return `Change your role from ${from} to ${to}? You will lose access to Team and user management as soon as this saves.`;
  }
  return `Change ${user.name} from ${from} to ${to}?`;
}
