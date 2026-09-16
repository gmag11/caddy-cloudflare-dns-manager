## 1. Global tunnel registry

- [x] 1.1 Add a `TunnelRegistry` field to `App` (name to UUID), carried in the config JSON like `Zones`.
- [x] 1.2 Parse a repeatable `tunnel <name> <uuid>` subdirective in `parseGlobalOption`, rejecting a missing name, a missing UUID, a duplicate name, and a UUID that fails `isUUID`.
- [x] 1.3 Keep `isUUID` at this new registration point; leave the format check reachable for the registry and for the target parser in § 3.
- [x] 1.4 Add a lookup helper returning the UUID for a name and reporting whether it was found, so both adapt-time resolution and the runtime registry check use one implementation.

## 2. Per-host name resolution

- [x] 2.1 Change `parseHostBlock`'s `tunnel` case to take a name, and move `isUUID` out of it.
- [x] 2.2 Read the registry in `parseDirective` alongside `zonesFromOptions` and resolve the host's tunnel name to a UUID before emitting the route, mirroring how `assignZone` validates against declared zones.
- [x] 2.3 Reject an unknown name with an error naming it and listing the registered names.
- [x] 2.4 Reject a UUID-shaped unregistered value with a distinct error that states `tunnel` takes a registered name and that the UUID belongs in the global block.
- [x] 2.5 Store the resolved UUID in `HostConfig.TunnelID` so the CNAME target, `deriveIngressPlan`'s grouping and the Tunnel API path are untouched. Do not add a second source of truth for the UUID.

## 3. Carry the tunnel identifier out of the deletion sites

- [x] 3.1 Add a helper that parses a CNAME target of the form `<uuid>.cfargotunnel.com` and returns the lowercased UUID, or empty when the target is anything else (a different suffix, a missing label, or a label that fails `isUUID`). Strip a trailing dot first, and match case-insensitively.
- [x] 3.2 Change `pruneZone` to report, per deleted record, the hostname and the tunnel UUID parsed from `r.Content` (empty when the record does not name a tunnel). Keep the existing `pruned orphan record` log unchanged.
- [x] 3.3 Change `clearConflictingRecords` to report the tunnel UUID parsed from a deleted CNAME's content alongside the existing `deletedCNAME` flag, so a tunnel-to-address revert carries the tunnel it came from.
- [x] 3.4 Thread the UUID through `reconcileZone`'s result: `reconcileZoneResult` replaces `prunedHosts []string` with a slice carrying hostname plus tunnel UUID, and both the prune path and the `clearedNames` path populate it.
- [x] 3.5 Thread it through the ingress phase: build the pruned set as a map keyed by lowercased hostname whose value carries the tunnel UUID, replacing the current `map[string]bool`.

## 4. Visit tunnels that only a deletion identifies

- [x] 4.1 In `reconcileTunnelIngress`, collect tunnel UUIDs from the pruned set in addition to the tunnels with a derived plan, filtered to those the registry contains.
- [x] 4.2 Report separately, and act on neither: a pruned name whose deleted record named no tunnel, and one naming a UUID outside the registry. Keep the manual-removal wording for the first and name the tunnel for the second.
- [x] 4.3 Build one map keyed by tunnel UUID holding an optional derived plan, so a tunnel that is both planned and named by a deletion is visited once on the normal path.
- [x] 4.4 Keep the existing early return for the case where no tunnel is identified at all, so a config that prunes only address records does not reach the Tunnel API.

## 5. Prune-only write for a tunnel with no plan

- [x] 5.1 Add a function that drops the rules whose hostname is in the pruned set and returns the remaining rules exactly as read, preserving order, and reports whether anything was dropped.
- [x] 5.2 Do not re-emit a catch-all on this path: the rule with no hostname always survives, so the written configuration still ends with the catch-all it had.
- [x] 5.3 Do not apply `inheritUnmanagedFields` or `ensureMatchSNItoHost` here, so no foreign rule is mutated and no option is added.
- [x] 5.4 Skip a rule whose hostname matches a declared host, so the never-prune-declared-hosts guarantee holds identically on this path.
- [x] 5.5 Route `reconcileOneTunnel` to this function when the tunnel has no plan, into the merge when it has one, and issue at most one write per tunnel either way. Keep the `source: local` refusal ahead of both.
- [x] 5.6 Log each deletion with its hostname and state that the record was deleted in the same run, matching the existing message on the plan path.

