// THE FIELD PORT. This file is the seam plan B.6 names: every read Pro Field
// makes, and its one queued write (delivery instructions, the offline exit),
// goes through the FieldPort interface, and the shells cycle's offline store
// and sync go BEHIND that interface. The screens ask the port; they never
// learn whether the answer came off the network or off the store. Nothing here
// queues: the live implementation below is a straight pass over the generated
// tenant client, and the outbox is that cycle's work, not this app's.
//
// The team and account writes (role, status, invite, profile, password) stay
// OFF the port on purpose: they are management actions that must never ride a
// queued store, so they call the client directly where they are used.
import type {
  ApiClient,
  Result,
  TenantAddress,
  TenantInvite,
  TenantMe,
  TenantOrder,
  TenantOrderDetail,
  TenantProjectDashboard,
  TenantProjectSummary,
  TenantReturnEligibility,
  TenantReturnRequest,
  TenantReturnRequestCreate,
  TenantStatusResponse,
  TenantUser,
} from "@hardscapehouse/api-client";
import { tenantAddresses, tenantAuth, tenantOrders, tenantProjects, tenantTeam } from "@hardscapehouse/api-client";

export type OrdersListAnswer = Result<TenantOrder[]> & { truncated?: boolean };

export interface FieldPort {
  /** Every order of the account, the core's keyset order, cursors followed to the end. */
  listOrders(): Promise<OrdersListAnswer>;
  getOrder(id: string): Promise<Result<TenantOrderDetail>>;
  /** The one write the offline plan covers. Everything else in this app never queues. */
  setDeliveryInstructions(id: string, instructions: string): Promise<Result<TenantStatusResponse>>;
  getReturnEligibility(id: string): Promise<Result<TenantReturnEligibility>>;
  createReturnRequest(id: string, body: TenantReturnRequestCreate): Promise<Result<TenantReturnRequest>>;
  listReturnRequests(): Promise<Result<TenantReturnRequest[]>>;
  listProjects(): Promise<Result<TenantProjectSummary[]>>;
  getProject(id: string): Promise<Result<TenantProjectDashboard>>;
  listAddresses(): Promise<Result<TenantAddress[]>>;
  getAddress(id: string): Promise<Result<TenantAddress>>;
  getMe(): Promise<Result<TenantMe>>;
  listUsers(): Promise<Result<TenantUser[]>>;
  listInvites(): Promise<Result<TenantInvite[]>>;
}

/** A read page walk's honest bound: the core pages in fifties, so this is many screens of orders. */
const MAX_PAGES = 100;

export function liveFieldPort(client: ApiClient): FieldPort {
  return {
    async listOrders() {
      const items: TenantOrder[] = [];
      let cursor: string | undefined;
      for (let page = 0; page < MAX_PAGES; page += 1) {
        const answer = await tenantOrders.tenantListOrders(client, cursor === undefined ? {} : { cursor });
        if (!answer.ok) return answer;
        items.push(...answer.value.items);
        const next = answer.value.next_cursor;
        if (typeof next !== "string" || next === "") return { ok: true, value: items, truncated: false };
        cursor = next;
      }
      return { ok: true, value: items, truncated: true };
    },
    async getOrder(id) {
      return tenantOrders.tenantGetOrder(client, id);
    },
    async setDeliveryInstructions(id, instructions) {
      return tenantOrders.tenantSetDeliveryInstructions(client, id, { instructions });
    },
    async getReturnEligibility(id) {
      return tenantOrders.tenantGetReturnEligibility(client, id);
    },
    async createReturnRequest(id, body) {
      return tenantOrders.tenantCreateReturnRequest(client, id, body);
    },
    async listReturnRequests() {
      return tenantOrders.tenantListReturnRequests(client);
    },
    async listProjects() {
      const answer = await tenantProjects.tenantListProjectRollup(client);
      return answer.ok ? { ok: true, value: answer.value.projects } : answer;
    },
    async getProject(id) {
      return tenantProjects.tenantGetProjectRollup(client, id);
    },
    async listAddresses() {
      const answer = await tenantAddresses.tenantListAddresses(client);
      return answer.ok ? { ok: true, value: answer.value.addresses } : answer;
    },
    async getAddress(id) {
      return tenantAddresses.tenantGetAddress(client, id);
    },
    async getMe() {
      return tenantAuth.tenantGetMe(client);
    },
    async listUsers() {
      return tenantTeam.tenantListUsers(client);
    },
    async listInvites() {
      return tenantTeam.tenantListInvites(client);
    },
  };
}
