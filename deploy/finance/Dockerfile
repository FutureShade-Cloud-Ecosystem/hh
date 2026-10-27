# The finance box image (the deploy spec FINANCE-BOX-SPEC-r1, section 1): one
# image in the FB Deploy shape. Build from the repository root:
#
#   docker build -f deploy/finance/Dockerfile -t hh-finance-box .
#
# The demo image (deploy/demo/Dockerfile) is the pattern this file follows;
# the differences are the point. The core here is the DEFAULT build: no demo
# tag, so the binary carries serve, worker, migrate, finance-seed and
# finance-box-check, and none of the demo commands or the demo credential
# bypass. The web build is the console sign in, never the demo mode: the
# front door, the Counter Till, the Money Desk and Manager Home, each under
# its own prefix, with the runtime config (mode console) written at boot from
# HH_WEB_* names by the entrypoint. Caddy fronts everything; there is no
# basic auth gate, the box's own FB Console sign in is the door. The
# entrypoint (deploy/finance/entrypoint.sh) is the only place the boot order
# lives: the variable checks, the one database name held in both spellings,
# migrate then finance-seed on an empty database, the box kind check then
# migrate on a non empty one, then core serve, core worker and Caddy under
# the supervisor.

FROM golang:1.27-alpine@sha256:f2c872bcee949b7be9c490efd02b48a8a2fbf947cdf533f1c58525ec9d8170fd AS core-build
WORKDIR /src/core
COPY core/go.mod core/go.sum ./
RUN go mod download
COPY core/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/core ./cmd/core

FROM node:22-alpine AS web-build
# The whole workspace is copied before the install: npm resolves the
# workspaces from the packages' directories on disk, so a manifest-only
# copy installs nothing but the root. The drive app's offline data layer
# imports the sync statechart beside the web workspace, so that folder
# comes too, exactly as in the demo build.
COPY statecharts/ /src/statecharts/
WORKDIR /src/web
COPY web/ ./
RUN npm ci
# Every app lands in /out under the name of its prefix. The console mode is
# named at runtime through /config.json (the entrypoint writes it from
# HH_WEB_* names); the builds are the console sign in, never the demo mode,
# so no demo mint or adopt path is compiled in.
RUN mkdir -p /out/till /out/money-desk /out/home
WORKDIR /src/web/apps/front-door
RUN npx vite build --outDir /out/root
WORKDIR /src/web/apps/counter-till
RUN npx vite build --mode console --base=/till/ --outDir /out/till --emptyOutDir
WORKDIR /src/web/apps/money-desk
RUN npx vite build --mode console --base=/money-desk/ --outDir /out/money-desk --emptyOutDir
WORKDIR /src/web/apps/manager-home
RUN npx vite build --mode console --base=/home/ --outDir /out/home --emptyOutDir

FROM caddy:2.10.2 AS caddy

FROM alpine:3.20
# ca-certificates: outbound TLS the core's standard library expects.
# postgresql16-client: the entrypoint's database probes, which read the
# database's shape before anything runs. Nothing else in the image talks to
# the database except the core itself.
RUN apk add --no-cache ca-certificates postgresql16-client
COPY --from=core-build /out/core /usr/local/bin/core
COPY --from=caddy /usr/bin/caddy /usr/local/bin/caddy
COPY --from=web-build /out/root /srv/app
COPY --from=web-build /out/till /srv/app/till
COPY --from=web-build /out/money-desk /srv/app/money-desk
COPY --from=web-build /out/home /srv/app/home
COPY core/db/migrations /srv/core/db/migrations
COPY deploy/finance/Caddyfile.template /srv/templates/Caddyfile.template
COPY deploy/finance/entrypoint.sh /usr/local/bin/entrypoint
COPY deploy/finance/write-web-config.sh /usr/local/bin/write-web-config.sh
COPY deploy/finance/signin-allow-list.sh /usr/local/bin/signin-allow-list.sh
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
