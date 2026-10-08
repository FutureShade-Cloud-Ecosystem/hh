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
# workspace root's binaries on PATH (vite is hoisted there). Every app lands
# in /out under the name of its prefix, so the final stage copies one tree.
RUN mkdir -p /out/till /out/q /out/quote
WORKDIR /src/web/apps/front-door
RUN npx vite build --outDir /out/root
WORKDIR /src/web/apps/counter-till
RUN npx vite build --mode demo --base=/till/ --outDir /out/till --emptyOutDir
WORKDIR /src/web/apps/public-desk
RUN npx vite build --base=/q/ --outDir /out/q --emptyOutDir
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
COPY --from=web-build /out/q /srv/app/q
COPY --from=web-build /out/quote /srv/app/quote
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
