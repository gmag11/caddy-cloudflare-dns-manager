## 1. Model and Caddyfile surface

- [x] 1.1 Add `AccountID`, `TunnelAPIToken`, `TunnelDefaultService` to `App` in `app.go` with JSON tags `account_id,omitempty`, `tunnel_api_token,omitempty`, `tunnel_default_service,omitempty`; add `account_id`/`tunnel_api_token` to the app's `Validate` (both or neither)
- [x] 1.2 Add `TunnelService string` to `HostConfig` in `app.go` with JSON tag `tunnel_service,omitempty`
- [x] 1.3 Add the `account <id> api_token <token>` case to `parseGlobalOption` in `caddyfile.go`: two required args, error on missing/extra args, error if declared twice
- [x] 1.4 Add the `tunnel_default_service <service>` case to `parseGlobalOption`: single required arg, no extra args, validated by the service parser from 1.6
- [x] 1.5 Add the `tunnel_service <service>` case to `parseHostBlock`: single required arg; at end of block, error if `TunnelService != ""` and `TunnelID == ""`, naming the missing `tunnel` subdirective
- [x] 1.6 Add `parseTunnelService(val string) error` validating the value is `http://`, `https://`, `unix://`, `unix+tls://`, `tcp://`, `ssh://`, `rdp://`, `smb://` with a non-empty address, or `http_status:<3-digit code>`; used by 1.4 and 1.5
- [x] 1.7 Extend the global-options doc comment in `caddyfile.go` and the per-site directive doc comment with the new subdirectives

## 2. Tunnel configuration API client

- [x] 2.1 Add `tunnelClient` (new file `tunnel.go`) holding an account id, a token and an optional base URL test seam, mirroring the `apiBase` seam pattern in `App`
- [x] 2.2 Implement `getConfiguration(ctx, tunnelID) (cfTunnelConfig, error)` calling `GET /accounts/{id}/cfd_tunnel/{tunnelID}/configurations`, decoding `config`, `source` and `version`; treat an empty/absent `ingress` as "no rules"
- [x] 2.3 Implement `putConfiguration(ctx, tunnelID, cfg cfTunnelConfig) error` calling `PUT` with the full `config` object round-tripped (never reconstruct it), so `originRequest` and `warp-routing` survive
- [x] 2.4 Reuse the existing request helper and error-envelope decoding; assert in a test that an API error body surfaces its Cloudflare error code and message
- [x] 2.5 Add the `cfTunnelConfig` / `cfIngressRule` types: `ingress[]` with `hostname`, `service`, `path`, `originRequest` (as `json.RawMessage` so unmodelled fields round-trip), plus `origin_request` and `warp-routing` at the top level

## 3. Ingress plan derivation and merge

- [x] 3.1 Implement `deriveIngressPlan(hosts []HostConfig, defaultService string) map[string][]cfIngressRule` keyed by tunnel UUID, using each host's `TunnelService` or the configured default; include only hosts with a non-empty `TunnelID`
- [x] 3.2 Sort each tunnel's host rules by FQDN (`sort.SliceStable` over a lowercased copy) and append the default rule last; the default rule is `{service: defaultService}` with no `hostname`
- [x] 3.3 Implement `mergeIngressPlan(current []cfIngressRule, plan []cfIngressRule) ([]cfIngressRule, changed bool, shadowed []string)`: preserve foreign hostnames in their current relative order, drop any existing catch-all, append the plugin's rules then the default rule; detect a preserved wildcard that matches a declared host for D5's warning
- [x] 3.4 Implement `ingressEqual` comparing only managed fields (`hostname`, `service`) per rule and the rule count, so a server-normalised rule (`originRequest: {}`, bumped `version`) reports no drift — this is the idempotence guarantee from D4
- [x] 3.5 Implement `normalizeDefaultService(val string) string` returning `http_status:404` when unset
- [x] 3.6 Unit tests: one rule per host; deterministic order across shuffled input; two tunnels produce independent plans; per-host override wins for its host; default rule last; empty config yields just the default rule

## 4. Ingress reconciliation

- [x] 4.1 Implement `reconcileTunnelIngress(ctx, cli *tunnelClient, hosts []HostConfig, defaultService string, logger) error` grouping by tunnel UUID, calling `getConfiguration`, `mergeIngressPlan`, and writing only when `changed`
- [x] 4.2 Enforce the remotely-managed rule: when `source` reports local, log an error naming the remedy and skip the write, per D9
- [x] 4.3 Log a warning for each shadowing preserved wildcard rule reported by 3.3, naming the rule and the affected host
- [x] 4.4 Log at Info when a plan is written (tunnel id, rule count, whether the catch-all came from a configured default or the fail-closed fallback) and at Debug when a plan is already in sync
- [x] 4.5 Wire the phase into `Reconcile` in `reconcile.go`: after the per-zone `WaitGroup`, skip entirely when no account credential or no tunnel hosts are configured; append failures to the existing error aggregation so ingress failures never abort DNS reconciliation
- [x] 4.6 Log once, at Info, when tunnel hosts exist but no account credential is configured, explaining that ingress management requires the `account` block

