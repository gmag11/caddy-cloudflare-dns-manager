## 1. Revert the ownership-tag mechanism

- [x] 1.1 Drop the `tag` parameter from `deriveIngressPlan` and stop writing `Description` into derived rules
- [x] 1.2 Remove `Description` from `ingressRulesEqual`, so the field is no longer a drift signal
- [x] 1.3 Restore `Description` to `inheritUnmanagedFields` alongside `path` and `originRequest`, so an operator-set value survives a rewrite
- [x] 1.4 Drop the tag argument from `reconcileTunnelIngress` and its call site in `reconcileIngressPhase`
- [x] 1.5 Replace the tag-semantics tests with their inverse: the plugin does not author a description, does not treat one as drift, and preserves the operator's value on rewritten rules

## 2. Correlate DNS deletions with routes

- [x] 2.1 Have `pruneZone` return the fully-qualified hostnames it deleted, including the partial-progress list when a deletion fails mid-loop
- [x] 2.2 Introduce `reconcileZoneResult` carrying the zone's account id and the pruned hostnames, and return it from `reconcileZone` on every path
- [x] 2.3 Collect the pruned hostnames per zone in `Reconcile` next to `accountByZone`, and pass the flattened set into `reconcileIngressPhase`
- [x] 2.4 Flatten the per-zone sets to a single lowercased hostname set in the ingress phase, since route matching is zone-agnostic
- [x] 2.5 Confirm a zone whose DNS reconcile failed contributes no pruned names, so its routes are never candidates
- [x] 2.6 Have `clearConflictingRecords` report whether it deleted a CNAME, and feed those hostnames into the same set behind the `prune` opt-in, so a tunnel-to-address revert cleans up its route too
- [x] 2.7 Test the revert in both modes: with `prune` the CNAME and the route are both gone, without it the CNAME goes and the route stays

## 3. Prune in the ingress phase

- [x] 3.1 Add the pruned-name set as a parameter of `mergeIngressPlan`, and drop a foreign rule only when its hostname is in that set
- [x] 3.2 Return the list of pruned hostnames from `mergeIngressPlan` so the caller can log each one
- [x] 3.3 Check declaration before prune eligibility, so a declared host is never a candidate even when its name appears in the pruned set
- [x] 3.4 Keep the catch-all structurally ineligible (it has no hostname) and always re-emitted
- [x] 3.5 Apply the deletions to the merged list before the single write, so the plan update and the removals land in one request
- [x] 3.6 Run the phase even when the config declares no tunnel hosts, but only to report; do not enumerate the account's tunnels
- [x] 3.7 Log each pruned route with its hostname and the reason (its DNS record was pruned in the same run), and include the pruned count in the plan-write line

## 4. Tests

- [x] 4.1 Prune removes the route whose DNS record this run deleted
- [x] 4.2 Nothing pruned: an undeclared route is preserved, however stale
- [x] 4.3 A declared host is never pruned, even when its name is in the pruned set
- [x] 4.4 The catch-all is never removed and always terminates the configuration
- [x] 4.5 Pruning all of a tunnel's host routes leaves a valid configuration
- [x] 4.6 A second reconcile after a prune issues no write (idempotence)
- [x] 4.7 No tunnel declared: no request is made and the need for manual removal is logged
- [x] 4.8 `mergeIngressPlan` reports exactly the pruned hostnames, and a name in the set with no matching rule changes nothing
- [x] 4.9 The plugin authors no description, and a description that appears is not drift

## 5. Documentation

- [x] 5.1 Document in `docs/cloudflare-tunnel.md` that route cleanup is driven by DNS prune: a route is removed when the plugin deletes the record that made its hostname reachable
- [x] 5.2 State the limit plainly: removing the last tunnel host prunes the record and leaves the route, with a log line pointing at manual removal
- [x] 5.3 Remove the documentation that described the ownership tag on routes, including the Description-column note and the "tag alone never authorises deletion" wording
- [x] 5.4 Add a troubleshooting entry for a route that was pruned and how to restore it (re-declare the host)
- [x] 5.5 Add a troubleshooting entry for the "no tunnel declared" case
- [x] 5.6 Update `docs/architecture.md`: the ingress phase now consumes the DNS phase's pruned-name set, and why the correlation replaced the tag
- [x] 5.7 Update the README's tunnel section accordingly

## 6. Verification

- [x] 6.1 Run `go build ./...`, `go vet ./...`, `gofmt -l .` and the full `go test ./...`
- [x] 6.2 Run `openspec validate prune-orphaned-tunnel-routes --strict`
- [x] 6.3 Confirm the regression baseline: with no `prune` opt-in the pre-existing suite passes unchanged
- [x] 6.4 Manual E2E in `testenv`: removed one tunnel host among several; its CNAME was pruned and its route deleted in the same run (`pruned_routes: 1`), with a declared host's `originRequest` intact
- [x] 6.5 Manual E2E negative: a declared host's route survives even when its name appears in the pruned set
- [ ] 6.6 Manual E2E for the documented limit: remove the last tunnel host and confirm the log line about manual removal appears