## 6. Tests

- [x] 6.1 Registry tests: a valid entry, several distinct entries, a duplicate name, a malformed UUID, and both missing-argument cases.
- [x] 6.2 Resolution tests: a registered name resolves to its UUID, an unknown name is rejected listing the registered names, and a UUID-shaped unregistered value is rejected with the migration message.
- [x] 6.3 Confirm the existing tunnel, ingress and prune suites across `tunnel_test.go`, `tunnelingress_test.go` and `reconcile_test.go` are updated to the `tunnel <name>` spelling and still pass.
- [x] 6.4 Table-test the target parser: a valid UUID, uppercase, a trailing dot, a target without the `cfargotunnel.com` suffix, a wrong suffix, a non-UUID label, an empty string, and a plain `somewhere.example.net`.
- [x] 6.5 Assert a tunnel-to-address revert with no host declaring the tunnel deletes the route, driven through `Reconcile` end to end, and that no derived plan is written (the catch-all keeps its original service).
- [x] 6.6 Assert the prune-only write preserves surrounding rules exactly: seed the mock with a rule the plugin did not write, including an `originRequest` option such as `http2Origin` and a rule with an explicit `path`, and confirm they come back unchanged and in order.
- [x] 6.7 Assert no route is added on the prune-only path: a registered tunnel whose deleted record names it but whose hostname has no matching rule produces no write.
- [x] 6.8 Assert the two residual paths: a pruned name whose deleted record is an A record, and one whose CNAME names an unregistered UUID, each contact no tunnel and are reported distinctly.
- [x] 6.9 Assert idempotence: a second reconcile after a prune-only write issues no update for that tunnel.
- [x] 6.10 Assert a planned tunnel plus an unplanned registered tunnel in one run each get exactly one write, and that the planned one still receives its plan.
- [x] 6.11 Update the existing last-host test to expect deletion instead of the "no tunnel is declared" log line.

## 7. Documentation

- [x] 7.1 Document the registry and the new spelling in `README.md`, `docs/cloudflare-tunnel.md` and `docs/publishing-addresses.md`, replacing every `tunnel <uuid>` example.
- [x] 7.2 Add the migration to `docs/troubleshooting.md`: the error for an unregistered UUID-shaped value, and the two-line fix.
- [x] 7.3 Rewrite the "last tunnel host" limit in `docs/cloudflare-tunnel.md`: the route is now pruned, and the manual-removal remedy applies only to names whose deleted record does not identify a registered tunnel.
- [x] 7.4 Update the affected entries in `docs/troubleshooting.md` ("A removed host still appears in the tunnel's Routes" and its last-host bullet) and the `tunnel <uuid>` references in `docs/docker-deployment.md`.
- [x] 7.5 Update the `testenv` files: `Caddyfile` to register the tunnel and reference it by name, `.env.example` and `README.md` accordingly.
- [x] 7.6 State the widened reach plainly: the plugin may write to a registered tunnel no host declares, and does so only to remove rules whose record it deleted in the same run, under the `prune` opt-in.
- [x] 7.7 Note the `tunnel`-to-`tunnel` switch and the shared per-tunnel default service as known limits, so the remaining gaps are documented rather than discovered.

## 8. Verification

- [x] 8.1 Run `go build ./...`, `go vet ./...`, `gofmt -l .` and the full `go test ./...`.
- [x] 8.2 Run `openspec validate register-named-tunnels-and-prune-routes --strict` and `openspec validate --all --strict`.
- [x] 8.3 Confirm the regression baseline: the existing suite passes with no `prune` opt-in, so nothing is deleted when the operator has not opted in.
- [x] 8.4 Confirm the adapt-time rejections by hand: an unknown tunnel name, and a bare UUID that is not registered.
- [x] 8.5 Manual E2E in `testenv`: reproduce the original case — both hosts switched from tunnel to host — and confirm both routes are deleted with `pruned_routes: 2` and no manual-removal log.
- [x] 8.6 Manual E2E negative: switch one host back to the registered tunnel and confirm its route is recreated, then switch it to `host` again and confirm it is deleted once more.
