## 1. Model and parsing

- [ ] 1.1 Add `IP6 string` and `IP6Mode string` (or equivalent: empty/false/auto/literal) fields to `HostConfig` in `app.go`, keeping old frozen configs decodable (empty == `ip6 false`); add `IP6URL` and `DefaultIP6URL` to `App`
- [ ] 1.2 Implement `ip6` subdirective parsing in `parseHostBlock` (caddyfile.go): accept `false`/`no`/`off`, `auto`, or a valid IPv6 literal; reject anything else at adapt time
- [ ] 1.3 Add `ip6_url` to the global options parser and validate `ip` stays IPv4-only (reject IPv6 values with a clear error)
- [ ] 1.4 Unit tests: ip6 parsing matrix (valid/invalid values, defaults, back-compat of old JSON), ip6_url option, ip IPv4-only validation

## 2. IP detection

- [ ] 2.1 Generalize `publicip.go` to family-aware detection: `detectPublicIP(ctx, client, url, family)` returning the address only when it matches the requested family; keep `parseIPv4Body` behavior for v4 and add the v6 parser that rejects v4/plain garbage
- [ ] 2.2 Add `DefaultIP6URL` constant (v6-only endpoint, e.g. `https://api6.ipify.org`) and short dedicated v6 detection timeout (3–5 s)
- [ ] 2.3 Run v4/v6 detection concurrently in `Reconcile` when needed (v6 only when some host has `ip6 auto`); collect independent results/failures
- [ ] 2.4 Unit tests: family mismatch rejection (v4 body on ip6_url and vice versa), parallel detection results, no-v6-hosts skip

## 3. Reconciliation core

- [ ] 3.1 Change `listRecords` to one paginated unfiltered query, filtering A/AAAA client-side; keep pagination loop
- [ ] 3.2 Convert the existing-records map to `map[nameKey]map[recordType][]cfDNSRecord` and thread per-family state through `reconcileZone`/`reconcileOne`
- [ ] 3.3 Implement per-family reconcile for each host: resolve effective IP per family (literal > detected; `ip6 false` disables AAAA), preserve "skip ⇒ host name still counts as reconciled for that family" semantics
- [ ] 3.4 Enforce per-type owned/untagged search: never cross-family adoption or cross-family PUT; ownership tag identical on A and AAAA
- [ ] 3.5 Mixed-state warning: adapt-time when both literals known; reconcile-time when both families resolve and one is private while the other is public
- [ ] 3.6 Unit tests: dual-family create/update/no-drift, skip semantics per family, cross-family non-adoption, mixed warning, apex/nested names for AAAA

## 4. Private-IP classification

- [ ] 4.1 Rewrite `isPrivateIP` into family-aware classification: v4 (private/loopback/link-local/CGNAT) and v6 (ULA fc00::/7, link-local fe80::/10, loopback ::1), public 2000::/3 proxyable; remove the `To4()==nil ⇒ private` behavior
- [ ] 4.2 Unit tests: classification table (public v4, CGNAT, public v6, ULA, link-local, loopback, malformed)

## 5. Prune

- [ ] 5.1 Extend `pruneZone` to consider both record types; eligibility strictly from declared config: host removed or `ip6 false` ⇒ AAAA prunable in prune-enabled zones; detection-failure skips ⇒ spared
- [ ] 5.2 Unit tests: prune matrix — AAAA pruned on host removal, pruned on `ip6 false` with prune on, orphaned without prune, spared on detection failure, untagged/other-instance untouched for AAAA

## 6. E2E and docs

- [ ] 6.1 Extend e2e/mock tests: dual-family lifecycle on a mock Cloudflare (create both, update drift per family, disable ipv6 with prune, v6 endpoint failure independence)
- [ ] 6.2 Update README: `ip6` subdirective, `ip6_url` global option, proxied/mixed-state notes, Tailscale ULA example
- [ ] 6.3 Run full `go test ./...` and lint; verify back-compat by adapting a pre-change Caddyfile fixture
