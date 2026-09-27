# syntax=docker/dockerfile:1

# Build stage: compile Caddy with the local plugin source baked in.
# The builder image (pinned to CADDY_VERSION) already ships xcaddy, so no
# separate install is needed. The plugin source is COPYed at build time only
# (not bind-mounted at runtime), so the image is self-contained.
ARG CADDY_VERSION=2.11.4

FROM caddy:${CADDY_VERSION}-builder-alpine AS build

WORKDIR /src

# Copy the Go module files and the plugin package (at the module root).
COPY go.mod go.sum ./
COPY *.go ./

# Build a custom Caddy binary:
#   - the version comes from the builder image's own `ENV CADDY_VERSION`, which
#     already carries the leading `v` (e.g. v2.11.4). Docker resolves an ENV
#     over a build ARG of the same name, so do not add another `v` here;
#   - --with "github.com/gmag11/caddy-cloudflare-dns-manager=." builds the
#     plugin from the COPYed module root instead of the module proxy;
#   - caddy-dns/cloudflare provides the `tls { dns cloudflare ... }` provider.
#
# The cache mounts keep the Go module and build caches out of the image layer
# and in the BuildKit builder cache, so editing the plugin source does not
# re-download the module graph and recompile every dependency from scratch
# (~3 min cold vs. seconds warm). They require BuildKit: with the classic
# builder the `--mount` flags are ignored and every change is a full rebuild.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    xcaddy build "${CADDY_VERSION}" \
    --with "github.com/gmag11/caddy-cloudflare-dns-manager=." \
    --with github.com/caddy-dns/cloudflare@v0.2.4 \
    --output /out/caddy

# Runtime stage: minimal image with the custom Caddy binary.
FROM caddy:${CADDY_VERSION}

COPY --from=build /out/caddy /usr/bin/caddy

# Sanity check: both plugins must be registered in this binary.
RUN caddy list-modules | grep -q '^cf_dns_manager$' \
    && caddy list-modules | grep -q '^dns.providers.cloudflare$'
