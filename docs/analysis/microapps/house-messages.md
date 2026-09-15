> Origin: `analysis/microapps/house-messages.md` in `futurebuildai/hh_pro_dibbits` at commit 829d387baec93556ffa0e9242f4091e9be9544ec (read only source).
> Copied into `hh/docs/analysis/` by Session HH-2 lane HH-2.5; content below this header is unchanged.
> In-body `analysis/...` paths resolve to `docs/analysis/...` in this copy.

# house-messages: House Messages

Audience: internal   Roles: every internal role (frontline through leadership)   Devices: desktop, tablet, phone, kiosk   Environment: counter, yard, truck, office
Kit: inbox   Brand: fb-tokens   Shell: hh-work and hh-field
Offline: read_cache_plus_outbox. Scopes: channels, messages, acks, notify_prefs. Conflict policy: messages server_wins, acks server_wins, notify_prefs last_write_wins_by_field. Why this mode: the people who most need this are in the yard and in a cab, and a one-tap acknowledgement that fails because of a dead spot is the acknowledgement that gets skipped. Messages themselves are server ordered, so a queued post replays in order and never rewrites history.

## Three jobs
1. Read the channels that concern this person, with an unread count and the house queue of unclaimed conversations (`listChannels`, `unreadSummary`, `messaging-house-queue`, in `hardscapeos_dibbits/app/src/messaging-hub.ts`).
2. Post, reply in thread, or send one of the canned one-tap acknowledgements written for the yard and driver floor (`app/src/messaging-hub.ts`, the quick-replies note).
3. Acknowledge a forced-ack alert on a shared screen, which blocks the screen with the latest alert and a single large control until it is tapped (`app/src/kiosk-takeover.ts`).

## Shared entities and handoffs
- message_thread: read and write; every other micro-app can deep link a record into a thread via `fb://hh/message_thread/<id>`
- approval_request: read; a card fans out to each eligible approver's own channel, resolved from the same authority function decide enforces (`ResolveApprovers` in `backend/internal/identity/rbac.go`)
- order, invoice, delivery_stop: read by reference from a rollup line, which is how a conversation reaches the record it is about (`app/src/_messaging-rollup.ts`)
- payment: read; the payments card click-through resolves to a thread rather than a second screen (`app/src/_payments-tile.ts`)

## What it replaces now
- HardscapeOS: `messaging/messaging` (`hos-messaging-hub`, `app/src/messaging-hub.ts`), the standalone full-screen second screen `messages.html` (`app/messages.html`), and the shared-screen forced-acknowledgement overlay mounted behind a flag on the main document (`app/src/kiosk-takeover.ts`)
- HH Pro: nothing. The contractor's notifications live in their own activity feed (`hh_pro_dibbits/src/ui/components/ActivitySheet.tsx`), which stays there.

## States to design once
- empty: no unread is the resting state and is stated plainly; an empty channel names who is in it.
- loading: the thread pins to the bottom while it fills, since the shipped hub already owns that behaviour (`_messaging-layout.ts`).
- offline: the queued count sits on the composer, and a forced-ack captured offline is marked pending rather than silently accepted.
- conflict: a message the server ordered differently takes the server order; nothing here merges.
- error: a failed load says which channel failed and offers a retry in place, not a full-screen fault.

## Device and environment notes
Four devices, and the kiosk one is the reason the overlay exists at all: a shared screen or a forklift tablet where the alert must block the screen and be cleared with one large tap (`app/src/kiosk-takeover.ts`). A shared screen needs the device unlock context 4.5 requires, and the audit must record which person acknowledged, not which tablet. The rollup and notification preferences resolve against the configured quiet window rather than the process timezone, which is a standing rule of this product (`hardscapeos_dibbits/CLAUDE.md`). Voice input and speech output are already wired for hands-busy use (`_voice.ts`, imported by the hub).

## Sources
- hardscapeos_dibbits/app/src/messaging-hub.ts
- hardscapeos_dibbits/app/messages.html
- hardscapeos_dibbits/app/src/kiosk-takeover.ts
- hardscapeos_dibbits/app/src/dashboard-nav.ts
- hardscapeos_dibbits/app/src/nav-access.ts
- hardscapeos_dibbits/backend/internal/identity/rbac.go
- hardscapeos_dibbits/CLAUDE.md
