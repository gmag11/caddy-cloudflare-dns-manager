# Test environment

Local Docker harness for manually exercising the plugin against the real
Cloudflare API. Not part of the published module.

## Layout

```
testenv/
├── Caddyfile            # Caddy config under test (normal + tunnel hosts)
├── docker-compose.yml   # caddy + cloudflared services
├── Dockerfile           # builds Caddy with the plugin via xcaddy
├── .env                 # secrets/config (gitignored; copy .env.example)
└── tunnel/
    ├── config.yml       # versioned ingress rules for the test tunnel
    ├── cert.pem         # account cert (gitignored; one-time step)
    └── <UUID>.json      # tunnel credentials (gitignored; one-time step)
```

## Prerequisites

1. Copy the env template and fill it in:

   ```bash
   cp testenv/.env.example testenv/.env
   # edit: CF_API_TOKEN, CF_INSTANCE
   ```

2. Create the Cloudflare Tunnel (one-time, requires a browser login):

   ```bash
   chmod 777 testenv/tunnel   # container runs as uid 65532 and must write here

   docker run -it --rm \
     -v "$PWD/testenv/tunnel:/home/nonroot/.cloudflared" \
     cloudflare/cloudflared:latest tunnel login

   docker run \
     -v "$PWD/testenv/tunnel:/home/nonroot/.cloudflared" \
     cloudflare/cloudflared:latest tunnel create cfdns-test
   ```

   This writes `cert.pem` and `<UUID>.json` into `testenv/tunnel/`. Put the
   UUID (shown by `cloudflared tunnel list`, and the credentials filename)
   into `CF_TUNNEL_ID` in `testenv/.env`.

   Notes:
   - The image resolves `~` via `/etc/passwd` (user `nonroot`, home
     `/home/nonroot`), not `$HOME`; do not pass `--user` (uid 1000 has no
     passwd entry and cloudflared then cannot find its home).
   - `testenv/tunnel/config.yml` carries the ingress rules
     (`*.<CF_ZONE> -> https://caddy:443`). It points at HTTPS because
     Caddy auto-redirects HTTP to HTTPS; proxying to `:80` would loop.

## Run

```bash
docker compose -f testenv/docker-compose.yml up -d
docker compose -f testenv/docker-compose.yml logs -f caddy cloudflared
```

`cloudflared` connects outbound only (no published ports). On startup the
plugin reconciles DNS and, for the tunnel host, creates a proxied CNAME
`git.<CF_ZONE> -> <CF_TUNNEL_ID>.cfargotunnel.com`.

> **Editing `tunnel/config.yml` requires a recreate.** The file is a bind
> mount, so `docker compose up -d` alone will NOT restart cloudflared and the
> old ingress stays loaded in memory. Force it:
>
> ```bash
> docker compose -f testenv/docker-compose.yml up -d --force-recreate cloudflared
> ```

## Verify

```bash
# Plugin side: the CNAME exists and is proxied (Cloudflare dashboard or dig)
dig +short git.$CF_ZONE CNAME

# End-to-end through the tunnel
curl -sS https://git.$CF_ZONE   # -> "git.<CF_ZONE> OK (tunnel)"
```

## Teardown

```bash
docker compose -f testenv/docker-compose.yml down
```

Deleting the tunnel itself is a separate, manual step:
`cloudflared tunnel delete cfdns-test`.
