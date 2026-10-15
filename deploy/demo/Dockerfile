# The demo image (plan r7 section 4, item W5, made multi app by Lane 0): one
# image in the Railway shape. Build from the repository root:
#
#   docker build -f deploy/demo/Dockerfile -t hh-demo-till .
#
# The image carries: the core built with -tags demo (the demo issuer, the
# membership source and every boot guard compiled in), the built files of
# every app the box serves, Caddy as the static host in front, and the frozen
# migration set the migrate role reads from its working directory. The
# entrypoint (deploy/demo/entrypoint.sh) is the only place the boot order
# lives: demo-check first, then migrate, demoseed -apply, demo-populate and
# demoseed -apply again (the quote world needs the two role logins the
# populate plants), then serve behind Caddy.
#
# The route plan (one origin behind the one phrase gate):
#
#   /            the front door, the box's role picker; dist at /srv/app
#   /till/*      the counter till, dist at /srv/app/till
#   /q/*         the public desk, the customer's quote page; dist at /srv/app/q
#   /quote.html  the same page, where the server's emailed share link lands
#   /quote/*     the Quote Desk, dist at /srv/app/quote, served whenever
#                web/apps/quote-desk exists in the build context; while it is
#                absent the directory is empty and the prefix answers 404, so
#                the image needs no edit when the desk lands
#   /api/*, /healthz  the core, proxied
#
# Each app is built once with Vite's --base flag, so its index.html and assets
# carry their own prefix and no app source changes for serving. The staff
# apps (the till and the Quote Desk) build with --mode demo, the only Vite
# mode whose build keeps the front door handoff's adopt reader compiled in; a
# default build ships the empty stub in its place (rows 893 and 899). A new
# role app joins by one build line below, one COPY line and one handle block
# in the Caddyfile template (web/apps/_role-template/ROLE-GOES-LIVE.md).

FROM golang:1.27-alpine@sha256:738d1cf061836894ff6bb8c33881080ac66de8cf0586615012a0c8f592649cfa AS core-build
WORKDIR /src/core
COPY core/go.mod core/go.sum ./
RUN go mod download
COPY core/ ./
RUN CGO_ENABLED=0 go build -trimpath -tags demo -o /out/core ./cmd/core

FROM node:22-alpine AS web-build
# The whole workspace is copied before the install: npm resolves the
# workspaces from the packages' directories on disk, so a manifest-only
# copy installs nothing but the root.
# The drive app's offline data layer imports the sync statechart, which sits beside the
# web workspace (web/apps/drive-run/src/drive-offline.ts), so that one folder comes too.
COPY statecharts/ /src/statecharts/
WORKDIR /src/web
COPY web/ ./
RUN npm ci
# The builds run from inside each app's directory so npm puts the
# workspace root's binaries on PATH (vite is hoisted there). Every app lands
# in /out under the name of its prefix, so the final stage copies one tree.
RUN mkdir -p /out/till /out/q /out/quote /out/drive /out/home /out/yard /out/money-desk /out/dispatch /out/dealer-admin /out/pro-procurement /out/pro-pay /out/pro-field
WORKDIR /src/web/apps/front-door
RUN npx vite build --outDir /out/root
# The front door has no default sign in mode (it reads /config.json), so this
# image says its own: the demo box is the tap a role page. Written after the
# build, which empties its output directory.
RUN printf '{"mode":"demo"}' > /out/root/config.json
WORKDIR /src/web/apps/counter-till
RUN npx vite build --mode demo --base=/till/ --outDir /out/till --emptyOutDir
# Buying and Stock, the procurement slice: a staff app, so the demo mode
# compiles the front door's handoff in, under its own prefix.
RUN mkdir -p /out/buying-and-stock
WORKDIR /src/web/apps/buying-and-stock
RUN npx vite build --mode demo --base=/buying-and-stock/ --outDir /out/buying-and-stock --emptyOutDir
WORKDIR /src/web/apps/public-desk
RUN npx vite build --base=/q/ --outDir /out/q --emptyOutDir
# HH Drive, the driver's run app: a staff app, so the demo mode compiles the
# front door's handoff in, under its own prefix.
WORKDIR /src/web/apps/drive-run
RUN npx vite build --mode demo --base=/drive/ --outDir /out/drive --emptyOutDir
# Manager Home, every manager role's landing: a staff app built the same way
# (demo mode, own prefix), so a manager's front door tile hands off into it.
WORKDIR /src/web/apps/manager-home
RUN npx vite build --mode demo --base=/home/ --outDir /out/home --emptyOutDir
# Yard Pick, the Yard Team's pick app: a staff app, the same shape as HH Drive.
WORKDIR /src/web/apps/yard-pick
RUN npx vite build --mode demo --base=/yard/ --outDir /out/yard --emptyOutDir
# The Money Desk, receivables and the ledger: a staff app, so the demo mode
# compiles the front door's handoff in, under its own prefix.
WORKDIR /src/web/apps/money-desk
RUN npx vite build --mode demo --base=/money-desk/ --outDir /out/money-desk --emptyOutDir
# The dispatch board, the Logistics Manager's load board and day board: a staff
# app, the same shape as HH Drive.
WORKDIR /src/web/apps/dispatch-board
RUN npx vite build --mode demo --base=/dispatch/ --outDir /out/dispatch --emptyOutDir
# Dealer Admin, staff, roles, pricing rules and the audit log: a staff app,
# so the demo mode compiles the front door's handoff in, under its own prefix.
WORKDIR /src/web/apps/dealer-admin
RUN npx vite build --mode demo --base=/dealer-admin/ --outDir /out/dealer-admin --emptyOutDir
# The three Pro contractor apps (Pro Procurement, Pro Pay, Pro Field): tenant apps, each under its own prefix.
# The demo mode picks the demo sign in adapter (the contractor persona by role label, from the core's demo
# routes) in place of the real exchange, which refuses until the auth lead's go; a production build never
# carries it. A tenant app keeps its bearer in memory, so the front door's handoff is not compiled in: the
# front door's contractor card opens the app with the role (?as=) and the app signs the persona in itself.
WORKDIR /src/web/apps/pro-procurement
RUN npx vite build --mode demo --base=/pro-procurement/ --outDir /out/pro-procurement --emptyOutDir
WORKDIR /src/web/apps/pro-pay
RUN npx vite build --mode demo --base=/pro-pay/ --outDir /out/pro-pay --emptyOutDir
WORKDIR /src/web/apps/pro-field
RUN npx vite build --mode demo --base=/pro-field/ --outDir /out/pro-field --emptyOutDir
WORKDIR /src/web
RUN if [ -d apps/quote-desk ]; then \
		cd apps/quote-desk && npx vite build --mode demo --base=/quote/ --outDir /out/quote --emptyOutDir; \
	fi

