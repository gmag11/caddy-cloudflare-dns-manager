# Test environment

Local Docker harness for manually exercising the plugin against the real
Cloudflare API. Not part of the published module.

## Layout

```
testenv/
├── Caddyfile            # Caddy config under test (normal + tunnel hosts)
├── docker-compose.yml   # caddy + cloudflared services
├── Dockerfile           # builds Caddy with the plugin via xcaddy
└── .env                 # secrets/config (gitignored; copy .env.example)
```

There is no `tunnel/` directory: the test tunnel is **remotely-managed**, so
its ingress rules live on Cloudflare and are written by the plugin. Nothing
about the tunnel is versioned here.

## How the two halves fit together

```
Caddyfile                    Cloudflare
─────────                    ──────────
zone ... api_token ...   →   DNS records (A/AAAA/CNAME), tagged with instance
                             └─ the lookup also yields the account id
account ...              →   tunnel ingress plan (rules + catch-all)
tunnel_default_service   →     └─ the catch-all, NOT a wildcard DNS record
tunnel <uuid> (per host) →     └─ one rule per declared hostname
```

Because the default route is the tunnel's mandatory catch-all, the zone needs
no `*.<zone>` DNS record — which is the point: undeclared subdomains do not
resolve at all.

## Prerequisites

1. Copy the env template and fill it in:

   ```bash
   cp testenv/.env.example testenv/.env
   # edit: CF_API_TOKEN, CF_ACCOUNT_TUNNEL_TOKEN, CF_TUNNEL_*, CF_ZONE, CF_INSTANCE
   ```

   `CF_API_TOKEN` is **zone-scoped** (`Zone → DNS → Edit`) and drives the DNS
   records. `CF_ACCOUNT_TUNNEL_TOKEN` is **account-scoped**
   (`Account → Cloudflare Tunnel → Edit`) and drives the ingress plan; it can
   reconfigure every tunnel in the account, which is why it is a separate,
   optional credential. The account id is not configured: the plugin derives it
   from the zone lookup.

2. Create the tunnel (one-time, in the dashboard — no local credentials file
   is involved):

   1. **Networking → Tunnels → Create a tunnel → Cloudflared**, name it
      (e.g. `cfdns-test`) and save.
   2. Copy the **run token** shown into `CF_TUNNEL_TOKEN`.
   3. Copy the **tunnel UUID** (visible on the tunnel's page) into
      `CF_TUNNEL_ID`.

   Do **not** add a public hostname to the tunnel in the dashboard: the plugin
   writes the ingress plan, and a hand-made rule would be preserved as a
   foreign rule rather than replaced.

## Run

```bash
docker compose -f testenv/docker-compose.yml up -d
docker compose -f testenv/docker-compose.yml logs -f caddy cloudflared
```

`cloudflared` connects outbound only (no published ports) and takes its
identity from the token. On startup the plugin reconciles DNS and ingress:

- creates a proxied CNAME `git.<CF_ZONE> → <CF_TUNNEL_ID>.cfargotunnel.com`;
- writes the tunnel's ingress plan — one rule per declared host plus the
  catch-all from `tunnel_default_service`.

> **Ingress changes need no recreate.** The tunnel is remotely-managed:
> Cloudflare holds the configuration and `cloudflared` syncs it. Editing the
> Caddyfile and reloading Caddy is enough — unlike the old `config.yml`
> bind-mount setup, which required
> `docker compose up -d --force-recreate cloudflared`.

## Verify

```bash
# Plugin side: the CNAME exists and is proxied (Cloudflare dashboard or dig)
dig +short git.$CF_ZONE CNAME

# Ingress side: the plugin's plan is on the tunnel
docker compose -f testenv/docker-compose.yml logs caddy | grep "wrote tunnel ingress plan"

# End-to-end through the tunnel
curl -sS https://git.$CF_ZONE   # -> "git.<CF_ZONE> OK (tunnel)"

# Negative check: an undeclared subdomain must NOT resolve.
# If this returns an address, a wildcard DNS record is present and must be
# deleted — the plugin never creates one, so it came from elsewhere.
dig +short does-not-exist.$CF_ZONE   # -> empty
```

## Teardown

```bash
docker compose -f testenv/docker-compose.yml down
```

Deleting the tunnel itself is a separate, manual step in the dashboard
(**Networking → Tunnels**). Deleting it there is also what removes its ingress
configuration; the plugin does not manage the tunnel lifecycle.
