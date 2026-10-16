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
  type MessagingChannelMember,
  type MessagingHouseConversation,
  type MessagingMessage,
  type MessagingNotifyPrefs,
  type MessagingPaymentRulePrefs,
  type MessagingPrefsPatch,
  type MessagingStreamTicket,
  type MessagingUnreadSummary,
  type Result,
} from "@hardscapehouse/api-client";
import type { StaffMember, ReactionItem } from "./messages-logic.js";

export interface AttachmentItem {
  readonly id: string;
  readonly channel_id: string;
  readonly filename: string;
  readonly content_type: string;
  readonly size_bytes: number;
  readonly author_id: string;
  readonly author_name: string;
  readonly created_at: string;
  readonly caption?: string;
  readonly url?: string;
}

export interface ChannelDirectoryItem {
  readonly id: string;
  readonly name: string;
  readonly topic?: string;
  readonly member_count: number;
  readonly is_member: boolean;
  readonly membership_source: "DERIVED" | "EXPLICIT" | "NONE";
}

export interface MembershipRuleItem {
  readonly channel_id: string;
  readonly channel_name: string;
  readonly joins_automatically: string;
  readonly leaves_automatically: string;
  readonly visibility: "PUBLIC" | "PRIVATE";
}

export interface MemberByHandItem {
  readonly channel_id: string;
  readonly user_id: string;
  readonly display_name: string;
  readonly role_name: string;
  readonly added_by_name: string;
  readonly added_reason: string;
  readonly joined_at: string;
}

export interface EventClassItem {
  readonly topic: string;
  readonly name: string;
  readonly description: string;
}

export interface RepAssignmentItem {
  readonly id: string;
  readonly customer_id: string;
  readonly customer_name: string;
  readonly rep_id: string;
  readonly rep_name: string;
  readonly assigned_at: string;
}

export interface NotificationSettings {
  readonly quiet_hours: {
    readonly enabled: boolean;
    readonly start_time: string;
    readonly end_time: string;
    readonly timezone: string;
  };
  readonly event_classes: Record<string, { readonly in_app: boolean; readonly push: boolean }>;
}

