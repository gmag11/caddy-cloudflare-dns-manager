# syntax=docker/dockerfile:1

# Build stage: compile Caddy with the local plugin source baked in.
# The builder image (pinned to CADDY_VERSION) already ships xcaddy, so no
# separate install is needed. The plugin source is COPYed at build time only
# (not bind-mounted at runtime), so the image is self-contained.
ARG CADDY_VERSION=2.11.4

FROM caddy:${CADDY_VERSION}-builder-alpine AS build

ARG CADDY_VERSION

WORKDIR /src

# Copy the Go module files and the plugin package (at the module root).
COPY go.mod go.sum ./
COPY *.go ./

# Build a custom Caddy binary:
#   - --with "github.com/gmag11/caddy-cloudflare-dns-manager=." builds the
#     plugin from the COPYed module root instead of the module proxy;
#   - caddy-dns/cloudflare provides the `tls { dns cloudflare ... }` provider.
RUN xcaddy build "v${CADDY_VERSION}" \
    --with "github.com/gmag11/caddy-cloudflare-dns-manager=." \
    --with github.com/caddy-dns/cloudflare@v0.2.4 \
    --output /out/caddy

# Runtime stage: minimal image with the custom Caddy binary.
FROM caddy:${CADDY_VERSION}

COPY --from=build /out/caddy /usr/bin/caddy

# Sanity check: both plugins must be registered in this binary.
RUN caddy list-modules | grep -q '^cf_dns_manager$' \
    && caddy list-modules | grep -q '^dns.providers.cloudflare$'
