// The app's one door to the core: every read and write House Messages makes, over the generated
// client, answering a Result and never throwing. Every route here is a route the core already
// serves (core/internal/messaging). The app draws no money of its own: a body the core sends is
// shown as sent.
//
// Reads: the caller's channels, a channel's messages (a page at a time, newest first), a thread's
// replies, the unread summary, the caller's unacknowledged forced-ack messages, a message's ack
// arithmetic, the house queue, the caller's notify prefs for a channel and their payment rules.
// Writes: post a message or a reply, mark a channel read, acknowledge a message, claim a house
// conversation, set notify prefs and one payment rule. And the stream ticket the live feed mints.
import {
  staffMessaging,
  type ApiClient,
  type MessagingAck,
  type MessagingAckProgress,
  type MessagingChannel,
  type MessagingHouseConversation,
  type MessagingMessage,
  type MessagingNotifyPrefs,
  type MessagingPaymentRulePrefs,
  type MessagingPrefsPatch,
  type MessagingStreamTicket,
  type MessagingUnreadSummary,
  type Result,
} from "@hardscapehouse/api-client";

/** One page of a channel's messages, newest first, and the cursor of the page before it. */
export interface MessagePage {
  readonly messages: readonly MessagingMessage[];
  readonly nextCursor: string | null;
}

/** The three payment notification rules the core closes the set at. */
export type PaymentRuleId = "ONLINE" | "CROSS_BRANCH" | "OWN_ORDER";

// The contract names a page's rows `items`; the core's handler writes `messages` (messaging/model.go
// MessagePage). Both are read, so the screen works against the core as it is and as it is documented.
interface WirePage {
  items?: readonly MessagingMessage[] | null;
  messages?: readonly MessagingMessage[] | null;
  next_cursor?: string | null;
}

export class MessagesApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  channels(): Promise<Result<MessagingChannel[]>> {
    return staffMessaging.messagingListChannels(this.#client).then((r) => (r.ok ? { ok: true as const, value: r.value.channels ?? [] } : r));
  }

  async messages(channelId: string, cursor: string | null, limit: number): Promise<Result<MessagePage>> {
    const opts: { cursor?: string; limit?: number } = { limit };
    if (cursor !== null && cursor !== "") opts.cursor = cursor;
    const r = await staffMessaging.messagingListMessages(this.#client, channelId, opts);
    if (!r.ok) return r;
    const wire = r.value as unknown as WirePage;
    return { ok: true, value: { messages: wire.items ?? wire.messages ?? [], nextCursor: wire.next_cursor ? wire.next_cursor : null } };
  }

  thread(channelId: string, rootId: string): Promise<Result<MessagingMessage[]>> {
    return staffMessaging.messagingListThread(this.#client, channelId, rootId).then((r) => (r.ok ? { ok: true as const, value: r.value.messages ?? [] } : r));
  }

  unread(): Promise<Result<MessagingUnreadSummary>> {
    return staffMessaging.messagingGetUnread(this.#client);
  }

  unacked(): Promise<Result<MessagingMessage[]>> {
    return staffMessaging.messagingListUnacked(this.#client).then((r) => (r.ok ? { ok: true as const, value: r.value.messages ?? [] } : r));
  }

  ackProgress(channelId: string, messageId: string): Promise<Result<MessagingAckProgress>> {
    return staffMessaging.messagingGetAckProgress(this.#client, channelId, messageId);
  }

  houseQueue(channelId: string): Promise<Result<MessagingHouseConversation[]>> {
    return staffMessaging.messagingListHouseQueue(this.#client, channelId).then((r) => (r.ok ? { ok: true as const, value: r.value.conversations ?? [] } : r));
  }

  notifyPrefs(channelId: string): Promise<Result<MessagingNotifyPrefs>> {
    return staffMessaging.messagingGetNotifyPrefs(this.#client, channelId);
  }

  paymentRules(): Promise<Result<MessagingPaymentRulePrefs>> {
    return staffMessaging.messagingGetPaymentRules(this.#client);
  }

  // ---- writes

  post(channelId: string, body: string, threadRootId: string | null): Promise<Result<MessagingMessage>> {
    return staffMessaging.messagingPostMessage(this.#client, channelId, threadRootId === null ? { body } : { body, thread_root_id: threadRootId });
  }

  markRead(channelId: string): Promise<Result<unknown>> {
    return staffMessaging.messagingMarkRead(this.#client, channelId);
  }

  ack(channelId: string, messageId: string): Promise<Result<MessagingAck>> {
    return staffMessaging.messagingAckMessage(this.#client, channelId, messageId, { method: "TAP" });
  }

  claim(conversationId: string): Promise<Result<MessagingHouseConversation>> {
    return staffMessaging.messagingClaimHouseConversation(this.#client, conversationId);
  }

  setNotifyPrefs(channelId: string, patch: MessagingPrefsPatch): Promise<Result<MessagingNotifyPrefs>> {
    return staffMessaging.messagingPutNotifyPrefs(this.#client, channelId, patch);
  }

  setPaymentRule(rule: PaymentRuleId, enabled: boolean): Promise<Result<MessagingPaymentRulePrefs>> {
    return staffMessaging.messagingPutPaymentRule(this.#client, rule, { enabled });
  }

  streamTicket(): Promise<Result<MessagingStreamTicket>> {
    return staffMessaging.messagingMintStreamTicket(this.#client);
  }
}
