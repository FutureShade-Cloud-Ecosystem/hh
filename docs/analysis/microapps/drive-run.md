> Origin: `analysis/microapps/drive-run.md` in `futurebuildai/hh_pro_dibbits` at commit 829d387baec93556ffa0e9242f4091e9be9544ec (read only source).
> Copied into `hh/docs/analysis/` by Session HH-2 lane HH-2.5; content below this header is unchanged.

# drive-run: HH Drive

Audience: internal   Roles: driver   Devices: phone   Environment: truck
Kit: guided-flow   Brand: fb-tokens   Shell: hh-field
Offline: full. Scopes: runs, stops, pod, attachments. Conflict policy: stops server_wins, pod manual, attachments last_write_wins_by_field. Why full: the run is read at the side of a road on cellular, which the shipped file states as the reason this became its own entry at all (`hardscapeos_dibbits/app/src/drive.ts`). Proof of delivery is `manual` on conflict because it is an attestation with a name attached, and the product's standing rule is that an artifact must not imply more than it captured (`app/src/quote-public.ts`). The shipped app deliberately has no service worker yet and says so in the document (`app/drive.html`); declaring the scopes is what closes that gap.

## Three jobs
1. Read the run in stop order and move a stop through its lifecycle. One lifecycle matrix, shared with the in-ERP tab (`dispatch-logic.ts`, imported by both `app/src/drive.ts` and `app/src/dispatch-surface.ts`).
2. Capture proof of delivery at the stop, or record a could-not-deliver with its handback sentence (`app/src/dispatch-driver-run.ts`, imported by `drive.ts`).
3. Close the run from the end-of-run summary (`app/src/drive-close.ts`).

## Shared entities and handoffs
- delivery_stop: write; taken from dispatch-board via `fb://hh/delivery_stop/<id>`
- pod: write; read by counter-till at invoicing and by pro-field as a tracking event via `fb://hh/pod/<id>`
- order: read only, and with no figure of any kind on it. The bundle fence exists to keep that true of the shipped bytes, not only of the source (`app/scripts/bundle-fence.mjs`, described in `hardscapeos_dibbits/CLAUDE.md`)
- message_thread: read and acknowledge; house-messages reaches this person here

## What it replaces now
- HardscapeOS: `drive.html` and `app/src/drive.ts`, plus the driver sub-tab of `dispatch/dispatch` (`app/src/dispatch-surface.ts`, where `openingTab()` already sends the DRIVERS role to it)
- HH Pro: nothing. The contractor sees the driver's work as tracking in pro-field.

## States to design once
- empty: no run assigned says so in the driver's words and offers the one exit, which is the link to the full app, because a control whose only outcome is a fault string is not a control (`app/src/drive.ts`).
- loading: the stop list holds its order; a stop must never appear to change position while it loads.
- offline: assumed, not exceptional. The queued count and the last pull are on the chrome at all times.
- conflict: a stop the office re-sequenced takes the server order and says so; a POD never merges, it surfaces.
- error: the run sheet says what to do at the roadside, never an engineering string. That specific failure is recorded as measured and fixed in `app/src/drive.ts`.

## Device and environment notes
One phone, one hand, often in a cradle, as often sideways as not, which is why the manifest pins orientation `any` (`app/drive.html`). The document pins its own dark theme unconditionally because an installed app cannot repaint the launch splash at runtime (same file). There is no nav, no tab strip, no switcher and nothing else routable, because none of it is this person's job (`app/src/drive.ts`), which is the cleanest existing statement of the one-job-per-app rule in either repository. Whether that makes it a micro-app in the internal shell or its own shell is an open question in the catalog.

## Sources
- hardscapeos_dibbits/app/src/drive.ts
- hardscapeos_dibbits/app/drive.html
- hardscapeos_dibbits/app/src/_driver-redirect.ts
- hardscapeos_dibbits/app/src/dispatch-surface.ts
- hardscapeos_dibbits/backend/internal/identity/rbac.go
- hardscapeos_dibbits/CLAUDE.md
