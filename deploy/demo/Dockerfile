# The demo image (Lane 0 sub item 1): multi-app shape. All web apps declared
# as frontends in the manifest are built, each with its Vite base path set
# to the prefix it serves at in the image. Caddy routes each prefix to its
# app's built files, stripping the prefix before the file server looks it up
# so the assets (which carry the base in their references) resolve correctly.
#
# The apps and their image prefixes:
#   front-door   /        (base /)
#   counter-till /till/   (base /till/)
#   public-desk  /public/ (base /public/)
#   quote-desk   /quote/  (base /quote/, only when web/apps/quote-desk exists)
#
# Build from the repository root:
#   ./deploy/demo/build-image.sh
#
# The image carries: the core built with -tags demo (the demo issuer, the
# membership source and every boot guard compiled in), all manifest frontend
# apps built with their image prefixes, Caddy as the static host in front,
# and the frozen migration set the migrate role reads from its working
# directory. The entrypoint (deploy/demo/entrypoint.sh) is the only place the
# boot order lives: demo-check first, then migrate, demoseed -apply,
# demo-populate and demoseed -apply again (the quote world needs the two
# role logins the populate plants), then serve behind Caddy.

FROM golang:1.27-alpine AS core-build
WORKDIR /src/core
COPY core/go.mod core/go.sum ./
RUN go mod download
COPY core/ ./
RUN CGO_ENABLED=0 go build -trimpath -tags demo -o /out/core ./cmd/core

FROM node:22-alpine AS web-deps
WORKDIR /src/web
COPY web/ ./
RUN npm ci

FROM node:22-alpine AS web-build-front-door
COPY --from=web-deps /src/web /src/web
WORKDIR /src/web/apps/front-door
RUN npm run build

FROM node:22-alpine AS web-build-counter-till
COPY --from=web-deps /src/web /src/web
# Build with base /till/ so the app's asset references carry /till/ and
# Caddy's strip_prefix makes them resolve in the app's own dist tree.
WORKDIR /src/web/apps/counter-till
RUN VITE_BASE=/till/ npm run build

FROM node:22-alpine AS web-build-public-desk
COPY --from=web-deps /src/web /src/web
WORKDIR /src/web/apps/public-desk
RUN VITE_BASE=/public/ npm run build

# The quote-desk stage uses BUILD_QUOTE_DESK to decide: 1 means the source
# is present and we build it with base /quote/; 0 (default) produces a
# minimal dist so the final image still assembles and Caddy skips the route.
# Set BUILD_QUOTE_DESK=1 in the build command when web/apps/quote-desk
# exists on main. The image needs no edit when the desk lands.
FROM node:22-alpine AS web-build-quote-desk
ARG BUILD_QUOTE_DESK=0
COPY --from=web-deps /src/web /src/web
# Ensure the directory always exists before the conditional check.
RUN mkdir -p /src/web/apps/quote-desk
WORKDIR /src/web/apps/quote-desk
# When BUILD_QUOTE_DESK=1 the source must be present; otherwise produce an
# empty dist so the final image still assembles.
RUN if [ "$BUILD_QUOTE_DESK" = "1" ]; then \
		if [ -d "src" ]; then \
			VITE_BASE=/quote/ npm run build; \
		else \
			echo "BUILD_QUOTE_DESK=1 but web/apps/quote-desk has no src directory"; \
			exit 1; \
		fi \
	else \
		mkdir -p dist; \
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
# Copy each app's built files to its own prefix under /srv/app.
COPY --from=web-build-front-door    /src/web/apps/front-door/dist  /srv/app/front-door
COPY --from=web-build-counter-till /src/web/apps/counter-till/dist /srv/app/counter-till
COPY --from=web-build-public-desk   /src/web/apps/public-desk/dist  /srv/app/public-desk
COPY --from=web-build-quote-desk    /src/web/apps/quote-desk/dist  /srv/app/quote-desk
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
