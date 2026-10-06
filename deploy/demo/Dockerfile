# The demo image (plan r7 section 4, item W5, extended by D1): one image in
# the Railway shape. Build from the repository root:
#
#   docker build -f deploy/demo/Dockerfile -t hh-demo-till .
#
# The image carries: the core built with -tags demo (the demo issuer, the
# membership source and every boot guard compiled in), the built files of the
# three apps the box serves, Caddy as the static host in front, and the
# frozen migration set the migrate role reads from its working directory. The
# entrypoint (deploy/demo/entrypoint.sh) is the only place the boot order
# lives: demo-check first, then migrate, demoseed -apply and
# demo-populate, a second demoseed -apply (the quote world's personas exist
# only after the populate), then serve behind Caddy.
#
# The route plan (D1, one origin behind the one phrase gate):
#
#   /            the counter till as it serves today, the landing until the
#                front door (D2) replaces it; its dist sits at /srv/app
#   /till/*      the counter till under its own prefix, dist at /srv/app/till
#   /quote/*     the Quote Desk, dist at /srv/app/quote
#   /q/*         the customer's quote page, dist at /srv/app/q; the page
#                reads its share link token from the ?t= query parameter
#   /api/*, /healthz  the core, proxied, exactly as today
#
# Each app is built once per base with Vite's --base flag, so its index.html
# and assets carry their own prefix and no app source changes for serving.
# The counter till is built twice, once per route it serves: the root build
# is byte for byte today's landing, and the /till/ build is the one the
# prefixed route serves; the root build's line is the one D2's front door
# replaces.

FROM golang:1.27-alpine AS core-build
WORKDIR /src/core
COPY core/go.mod core/go.sum ./
RUN go mod download
COPY core/ ./
RUN CGO_ENABLED=0 go build -trimpath -tags demo -o /out/core ./cmd/core

FROM node:22-alpine AS web-build
# The whole workspace is copied before the install: npm resolves the
# workspaces from the packages' directories on disk, so a manifest-only
# copy installs nothing but the root.
WORKDIR /src/web
COPY web/ ./
RUN npm ci
# The builds run from inside each app's directory so npm puts the
# workspace root's binaries on PATH (vite is hoisted there).
WORKDIR /src/web/apps/counter-till
RUN npm run build
RUN npx vite build --base=/till/ --outDir dist-till
WORKDIR /src/web/apps/quote-desk
RUN npx vite build --base=/quote/
WORKDIR /src/web/apps/public-desk
RUN npx vite build --base=/q/

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
COPY --from=web-build /src/web/apps/counter-till/dist /srv/app
COPY --from=web-build /src/web/apps/counter-till/dist-till /srv/app/till
COPY --from=web-build /src/web/apps/quote-desk/dist /srv/app/quote
COPY --from=web-build /src/web/apps/public-desk/dist /srv/app/q
COPY core/db/migrations /srv/core/db/migrations
COPY deploy/demo/Caddyfile.template /srv/templates/Caddyfile.template
COPY deploy/demo/entrypoint.sh /usr/local/bin/entrypoint
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
