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