export interface SearchResults {
  readonly query: string;
  readonly messages: readonly { readonly message: MessagingMessage; readonly channel_id: string; readonly channel_name: string }[];
  readonly people: readonly StaffMember[];
  readonly channels: readonly MessagingChannel[];
  readonly files: readonly AttachmentItem[];
}

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

  roster(): Promise<Result<{ roster: StaffMember[] }>> {
    return this.#client.request<{ roster: StaffMember[] }>({
      method: "GET",
      path: "/api/v1/messaging/roster",
      operationId: "messagingRoster",
    });
  }

  createDM(userId: string): Promise<Result<MessagingChannel>> {
    return staffMessaging.messagingCreateDM(this.#client, { user_id: userId });
  }

  createGroup(name: string, memberIds: string[]): Promise<Result<MessagingChannel>> {
    return staffMessaging.messagingCreateGroup(this.#client, { name, member_ids: memberIds });
  }

  createChannel(input: { kind?: "TEAM" | "TOPIC"; name: string; topic?: string; slug?: string; branch_id?: string; visibility?: "PUBLIC" | "PRIVATE" }): Promise<Result<MessagingChannel>> {
    return staffMessaging.messagingCreateChannel(this.#client, {
      kind: input.kind ?? "TOPIC",
      name: input.name,
      topic: input.topic,
      slug: input.slug,
      branch_id: input.branch_id,
    });
  }

  renameChannel(channelId: string, name: string): Promise<Result<MessagingChannel>> {
    return staffMessaging.messagingRenameChannel(this.#client, channelId, { name });
  }

  addMember(channelId: string, userId: string): Promise<Result<MessagingChannelMember>> {
    return staffMessaging.messagingAddChannelMember(this.#client, channelId, { user_id: userId, role: "MEMBER" });
  }

  removeMember(channelId: string, userId: string): Promise<Result<{ status: "ok" }>> {
    return staffMessaging.messagingRemoveChannelMember(this.#client, channelId, userId);
  }

  editMessage(channelId: string, messageId: string, body: string): Promise<Result<MessagingMessage>> {
    return this.#client.request<MessagingMessage>({
      method: "PATCH",
      path: "/api/v1/messaging/channels/{id}/messages/{msgID}",
      pathParams: { id: channelId, msgID: messageId },
      body: { body },
      operationId: "messagingEditMessage",
    });
  }

  deleteMessage(channelId: string, messageId: string): Promise<Result<MessagingMessage>> {
    return this.#client.request<MessagingMessage>({
      method: "DELETE",
      path: "/api/v1/messaging/channels/{id}/messages/{msgID}",
      pathParams: { id: channelId, msgID: messageId },
      operationId: "messagingDeleteMessage",
    });
  }

  channelReactions(channelId: string): Promise<Result<{ reactions: Record<string, ReactionItem[]> }>> {
    return this.#client.request<{ reactions: Record<string, ReactionItem[]> }>({
      method: "GET",
      path: "/api/v1/messaging/channels/{id}/reactions",
      pathParams: { id: channelId },
      operationId: "messagingChannelReactions",
    });
  }

  toggleReaction(channelId: string, messageId: string, emoji: string): Promise<Result<{ reactions: ReactionItem[] }>> {
    return this.#client.request<{ reactions: ReactionItem[] }>({
      method: "PUT",
      path: "/api/v1/messaging/channels/{id}/messages/{msgID}/reactions",
      pathParams: { id: channelId, msgID: messageId },
      body: { emoji },
      operationId: "messagingToggleReaction",
    });
  }

  seenBy(channelId: string, messageId: string): Promise<Result<{ count: number; readers: Array<{ user_id: string; display_name: string; seen_at: string }> }>> {
    return this.#client.request<{ count: number; readers: Array<{ user_id: string; display_name: string; seen_at: string }> }>({
      method: "GET",
      path: "/api/v1/messaging/channels/{id}/messages/{msgID}/seen-by",
      pathParams: { id: channelId, msgID: messageId },
      operationId: "messagingSeenBy",
    });
  }

  mentions(): Promise<Result<{ mentions: readonly { readonly message: MessagingMessage; readonly channel_id: string; readonly channel_name: string }[] }>> {
    return this.#client.request<{ mentions: readonly { readonly message: MessagingMessage; readonly channel_id: string; readonly channel_name: string }[] }>({
      method: "GET",
      path: "/api/v1/messaging/mentions",
      operationId: "messagingMentions",
    });
  }

  saved(): Promise<Result<{ saved: readonly { readonly message: MessagingMessage; readonly channel_id: string; readonly channel_name: string; readonly saved_at: string }[] }>> {
    return this.#client.request<{ saved: readonly { readonly message: MessagingMessage; readonly channel_id: string; readonly channel_name: string; readonly saved_at: string }[] }>({
      method: "GET",
      path: "/api/v1/messaging/saved",
      operationId: "messagingSaved",
    });
  }

  saveMessage(messageId: string): Promise<Result<{ status: "ok"; saved: boolean }>> {
    return this.#client.request<{ status: "ok"; saved: boolean }>({
      method: "POST",
      path: "/api/v1/messaging/saved/{id}",
      pathParams: { id: messageId },
      operationId: "messagingSaveMessage",
    });
  }

  unsaveMessage(messageId: string): Promise<Result<{ status: "ok"; saved: boolean }>> {
    return this.#client.request<{ status: "ok"; saved: boolean }>({
      method: "DELETE",
      path: "/api/v1/messaging/saved/{id}",
      pathParams: { id: messageId },
      operationId: "messagingUnsaveMessage",
    });
  }

  search(q: string): Promise<Result<SearchResults>> {
    return this.#client.request<SearchResults>({
      method: "GET",
      path: "/api/v1/messaging/search",
      query: { q },
      operationId: "messagingSearch",
    });
  }

  directory(): Promise<Result<{ channels: readonly ChannelDirectoryItem[] }>> {
    return this.#client.request<{ channels: readonly ChannelDirectoryItem[] }>({
      method: "GET",
      path: "/api/v1/messaging/directory",
      operationId: "messagingDirectory",
    });
  }

  joinChannel(channelId: string): Promise<Result<{ status: "ok"; is_member: boolean }>> {
    return this.#client.request<{ status: "ok"; is_member: boolean }>({
      method: "POST",
      path: "/api/v1/messaging/channels/{id}/join",
      pathParams: { id: channelId },
      operationId: "messagingJoinChannel",
    });
  }

  leaveChannel(channelId: string): Promise<Result<{ status: "ok"; is_member: boolean }>> {
    return this.#client.request<{ status: "ok"; is_member: boolean }>({
      method: "POST",
      path: "/api/v1/messaging/channels/{id}/leave",
      pathParams: { id: channelId },
      operationId: "messagingLeaveChannel",
    });
  }

  notificationSettings(): Promise<Result<NotificationSettings>> {
    return this.#client.request<NotificationSettings>({
      method: "GET",
      path: "/api/v1/messaging/notification-settings",
      operationId: "messagingNotificationSettings",
    });
  }

  putNotificationSettings(settings: Partial<NotificationSettings>): Promise<Result<NotificationSettings>> {
    return this.#client.request<NotificationSettings>({
      method: "PUT",
      path: "/api/v1/messaging/notification-settings",
      body: settings,
      operationId: "messagingPutNotificationSettings",
    });
  }

  membershipRules(): Promise<Result<{ rules: readonly MembershipRuleItem[] }>> {
    return this.#client.request<{ rules: readonly MembershipRuleItem[] }>({
      method: "GET",
      path: "/api/v1/messaging/membership-rules",
      operationId: "messagingMembershipRules",
    });
  }

  membersByHand(channelId?: string): Promise<Result<{ members: readonly MemberByHandItem[] }>> {
    if (channelId) {
      return this.#client.request<{ members: readonly MemberByHandItem[] }>({
        method: "GET",
        path: "/api/v1/messaging/channels/{id}/members-by-hand",
        pathParams: { id: channelId },
        operationId: "messagingMembersByHand",
      });
    }
    return this.#client.request<{ members: readonly MemberByHandItem[] }>({
      method: "GET",
      path: "/api/v1/messaging/members-by-hand",
      operationId: "messagingAllMembersByHand",
    });
  }

  addMemberByHand(channelId: string, userId: string, reason: string): Promise<Result<MemberByHandItem>> {
    return this.#client.request<MemberByHandItem>({
      method: "POST",
      path: "/api/v1/messaging/channels/{id}/members-by-hand",
      pathParams: { id: channelId },
      body: { user_id: userId, added_reason: reason },
      operationId: "messagingAddMemberByHand",
    });
  }

  eventClasses(): Promise<Result<{ classes: readonly EventClassItem[] }>> {
    return this.#client.request<{ classes: readonly EventClassItem[] }>({
      method: "GET",
      path: "/api/v1/messaging/event-classes",
      operationId: "messagingEventClasses",
    });
  }

  repAssignments(): Promise<Result<{ assignments: readonly RepAssignmentItem[] }>> {
    return this.#client.request<{ assignments: readonly RepAssignmentItem[] }>({
      method: "GET",
      path: "/api/v1/messaging/rep-assignments",
      operationId: "messagingRepAssignments",
    });
  }

  unassignRep(repId: string): Promise<Result<{ status: "ok" }>> {
    return this.#client.request<{ status: "ok" }>({
      method: "DELETE",
      path: "/api/v1/messaging/rep-assignments",
      body: { id: repId },
      operationId: "messagingUnassignRep",
    });
  }

  channelDirectory(): Promise<Result<{ channels: readonly ChannelDirectoryItem[] }>> {
    return this.directory();
  }

  updateEventClassNotif(eventTopic: string, channelSettings: { in_app: boolean; push: boolean }): Promise<Result<NotificationSettings>> {
    return this.putNotificationSettings({ event_classes: { [eventTopic]: channelSettings } });
  }

  updateQuietHours(quietHours: { enabled: boolean; start_time?: string; end_time?: string; timezone?: string }): Promise<Result<NotificationSettings>> {
    return this.putNotificationSettings({ quiet_hours: quietHours as any });
  }

  attachments(channelId: string): Promise<Result<{ attachments: readonly AttachmentItem[] }>> {
    return this.#client.request<{ attachments: readonly AttachmentItem[] }>({
      method: "GET",
      path: "/api/v1/messaging/channels/{id}/attachments",
      pathParams: { id: channelId },
      operationId: "messagingAttachments",
    });
  }

  uploadAttachment(channelId: string, filename: string, caption?: string): Promise<Result<{ attachment: AttachmentItem }>> {
    return this.#client.request<{ attachment: AttachmentItem }>({
      method: "POST",
      path: "/api/v1/messaging/channels/{id}/attachments",
      pathParams: { id: channelId },
      body: { filename, caption },
      operationId: "messagingUploadAttachment",
    });
  }
}
