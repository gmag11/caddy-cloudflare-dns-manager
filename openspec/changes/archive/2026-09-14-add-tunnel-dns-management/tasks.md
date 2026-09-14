## 1. Model and parsing

- [x] 1.1 Add `TunnelID string` to `HostConfig` in `app.go` with JSON tag `tunnel,omitempty`
- [x] 1.2 Add `tunnel` case to `parseHostBlock` in `caddyfile.go`: single argument, canonical UUID validation (8-4-4-4-12 hex), error on missing/malformed value
- [x] 1.3 Add adapt-time mutual-exclusion errors: `tunnel` + `ip`, `tunnel` + `ip6`, `tunnel` + `proxied`; verify `tunnel` + `force_adopt` is accepted

## 2. Reconciliation

- [x] 2.1 Branch `reconcileZone` per host: tunnel hosts mark `managed(key,"CNAME")` and reconcile CNAME to `<uuid>.cfargotunnel.com` with proxied=true; normal hosts unchanged
- [x] 2.2 Ensure CNAME branch never routes through `effectiveProxied` (no IP privacy classification for CNAME content)
- [x] 2.3 Implement tunnel migration in the CNAME path: delete owned A/AAAA at the name before CNAME create; with `force_adopt` also delete untagged A/AAAA; without it, log a conflict error and skip CNAME creation
- [x] 2.4 Skip public-IP detection for tunnel-only configs (tunnel hosts contribute to neither `wantV4` nor `wantV6` in `detectPublicIPs`)
- [x] 2.5 Extend `listRecords` filter in `cloudflare.go` to include CNAME alongside A/AAAA

## 3. Unit and integration tests (mock Cloudflare)

- [x] 3.1 Parsing tests: valid UUID, malformed UUID, missing arg, each exclusivity conflict, `tunnel` + `force_adopt` accepted
- [x] 3.2 CNAME create/update/adopt tests: created CNAME is proxied and tagged; drift updates content; untagged CNAME skipped without `force_adopt` and adopted with it
- [x] 3.3 Migration tests: owned A deleted then CNAME created in one run; untagged A blocks without `force_adopt`; untagged A deleted with `force_adopt`; AAAA handled like A
- [x] 3.4 Prune tests: removed tunnel host's owned CNAME pruned; pre-tunnel owned A leftover pruned; untagged CNAME never pruned
- [x] 3.5 Detection test: tunnel-only config issues no IP detection requests; mixed config still detects once per family
- [x] 3.6 Revert tests (CNAME → A): owned CNAME deleted then A created in the same run; untagged CNAME blocks without `force_adopt`; untagged CNAME deleted with `force_adopt`

## 4. Test environment

- [x] 4.1 Add `cloudflared` service to `testenv/docker-compose.yml` (locally-managed, config mounted read-only, no published ports)
- [x] 4.2 Add versioned `testenv/tunnel/config.yml` with ingress `hostname → http://caddy:80` + `http_status:404` fallback; gitignore `testenv/tunnel/credentials.json`
- [x] 4.3 Add tunnel host block to `testenv/Caddyfile` using `tunnel {$CF_TUNNEL_ID}` with `force_adopt`
- [x] 4.4 Document one-time setup in `testenv` docs/README section: `cloudflared tunnel create`, placing credentials.json, setting `CF_TUNNEL_ID`
- [x] 4.5 Manual E2E validation: plugin creates CNAME → `curl https://<tunnel-host>` returns the site response through the tunnel

## 5. Verification

- [x] 5.1 Run `go test ./...` and `go vet ./...`; fix findings
- [x] 5.2 Validate change: `openspec validate add-tunnel-dns-management`
