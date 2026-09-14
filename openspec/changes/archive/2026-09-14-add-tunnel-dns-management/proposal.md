# Add Cloudflare Tunnel DNS management

## Why

Cloudflare Tunnel is a common way to expose homelab services without exposing the origin IP: traffic reaches Cloudflare's edge via a CNAME to `<tunnel-id>.cfargotunnel.com`, and `cloudflared` connects outbound. Today the plugin can only manage A/AAAA records pointing at a public IP, so tunnel-backed hosts must have their CNAME created and maintained by hand (or by `cloudflared tunnel route dns`, which creates untagged records the plugin will not touch). The user also wants tunnel usage to be identifiable per subdomain, not as a global mode — which matches the plugin's existing per-host declaration model.

## What Changes

- Add a `tunnel <uuid>` subdirective to the per-site `cf_dns_manager` directive declaring that the host's DNS endpoint is a Cloudflare Tunnel identified by its UUID.
- For tunnel hosts, the plugin reconciles a single **CNAME** record with content `<tunnel-id>.cfargotunnel.com`, always proxied, instead of A/AAAA records.
- `tunnel` is mutually exclusive with `ip`, `ip6`, and `proxied`; declared at adapt time with a clear error.
- `force_adopt` works with tunnel hosts, since `cloudflared tunnel route dns` creates the CNAME without the ownership tag.
- Tunnel hosts never trigger public-IP detection.
- Migration from a normal host to a tunnel host works: the previously owned A record becomes orphaned and is pruned in prune-enabled zones (or warned about otherwise).
- The test environment (`testenv/`) gains a `cloudflared` container (locally-managed tunnel, config versioned in the repo) so the feature can be validated end-to-end.

## Capabilities

### New Capabilities

- `tunnel-dns-management`: Cloudflare Tunnel host declarations, CNAME reconciliation for tunnel hosts, validation rules (mutual exclusion with ip/ip6/proxied, UUID format), adoption of untagged tunnel CNAMEs via `force_adopt`, and tunnel-host interaction with public-IP detection and prune.

### Modified Capabilities

- `caddyfile-config`: The per-site directive gains the `tunnel` subdirective and its validation rules (mutual exclusion with `ip`/`ip6`/`proxied`; UUID format check). Existing requirements are unchanged.
- `dns-record-reconciliation`: Reconciliation branches per host family: tunnel hosts reconcile one CNAME instead of A/AAAA, always proxied. Existing A/AAAA requirements are unchanged.
- `record-ownership`: The ownership tag and `force_adopt` semantics extend to CNAME records created for tunnel hosts. Existing requirements are unchanged (they are per-record-type by construction).

## Impact

- **Code**: `app.go` (HostConfig gains `TunnelID`), `caddyfile.go` (`tunnel` subdirective parsing + validation), `reconcile.go` (per-host branch: CNAME plan for tunnel hosts, `managed` map marks `CNAME`, no IP detection for tunnel-only configs).
- **Tests**: unit tests for parsing/validation; mock-Cloudflare tests for CNAME create/update/adopt/prune; a migration test (A → CNAME with prune).
- **Test environment**: `testenv/docker-compose.yml` (new `cloudflared` service), `testenv/tunnel/config.yml` (versioned ingress config; credentials gitignored), `testenv/Caddyfile` (example tunnel host block).
- **No changes** to global options block, zone registration, ownership tag format, prune semantics, or IP detection endpoints.
- **User workflow**: creating the tunnel itself (and obtaining its UUID/credentials) remains a one-time manual step outside the plugin, as does running `cloudflared`.
