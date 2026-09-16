# Deploying with Docker

How to run the plugin in Docker: build (or pull) an image with the module
baked in, wire up the Caddyfile, credentials and volumes, and — if you use
Cloudflare Tunnels — add a `cloudflared` container. For the tunnel
configuration itself (tunnel creation, the `tunnel` directive), see
[cloudflare-tunnel.md](cloudflare-tunnel.md).

## 1. The image

The root `Dockerfile` builds a Caddy image with the module compiled in:

```bash
docker build -t caddy-cloudflare-dns-manager .
```

If you also need ACME DNS-01 challenges (`tls { dns cloudflare ... }`), build
with that provider too — e.g. with the [xcaddy](https://github.com/caddyserver/xcaddy)
builder image:

```dockerfile
FROM caddy:2-builder-alpine AS builder
RUN xcaddy build \
    --with github.com/gmag11/caddy-cloudflare-dns-manager \
    --with github.com/caddy-dns/cloudflare \
    --output /usr/bin/caddy
FROM caddy:2
COPY --from=builder /usr/bin/caddy /usr/bin/caddy
```

Verify the module is present in the image:

```bash
docker run --rm caddy-cloudflare-dns-manager caddy list-modules | grep cf_dns_manager
```

### Alternative: official image + runtime packages

Instead of building a custom image, use the official `caddy` image and let the
container add the packages at startup with
[`caddy add-package`](https://caddyserver.com/docs/command-line#caddy-add-package):

```sh
#!/bin/sh
# start-caddy.sh
set -e

# The ACME DNS-01 provider (tls { dns cloudflare ... }).
if ! caddy list-modules | grep -q 'dns.providers.cloudflare'; then
  echo "Installing caddy-dns/cloudflare..."
  caddy add-package github.com/caddy-dns/cloudflare
fi

# This plugin. Note the app module id is `cf_dns_manager`.
if ! caddy list-modules | grep -q '^cf_dns_manager$'; then
  echo "Installing caddy-cloudflare-dns-manager..."
  caddy add-package github.com/gmag11/caddy-cloudflare-dns-manager
fi

exec caddy run --config /etc/caddy/Caddyfile --adapter caddyfile
```

```yaml
services:
  caddy:
    image: caddy:2
    restart: unless-stopped
    env_file: .env
    ports:
      - "80:80"
      - "443:443"
      - "443:443/udp"
    entrypoint: ["/bin/sh", "/usr/local/bin/start-caddy.sh"]
    volumes:
      - ./start-caddy.sh:/usr/local/bin/start-caddy.sh:ro
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy_data:/data
      - caddy_config:/config

volumes:
  caddy_data:
  caddy_config:
```

Notes and trade-offs:

- **Both packages are needed**: `caddy-dns/cloudflare` for the ACME provider
  and this module for DNS management. Each `add-package` call accumulates into
  the running build.
- The downloaded custom build is stored under `/data` (the `caddy_data`
  volume), so the checks pass instantly on later starts. Without that volume
  every start re-downloads.
- The **first start requires outbound access** to `caddyserver.com`; on
  air-gapped hosts use the baked image from section 1 instead.
- Versions are **not pinned** — `add-package` pulls the latest from the module
  proxy. If you need reproducible builds, prefer the baked image.
- `exec` replaces the shell so Caddy receives `docker stop`'s signal directly.

## 2. Files and secrets

```
.
├── Caddyfile            # your config (mounted read-only)
├── .env                 # CF_API_TOKEN etc. (never committed)
└── (no tunnel/ dir)     # the tunnel is remotely-managed: ingress lives on
                         # Cloudflare, written by the plugin or the dashboard
```

`.env` (referenced with `{$VAR}` in the Caddyfile):

```dotenv
CF_API_TOKEN=your-zone-dns-edit-token
CF_INSTANCE=server-1          # stable id, see "Instance id" below
CF_TUNNEL_ID=8a7f3c2e-1234-4567-89ab-cdef01234567   # only for tunnels
```

## 3. Minimal compose (address records only)

```yaml
services:
  caddy:
    image: caddy-cloudflare-dns-manager
    # or: build: .
    container_name: caddy
    restart: unless-stopped
    env_file: .env
    ports:
      - "80:80"
      - "443:443"
      - "443:443/udp"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy_data:/data       # certificates and keys
      - caddy_config:/config   # autosaved config

volumes:
  caddy_data:
  caddy_config:
```

With this Caddyfile the plugin reconciles on every (re)start and reload:

```
{
	cf_dns_manager {
		zone example.com api_token {$CF_API_TOKEN} prune
		instance {$CF_INSTANCE}
	}
}

*.example.com {
	@foo host foo.example.com
	handle @foo {
		cf_dns_manager {
			host @foo
		}
		reverse_proxy localhost:8080
	}
}
```

## 4. Adding a Cloudflare Tunnel

Same stack plus a `cloudflared` container. It connects **outbound** to
Cloudflare's edge — no published ports — and forwards matching requests to
Caddy over the compose network:

```yaml
services:
  caddy:
    image: caddy-cloudflare-dns-manager
    container_name: caddy
    restart: unless-stopped
    env_file: .env
    ports:
      - "80:80"
      - "443:443"
      - "443:443/udp"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy_data:/data
      - caddy_config:/config

  cloudflared:
    image: cloudflare/cloudflared:latest
    container_name: cloudflared
    restart: unless-stopped
    # Remotely-managed tunnel (recommended): just the token. Its ingress rules
    # live on Cloudflare — written by the plugin when the Caddyfile declares
    # `account`, or by you in the dashboard.
    command: tunnel --no-autoupdate run --token ${CF_TUNNEL_TOKEN}
    env_file: .env
    depends_on:
      - caddy

volumes:
  caddy_data:
  caddy_config:
```

No `tunnel/config.yml` and no credentials file are needed: a remotely-managed
connector takes its identity from the token and ignores `--config`.

Use the **service name** (`caddy`) as the origin host, not `localhost`: inside
the compose network `localhost` is the cloudflared container itself.

#### Letting the plugin write the ingress rules

Declare the account credential and a default service, and the plugin maintains
the tunnel's ingress plan alongside the DNS records:

```
{
	cf_dns_manager {
		zone example.com api_token {$CF_EXAMPLE}
		account {$CF_ACCOUNT_ID} api_token {$CF_TUNNEL_TOKEN}
		tunnel_default_service https://caddy:443
	}
}
```

The default becomes the tunnel's catch-all, so every declared subdomain reaches
Caddy and **no wildcard DNS record is needed**. `matchSNItoHost` is not
available through the plugin (it manages `hostname` and `service` only); if your
origin needs a specific SNI behaviour, keep that rule hand-made and the plugin
will preserve it as a foreign rule.

> The account token can reconfigure **every** tunnel in the account. Omit the
> `account` line to keep the plugin DNS-only — then maintain the ingress rules
> yourself, in the dashboard or via the API.

#### Hand-managed ingress (no `account` line)

If you prefer to own the ingress rules, add the published hostnames in the
dashboard. Note that the dashboard's **Published application** flow also creates
the DNS record without the plugin's ownership tag, which the plugin will then
neither update nor prune. Two ways to avoid that:

- create the DNS record with the plugin (declare the host with `tunnel <uuid>`)
  and add only the ingress rule in the dashboard; or
- declare the host with `tunnel <uuid>` **and** give the plugin the `account`
  credential, so it writes both halves itself.

Full reference:
[cloudflare-tunnel.md](cloudflare-tunnel.md#the-tunnel-ingress-configuration-cloudflared-side).

The Caddyfile side is unchanged — the `tunnel` directive (see
[cloudflare-tunnel.md](cloudflare-tunnel.md)) makes the plugin create the
CNAME; `cloudflared` handles the transport:

```
@app host app.example.com
handle @app {
	cf_dns_manager {
		host @app
		tunnel {$CF_TUNNEL_ID}
		force_adopt
	}
	reverse_proxy localhost:8080
}
```

Start and verify:

```bash
docker compose up -d
docker compose logs -f caddy cloudflared
curl -sS https://app.example.com/
```

`cloudflared` reports `Registered tunnel connection` when it is live.

## 5. Reloading configuration

The Caddyfile is bind-mounted, so edit it on the host and reload inside the
container (the admin endpoint must be enabled — it is by default):

```bash
docker compose exec caddy caddy reload --config /etc/caddy/Caddyfile
```

The plugin reconciles on every reload, so DNS follows the config immediately.
To restart the stack:

```bash
docker compose restart caddy
```

> **Note:** editing a mounted file does not restart a running container. That
> does not matter for a remotely-managed tunnel: its ingress lives on Cloudflare
> and `cloudflared` syncs it, so a Caddy reload is enough. Only if you run a
> locally-managed tunnel (with a mounted `config.yml`) do you need to recreate
> the connector so it reloads its file:
>
> ```bash
> docker compose up -d --force-recreate cloudflared
> ```

## 6. Instance id (important in Docker)

By default the ownership tag is `<tag_prefix>:<hostname>`, and inside a
container the hostname is the container ID — which changes on every recreate.
After an update you would end up with two "instances": old records tagged with
the dead container's id (orphans, pruned if enabled) and fresh records. Set a
stable `instance` in the global block (as in the examples above) so tags survive
container replacement:

```
cf_dns_manager {
	zone example.com api_token {$CF_API_TOKEN} prune
	instance {$CF_INSTANCE}
}
```

## 7. Checklist

- [ ] Image contains the module (`caddy list-modules | grep cf_dns_manager`)
- [ ] `.env` has `CF_API_TOKEN` (Zone, DNS:Edit) and a stable `CF_INSTANCE`
- [ ] `Caddyfile` mounts read-only; `/data` and `/config` are volumes
- [ ] `cloudflared` publishes no ports and uses the service name as origin
- [ ] Tunnel config edited? `--force-recreate cloudflared`
