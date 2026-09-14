# caddy-cloudflare-dns-manager

A Caddy plugin that keeps Cloudflare DNS A (and optionally AAAA) records in
sync with hosts declared in the Caddyfile. For every declared public subdomain
it ensures the Cloudflare record exists and points at the intended address —
the server's detected public IP by default, or an explicit override (for
example a Tailscale address).

It is Cloudflare-only and reconciles on every config load/reload. IPv4 (A
records) is always managed; IPv6 (AAAA) is opt-in per host. It never sweeps a
zone: only hosts that explicitly opt in with a directive are managed, and
records it did not create are left alone by default.

## Build / install

The plugin is a Caddy module. Build a Caddy binary with it using
[xcaddy](https://github.com/caddyserver/xcaddy):

```
xcaddy build --with github.com/gmag11/caddy-cloudflare-dns-manager
```

A prebuilt image that bakes the module into Caddy is available via the root
`Dockerfile`:

```
docker build -t caddy-cloudflare-dns-manager .
```

To also include the [caddy-dns/cloudflare](https://github.com/caddy-dns/cloudflare)
provider for ACME DNS-01 challenges:

```
xcaddy build \
    --with github.com/gmag11/caddy-cloudflare-dns-manager \
    --with github.com/caddy-dns/cloudflare
```

## Repository layout

The Go package lives at the repository root so the module path resolves
directly to it — no subdirectory is needed to reference or import the plugin.

```
.
├── .github/workflows/   CI: build + test, and image build/push on tags
├── Dockerfile           Caddy image with the module baked in
├── docs/                deployment guides (Docker, Cloudflare Tunnels)
├── LICENSE              Apache-2.0
├── go.mod / go.sum      module github.com/gmag11/caddy-cloudflare-dns-manager
├── *.go                 the plugin package (Caddy app, directives, reconcile)
└── testenv/             local Docker harness for manual testing (not published)
```

Guides:

- [Deploying with Docker](docs/docker-deployment.md) — image, compose, volumes,
  `cloudflared` sidecar, reload workflow.
- [Configuring a Cloudflare Tunnel connection](docs/cloudflare-tunnel.md) —
  step-by-step tunnel setup with the `tunnel` directive.

## Caddyfile

### 1. Global options: declare zones and their tokens

Each managed Cloudflare zone must be declared with its API token before any
per-site directive can reference a host under it. Global settings live here too.

```
{
	cf_dns_manager {
		# zone <zone> api_token <token> [prune]
		zone example.com     api_token {$CF_EXAMPLE}
		zone otherdomain.com api_token {$CF_OTHER} prune

		# ip_url overrides the public-IPv4 detection endpoint (optional;
		# default: https://cloudflare.com/cdn-cgi/trace)
		# ip_url https://ifconfig.me/ip

		# ip6_url overrides the public-IPv6 detection endpoint (optional;
		# default: https://api6.ipify.org, an IPv6-only endpoint)
		# ip6_url https://v6.ident.me

		# tag_prefix customizes the ownership-comment prefix (optional)
		# tag_prefix caddy-cf-dns

		# instance identifies this server for ownership tagging (optional;
		# default: the hostname). Use a stable id when multiple Caddy servers
		# share a zone so each only prunes its own records.
		# instance {$CADDY_INSTANCE_ID}
	}
}
```

- `prune` on a zone opts in to deleting this instance's orphaned A/AAAA records
  (records carrying this server's tag whose host is no longer declared, or whose
  IPv6 was disabled). It only ever deletes records tagged with this instance;
  records without the tag or tagged by another instance are never touched.
- A host under a zone that is not declared is a configuration error.

### 2. Per-site / per-handle directive: opt a host in

Inside a site block or a `handle` block (e.g. under a `*.example.com`
wildcard), `cf_dns_manager` declares a single host to reconcile:

```
*.example.com {
	tls {
		dns cloudflare {$CF_EXAMPLE}   # wildcard cert via ACME (unchanged)
	}

	@foo host foo.example.com
	handle @foo {
		cf_dns_manager {
			host @foo            # resolves to foo.example.com
		}
		reverse_proxy 127.0.0.1:8080
	}

	@tail host tail.example.com
	handle @tail {
		cf_dns_manager {
			host @tail
			ip 100.64.10.5      # explicit override (e.g. Tailscale)
			ip6 auto            # manage AAAA with the detected public IPv6
			proxied no          # DNS-only (default is proxied)
			force_adopt         # adopt an existing untagged record
		}
		reverse_proxy 127.0.0.1:8081
	}

	@private host private.example.com
	handle @private {
		# no cf_dns_manager -> never announced in Cloudflare
		reverse_proxy 127.0.0.1:8082
	}
}

example.com {                    # apex domain, own site block
	cf_dns_manager {
		host example.com         # literal FQDN also accepted
	}
	redir https://www.example.com
}
```

Subdirectives:

| Subdirective | Description |
| --- | --- |
| `host <@ref\|fqdn>` | **Required.** The host to reconcile, as a named matcher (`@foo`, which must map to exactly one host) or a literal FQDN. One directive = one host; repeat the directive for more hosts. |
| `ip <ipv4>` | Optional IPv4 override. When absent, the server's detected public IPv4 is used. |
| `ip6 false\|auto\|<ipv6>` | IPv6 mode, per host. `false` (default) manages no AAAA. `auto` manages an AAAA with the detected public IPv6. A literal IPv6 (e.g. a Tailscale ULA `fd7a:115c:a1e0::1`) manages an AAAA with that address. |
| `proxied yes\|no` | Cloudflare proxied (orange cloud) or DNS-only. Default `yes`. Applies to both A and AAAA. A private/reserved IP always forces DNS-only for that family regardless. |
| `force_adopt` | Update and claim an existing record that lacks the plugin's tag (otherwise untagged records are left untouched). Applies per family. |

## Policy

Every A/AAAA record the plugin creates or adopts carries an ownership comment of
the form `<tag_prefix>:<instance>`. That tag is the single source of truth for
"does this record belong to me?". Matching is per record family: an untagged
AAAA is never adopted just because the plugin owns the A of the same name.

```
existing A/AAAA record for the host in Cloudflare?
   |
   +-- none          -> create (never destructive)
   |
   +-- has my tag    -> update IP/proxy mode on drift (idempotent if equal)
   |
   +-- untagged      -> force_adopt present?  yes -> update + claim
                      |                       no  -> leave unchanged (log)
   +-- other instance tag -> always left unchanged (even with force_adopt)
```

Prune (per zone, opt-in) deletes only A/AAAA records carrying this instance's
tag whose host is no longer declared by the current config (or whose IPv6 was
disabled with `ip6 false`). Eligibility comes solely from the declared
configuration: a record skipped because public-IP detection failed is never
pruned. Untagged records and records of other instances are never candidates, so
multiple Caddy servers can share a zone safely.

The plugin never touches TXT records, so ACME DNS-01 challenges (`tls { dns
cloudflare ... }`) are unaffected.

## Public IP detection

Auto-IP hosts share a single detection per config load per family: IPv4 against
`ip_url` (default Cloudflare's `https://cloudflare.com/cdn-cgi/trace`) and, when
any host uses `ip6 auto`, IPv6 against `ip6_url` (default the IPv6-only
`https://api6.ipify.org`) in parallel with a shorter timeout. If detection
fails for a family, Caddy still starts: the plugin logs a warning, leaves
existing auto-IP records of that family unchanged, and skips creating new ones
until a later reload succeeds. Hosts with an explicit `ip`/`ip6` are unaffected.

### IPv6 and Tailscale

A host with a public IPv4 and a Tailscale IPv6 ULA can publish both: the A
record is proxied and the AAAA (ULA) is forced DNS-only, since Cloudflare cannot
proxy to a private address. When the two families end up with different proxied
states the plugin logs a warning and writes each record with its own forced
mode.

```
cf_dns_manager {
    host app.example.com
    ip 203.0.113.7          # public IPv4 -> proxied A
    ip6 fd7a:115c:a1e0::1   # Tailscale ULA -> DNS-only AAAA
}
```

