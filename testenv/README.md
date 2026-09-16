# Test environment

Local Docker harness for manually exercising the plugin against the real
Cloudflare API. Not part of the published module.

## Layout

```
testenv/
├── Caddyfile            # Caddy config under test (normal + tunnel hosts)
├── create-tunnel.sh     # provisions the harness tunnel, fills .env
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
tunnel <name> (per host) →     └─ one rule per declared hostname
```

Because the default route is the tunnel's mandatory catch-all, the zone needs
no `*.<zone>` DNS record — which is the point: undeclared subdomains do not
resolve at all.

## Prerequisites

1. Copy the env template and fill it in:

   ```bash
   cp testenv/.env.example testenv/.env
   # edit: CF_API_TOKEN, CF_ACCOUNT_TUNNEL_TOKEN, CF_ZONE, CF_INSTANCE
   # then run testenv/create-tunnel.sh to fill CF_TUNNEL_TOKEN / CF_TUNNEL_ID
   ```

   `CF_API_TOKEN` is **zone-scoped** (`Zone → DNS → Edit`) and drives the DNS
   records. `CF_ACCOUNT_TUNNEL_TOKEN` is **account-scoped**
   (`Account → Cloudflare Tunnel → Edit`) and drives the ingress plan; it can
   reconfigure every tunnel in the account, which is why it is a separate,
   optional credential. The account id is not configured: the plugin derives it
   from the zone lookup.

2. Create the tunnel (one-time):

   ```bash
   testenv/create-tunnel.sh
   ```

   The script creates a remotely-managed tunnel named after `CF_INSTANCE` and
   writes both `CF_TUNNEL_TOKEN` and `CF_TUNNEL_ID` back into `.env`. It runs
   only when those two values are unconfigured, so it is safe to call
   unconditionally: with a provisioned harness it reports so and exits 0, and
   with exactly one of the two unconfigured it refuses and creates nothing,
   because the set half already belongs to some tunnel.

   **Blank the two lines before the first run.** The template ships
   `CF_TUNNEL_TOKEN=replace-me` and an all-zero `CF_TUNNEL_ID`, and those
   placeholders count as configured — the script only acts on an unambiguous
   empty value, never on a guess about what looks like a placeholder. With the
   placeholders still in place it prints the two lines to blank and exits 0
   without creating anything:

   ```dotenv
   CF_TUNNEL_TOKEN=
   CF_TUNNEL_ID=
   ```

   It derives the account id from `CF_ZONE` with the zone-scoped `CF_API_TOKEN`
   (the same derivation the plugin uses), and does the creating with the
   account-scoped `CF_ACCOUNT_TUNNEL_TOKEN`. The run token never reaches
   stdout, so nothing sensitive lands in the terminal scrollback.

   *Alternatively, by hand:* **Networking → Tunnels → Create a tunnel →
   Cloudflared**, then copy the shown run token into `CF_TUNNEL_TOKEN` and the
   tunnel UUID into `CF_TUNNEL_ID`.

   Either way, do **not** add a public hostname to the tunnel in the dashboard:
   the plugin writes the ingress plan, and a hand-made rule would be preserved
   as a
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
  catch-all from `tunnel_default_service`;
- sets `originRequest.matchSNItoHost` on every rule whose service is `https://`,
  which this harness needs because Caddy serves a `*.<CF_ZONE>` certificate.
  No manual step: without it the TLS handshake to the origin fails and every
  request returns 502.

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
