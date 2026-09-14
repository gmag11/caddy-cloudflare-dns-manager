# Publishing addresses: direct IP, Tailscale, IPv6, or Tunnel?

The plugin can publish the same hostname in several ways. This guide compares
them and shows the common dual-family setups.

## The four ways in

| Way | Directive | Visitor path | Origin exposure |
| --- | --- | --- | --- |
| Detected public IPv4 | *(default)* | CF edge → your IP (proxied) | ports open, IP hidden by proxy |
| Explicit IPv4 | `ip <a.b.c.d>` | same | same |
| Cloudflare Tunnel | `tunnel <uuid>` | CF edge → cloudflared (outbound) | **no open ports, no public IP** |
| Tailscale (direct) | `ip 100.x.y.z` / `ip6 <ULA>` | **bypasses Cloudflare** — DNS-only, LAN/VPN only | none |

Key distinction: `proxied yes/no` decides whether visitors go *through
Cloudflare*. Private/CGNAT/ULA addresses (Tailscale's `100.64.0.0/10`,
`fd7a:115c:a1e0::/48`) **cannot be proxied**, so the plugin always forces them
DNS-only regardless of `proxied`.

## Dual-family: public IPv4 + Tailscale IPv6

The classic homelab setup: normal visitors take the proxied public path;
Tailscale clients resolve the same name to the ULA and connect over the VPN.

```
app.example.com {
	cf_dns_manager {
		host app.example.com
		ip 203.0.113.7            # proxied A (public)
		ip6 fd7a:115c:a1e0::1     # DNS-only AAAA (Tailscale ULA)
	}
	reverse_proxy localhost:8080
}
```

Result in DNS:

| Record | Content | Proxied |
| --- | --- | --- |
| A | 203.0.113.7 | yes |
| AAAA | fd7a:115c:a1e0::1 | **no** (forced: ULA) |

When the two families end up with different proxied states the plugin logs a
warning — expected in this setup, not an error.

`ip6 auto` instead of a literal publishes the **detected public** IPv6 (for
hosts with real v6 connectivity); it will be proxied like the A.

## Tunnel vs Tailscale

| | Tunnel | Tailscale direct |
| --- | --- | --- |
| Audience | public internet | VPN members only |
| Latency | via CF edge (all regions) | direct WireGuard |
| Origin exposure | none (outbound only) | none |
| Cost | free tier ok | free tier ok |
| DNS record | proxied CNAME (plugin-managed) | DNS-only A/AAAA (plugin-managed) |
| Ingress config | cloudflared (per hostname) | tailscale ACLs |
| Auth | add Cloudflare Access on top | Tailscale ACLs |

Rule of thumb: **public service → tunnel; internal-only → Tailscale.** Both can
coexist on the same Caddy: tunnel for `app.`, Tailscale for `admin.`:

```
@admin host admin.example.com
handle @admin {
	cf_dns_manager {
		host admin.example.com
		ip 100.64.10.5      # Tailscale IPv4 (CGNAT → DNS-only)
	}
	reverse_proxy localhost:9090
}
```

## IPv6-only hosts

A host with public IPv6 but no IPv4:

```
cf_dns_manager {
	host app.example.com
	ip6 auto     # AAAA with detected public IPv6, proxied
}
```

The plugin manages families independently: an IPv6-only host just never gets an
A record. Note that proxied AAAA without an A still covers dual-stack visitors
entirely through Cloudflare.

## Detection details worth knowing

- One detection per family per reload, shared by all auto-IP hosts, in parallel
  (v4: `ip_url`, 15s timeout; v6: `ip6_url`, 5s timeout).
- The default v6 endpoint is IPv6-only **on purpose**: a v4-shaped answer means
  the network or endpoint is broken, and the plugin rejects it instead of
  publishing a wrong record.
- A failed detection never blocks startup: existing records are left untouched,
  new ones skipped, and the next reload retries. Explicit `ip`/`ip6` literals
  are immune to detection failures.
- The plugin classifies private/reserved ranges (RFC1918, CGNAT, ULA,
  link-local, loopback) and forces them DNS-only; Cloudflare cannot proxy them.

## Public IP behind NAT you don't control

If the detected IP is a shared NAT address (carrier-grade or VPS provider),
pin the exact address the record should have (`ip <addr>`), or prefer a tunnel —
with a tunnel the origin IP is irrelevant.
