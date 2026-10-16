# House Messages: provenance of the ports (part A)

The channel list, the conversation with its threads, posting, marking read, the forced acknowledgement,
the house queue and a person's notify choices carry logic and words from the original HardscapeOS app
(repository `hardscapeos_dibbits`, read only), master 87d7941, under the roadmap's row 563 rule (take the
existing and improve it). The logic ports as plain code onto HH's generated api-client; the screens are
rebuilt on HH's design system and the role app template's shell.

Visual reference: no FB Design board exists for House Messages yet, so the reference is the original's
messaging screens (`messaging-hub.ts` and the files below) rebuilt at the standard of the HH Drive and
Buying and Stock apps: cards border first with no shadow, status as a word before a colour, figures in
tabular columns, 44 pixel targets, light by default and brand one tap away, the channel rail beside the
conversation from 900 pixels and one column with a Back control under it. Screens are looked at in
`hh-s4/modules/reports/messages-shots/`.

| What | Source path in the original app | Lands in | What changed |
| --- | --- | --- | --- |
| The hub: channel list with unread counts, the stream, one level threads, the composer | `app/src/messaging-hub.ts` | `src/views.ts` (rail, conversation, composer), `src/desk.ts` | Split into a model, templates and a controller; the rail is grouped (direct messages, rooms, branch, approvals); a draft belongs to its room and never travels |
| The headless page | `app/src/messages.ts` | the app itself, under `/messages/` | The demo's front door handoff replaces the original's sign in page |
| Forced acknowledgement bubble: oldest first, never a superseded one, no way to dismiss it, kept up when a read fails | `app/src/messaging-ack-bubble.ts`, `_messaging-conversation.ts` (`bubbleFor`, `bubblePersistsAfter`) | `bubbleFor`, `bubblePersistsAfter`, the bubble in `src/views.ts` | The write is held to the core: the bubble goes when the core lists nothing waiting, and a refusal keeps it with the core's words |
| The ack arithmetic in words | `_messaging-conversation.ts` (`ackProgressLabel`) | `ackProgressLabel` | Sentences lose their dashes; the count shows for every notice in the escalation room, with the caller's own answer read from the core's acks |
| The house queue: unclaimed first, longest waiting first, claim, the sticky words, a lost race | `app/src/messaging-house-queue.ts`, `_messaging-conversation.ts` (`queueOrder`) | `queueOrder`, `waitedWords`, the queue in `src/views.ts` and `src/desk.ts` | A lost race is the core's sentence, said after the queue is read again |
| Notify modes, the escalation room that cannot be muted, the room that arrives quiet | `app/src/messaging-notify-prefs.ts`, `_messaging-conversation.ts` (`canMute`, `modesFor`, `MUTE_REFUSAL`) | `canMute`, `modesFor`, `MUTE_REFUSAL`, the prefs sheet | A control moves only to what the core answers; the original showed the payment rules inside a payments room's sheet, here they are one tap from the rail for every caller because the core answers them for every staff caller and keeps them self scoped |
| The error banner rule (a success clears it) | `app/src/_messaging-error.ts` | `refusalWords`, the stale strip | A failed read now says the screen shows what was read before, and clears when a read works |
| The API client | `app/src/messaging-api.ts` | `src/messages-api.ts` | Over the generated client, answering a Result; a page is read as the core writes it (`messages`) and as the contract documents it (`items`) |
| The poll fallback | `messaging-hub.ts` (10 second timer) | `src/live.ts` | The stream is tried first (ticket, then the server sent events); the timer reads the cheap unread summary and reads the lists only when it moved |

## Original routes this part covers (Appendix A of the coverage map, the messaging rows)

Reads: `GET /api/v1/messaging/channels`, `/channels/{id}/messages`, `/channels/{id}/threads/{rootID}`,
`/channels/{id}/messages/{msgID}/ack-progress`, `/channels/{id}/house-queue`, `/channels/{id}/notify-prefs`,
`/unread`, `/unacked`, `/payment-rules`, `/stream` (through the stream ticket).
Writes: `POST /api/v1/messaging/channels/{id}/messages`, `/channels/{id}/read`,
`/channels/{id}/messages/{msgID}/ack`, `/house-conversations/{convID}/claim`, `/stream-ticket`;
`PUT /api/v1/messaging/channels/{id}/notify-prefs`, `/payment-rules/{rule}`.

That is 17 of the 25 kept routes. `POST /api/v1/messaging/inbound/customer` is the portal and SMS bridges'
server side ingress, not a screen; the acceptance run uses it to put a customer in the queue.

## Not ported yet (part B)

The admin items, which the core gates to the general manager at admin level: the event class list and its
write (`GET` and `PUT /event-classes`), the export (`GET /export`), the rep assignments and the route
preview (`GET`, `POST`, `DELETE /rep-assignments`, `GET /rep-route`): 8 routes. Also: founding a room,
opening a direct message and seating members (`POST /channels`, `/dms`, `/channels/{id}/members`, `DELETE
.../members/{userID}`, which the core serves and the original never had); the day's payments rollup
drawn as a day line with "today" and "yesterday" words (`_messaging-rollup.ts`; a rollup is a system
notice with a thread now); the canned quick replies (`DIB-277`); an approval card's decide action (the
card shows its words and status); deep links from a notice into the record it names (the box has no route
to open yet); mentions (the core sets the flag, the screen shows the badge).