FROM caddy:2.10.2 AS caddy

FROM alpine:3.20
# ca-certificates: outbound TLS the core's standard library expects.
# postgresql16-client: the entrypoint's one database probe, which reads the
# database's shape before anything runs (the interrupted-build answer, in
# the entrypoint). Nothing else in the image talks to the database except
# the core itself.
RUN apk add --no-cache ca-certificates postgresql16-client
COPY --from=core-build /out/core /usr/local/bin/core
COPY --from=caddy /usr/bin/caddy /usr/local/bin/caddy
COPY --from=web-build /out/root /srv/app
COPY --from=web-build /out/till /srv/app/till
COPY --from=web-build /out/buying-and-stock /srv/app/buying-and-stock
COPY --from=web-build /out/q /srv/app/q
COPY --from=web-build /out/quote /srv/app/quote
COPY --from=web-build /out/drive /srv/app/drive
COPY --from=web-build /out/home /srv/app/home
COPY --from=web-build /out/yard /srv/app/yard
COPY --from=web-build /out/money-desk /srv/app/money-desk
COPY --from=web-build /out/dispatch /srv/app/dispatch
COPY --from=web-build /out/dealer-admin /srv/app/dealer-admin
COPY --from=web-build /out/pro-procurement /srv/app/pro-procurement
COPY --from=web-build /out/pro-pay /srv/app/pro-pay
COPY --from=web-build /out/pro-field /srv/app/pro-field
COPY core/db/migrations /srv/core/db/migrations
COPY deploy/demo/Caddyfile.template /srv/templates/Caddyfile.template
COPY deploy/demo/entrypoint.sh /usr/local/bin/entrypoint
COPY deploy/demo/write-web-config.sh /usr/local/bin/write-web-config.sh
RUN chmod 0755 /usr/local/bin/entrypoint

# The migrate role reads db/migrations relative to the working directory
# (core/cmd/core/roles_oneshot.go), so the workdir is where the migration
# set was copied above.
WORKDIR /srv/core

# The platform reaches the image on this port: Caddy listens here and
# proxies to the core on a loopback port the entrypoint chooses, so the
# core is never reachable except through Caddy.
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=90s --retries=12 \
  CMD wget -q -O /dev/null "http://127.0.0.1:${PORT:-8080}/healthz" || exit 1

ENTRYPOINT ["/usr/local/bin/entrypoint"]
