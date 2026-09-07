# caddy-cloudflare-dns-manager

A Caddy plugin that keeps Cloudflare DNS A records in sync with hosts declared
in the Caddyfile. For every declared public subdomain it ensures the Cloudflare
record exists and points at the intended IPv4 — the server's detected public IP
by default, or an explicit override (for example a Tailscale address).

It is IPv4-only (A records; no AAAA), Cloudflare-only, and reconciles on every
config load/reload. It never sweeps a zone: only hosts that explicitly opt in
with a directive are managed, and records it did not create are left alone by
default.

## Build / install

The plugin is a Caddy module. Build a Caddy binary with it using
[xcaddy](https://github.com/caddyserver/xcaddy):

```
xcaddy build --with github.com/gmag11/caddy-cloudflare-dns-manager
```

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

		# tag_prefix customizes the ownership-comment prefix (optional)
		# tag_prefix caddy-cf-dns

		# instance identifies this server for ownership tagging (optional;
		# default: the hostname). Use a stable id when multiple Caddy servers
		# share a zone so each only prunes its own records.
		# instance {$CADDY_INSTANCE_ID}
	}
}
```

- `prune` on a zone opts in to deleting this instance's orphaned A records
  (records carrying this server's tag whose host is no longer declared). It
  only ever deletes records tagged with this instance; records without the tag
  or tagged by another instance are never touched.
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
| `proxied yes\|no` | Cloudflare proxied (orange cloud) or DNS-only. Default `yes`. A private/reserved IP always forces DNS-only regardless. |
| `force_adopt` | Update and claim an existing record that lacks the plugin's tag (otherwise untagged records are left untouched). |

## Policy

Every A record the plugin creates or adopts carries an ownership comment of the
form `<tag_prefix>:<instance>`. That tag is the single source of truth for
"does this record belong to me?".

```
existing A record for the host in Cloudflare?
   |
   +-- none          -> create (never destructive)
   |
   +-- has my tag    -> update IP/proxy mode on drift (idempotent if equal)
   |
   +-- untagged      -> force_adopt present?  yes -> update + claim
                      |                       no  -> leave unchanged (log)
   +-- other instance tag -> always left unchanged (even with force_adopt)
```

Prune (per zone, opt-in) deletes only A records carrying this instance's tag
whose host is no longer declared by the current config. Untagged records and
records of other instances are never candidates, so multiple Caddy servers can
share a zone safely.

The plugin never touches TXT records, so ACME DNS-01 challenges (`tls { dns
cloudflare ... }`) are unaffected.

## Public IP detection

Auto-IP hosts share a single detection per config load against the configured
`ip_url` (default Cloudflare's `https://cloudflare.com/cdn-cgi/trace`). If
detection fails, Caddy still starts: the plugin logs a warning, leaves existing
auto-IP records unchanged, and skips creating new auto-IP records until a later
reload succeeds. Hosts with an explicit `ip` are unaffected.