## 5. Mock-API tests

- [x] 5.1 Extend the mock Cloudflare harness with the tunnel-configuration routes (`GET`/`PUT /accounts/{id}/cfd_tunnel/{id}/configurations`) recording requests for assertions
- [x] 5.2 Create test: no existing rules → one `PUT` carrying a rule per declared host plus the catch-all
- [x] 5.3 Update-on-drift test: a stale `service` for a declared host → one `PUT` with the corrected value
- [x] 5.4 Idempotence test: a configuration mirroring the plan but server-normalised (extra `originRequest: {}`, higher `version`) → zero `PUT`s
- [x] 5.5 Foreign-rule test: an undeclared hostname present → preserved with its service intact and positioned before the catch-all
- [x] 5.6 Catch-all test: an existing catch-all is replaced, not duplicated, and the written plan's last rule has no `hostname`
- [x] 5.7 Round-trip test: a configuration carrying top-level `originRequest` and `warp-routing` → both present unchanged in the `PUT` body
- [x] 5.8 No-delete test: a host removed from the config → no `DELETE` request is ever issued against any rule
- [x] 5.9 Locally-managed test: `source: local` → no `PUT` and an error logged naming the remedy
- [x] 5.10 Failure-isolation test: the Tunnel API returns 500 → DNS records are still reconciled and the ingress error is surfaced in the reconcile result
- [x] 5.11 Credential-absence test: tunnel hosts declared with no `account` → zero tunnel API calls, DNS still reconciled
- [x] 5.12 Shadowing test: a preserved `*.example.com` foreign rule plus a declared `git.example.com` → warning emitted naming both

## 6. Test environment migration

- [x] 6.1 Convert `testenv/docker-compose.yml`'s `cloudflared` service to the remotely-managed form (`tunnel --no-autoupdate run --token $CF_TUNNEL_TOKEN`), dropping `--config` and `--cred-file`
- [x] 6.2 Delete `testenv/tunnel/config.yml` and update `testenv/Caddyfile` comments that reference it
- [x] 6.3 Add `CF_TUNNEL_TOKEN` and `CF_ACCOUNT_ID` to `testenv/.env.example`; document that `CF_TUNNEL_ID` is still needed for the `tunnel <uuid>` directive
- [x] 6.4 Add `tunnel_default_service https://caddy:443` and the `account` block to `testenv/Caddyfile`'s global options
- [x] 6.5 Update `testenv/README.md`: the one-time step becomes "create a remotely-managed tunnel in the dashboard, copy its token", replacing the `tunnel login`/`tunnel create`/credentials instructions
- [ ] 6.6 Manual E2E: `docker compose up -d`, confirm the plugin writes the ingress plan, and `curl https://<tunnel-host>` returns the site response
- [ ] 6.7 Manual negative E2E: an undeclared subdomain of the zone returns NXDOMAIN (proving no wildcard DNS record exists)

## 7. Documentation

- [x] 7.1 Update `docs/cloudflare-tunnel.md`: recommend the remotely-managed mode as the default, document the `account` and `tunnel_default_service` options and `tunnel_service`, and explain the two-halves model (DNS + ingress)
- [x] 7.2 Add the account token permission requirement (`Cloudflare Tunnel Write`, account-scoped) and a plain statement of its blast radius to `docs/getting-started.md`
- [x] 7.3 Update `docs/docker-deployment.md`: replace the `config.yml` example with the token-based service and the plugin-managed ingress
- [x] 7.4 Add troubleshooting entries to `docs/troubleshooting.md`: locally-managed tunnel refusal, leftover ingress rule after removing a host, and the shadowing-wildcard warning
- [x] 7.5 Update `docs/architecture.md` with the ingress phase and its placement after the DNS phase
- [x] 7.6 Update `README.md` if its tunnel example still implies a `config.yml`
- [x] 7.7 Document the security trade-off explicitly: ingress management is opt-in precisely because the account token spans every tunnel in the account

## 8. Verification

- [x] 8.1 Run `go build ./...`, `go vet ./...` and the full `go test ./...`; fix findings
- [x] 8.2 Run `gofmt -l .` and fix any formatting drift
- [x] 8.3 Run `openspec validate add-tunnel-ingress-management --strict` and resolve findings
- [x] 8.4 Confirm the regression baseline: a config with no `account` block passes the pre-existing suite unchanged

## 9. Merge to main

- [ ] 9.1 Confirm every task above is complete and the change validates
- [ ] 9.2 `openspec archive add-tunnel-ingress-management` so the delta specs land in `openspec/specs/` (the archive command is what updates main specs; do not hand-sync them earlier or the specs would describe unverified behaviour)
- [ ] 9.3 Commit the archive move and then merge the branch into `main` with `--no-ff`, matching the `add-tunnel-dns-management` precedent
- [ ] 9.4 Tag the release as `v0.4.0` (new capability, backwards compatible)
- [ ] 9.5 Leave the version bump / remote push to the user; do not push
