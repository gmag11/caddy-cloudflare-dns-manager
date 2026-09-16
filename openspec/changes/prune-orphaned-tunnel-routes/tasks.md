## 1. Liveness data from the DNS phase

- [ ] 1.1 Have `reconcileZone` return, alongside the account id, the set of zone-relative record names that have at least one record, so the ingress phase can answer "does this hostname resolve?" without a second API call
- [ ] 1.2 Collect that set per zone in `Reconcile` next to `accountByZone`, and pass it into `reconcileIngressPhase`
- [ ] 1.3 Represent "unknown" distinctly from "empty": when a zone's DNS reconciliation failed, its entry carries no liveness data so the prune pass skips it rather than treating every name as unresolvable
- [ ] 1.4 Unit-test the unknown-vs-empty distinction at the boundary, since the whole fail-closed guarantee rests on it

## 2. Eligibility

- [ ] 2.1 Implement `pruneEligible(rule cfIngressRule, tag string, declared map[string]bool, resolving map[string]bool, zonePrune bool) (eligible bool, sparedReason string)` returning the reason a rule was spared, so the caller can log it
- [ ] 2.2 Enforce the three conditions in the documented order and return a distinct reason for each: no/other tag, declared hostname, resolving hostname
- [ ] 2.3 Exclude the catch-all structurally (empty hostname is never eligible) rather than by a special case, and keep it in the merged list
- [ ] 2.4 Resolve each rule's hostname to its owning zone (longest declared-zone suffix, as zone assignment already does) and require that zone to have declared `prune`
- [ ] 2.5 Unit tests: each condition alone spares the rule; all three together make it eligible; a rule tagged by another instance is spared; the catch-all is never eligible

## 3. Prune pass

- [ ] 3.1 Implement `pruneIngressRules(merged []cfIngressRule, tag string, declared map[string]bool, resolving map[string]bool, zonePrunes map[string]bool) (kept []cfIngressRule, pruned []prunedRule)` as a pure function over the merged rule list, so it is testable without an API
- [ ] 3.2 Apply it inside `reconcileOneTunnel` after `mergeIngressPlan` and before the write, so the plan update and the deletions land in one `PUT`
- [ ] 3.3 Gate the whole pass on the tunnel's zone set being resolvable; skip the tunnel with a debug log when liveness for the relevant zone is unknown
- [ ] 3.4 Verify by test that a reconcile which both updates the plan and prunes issues exactly one update request
- [ ] 3.5 Verify by test that a follow-up reconcile with nothing left to prune issues zero update requests
- [ ] 3.6 Test that pruning every host-declared rule leaves a configuration whose only entry is the catch-all, and that the write succeeds

## 4. Logging and auditability

- [ ] 4.1 Log each deletion at Info with the rule's hostname, service, the authorising tag, and an explicit statement that no DNS record existed
- [ ] 4.2 Log each spared near-candidate at Info with the condition that spared it (no tag, another instance's tag, declared, or still resolving), so a non-prune is explainable
- [ ] 4.3 Include the pruned count in the existing "wrote tunnel ingress plan" log line so a reconcile's effect is visible at a glance
- [ ] 4.4 Confirm no path logs a rule's description verbatim in a way that could be mistaken for a secret; the tag is configuration, not a credential

## 5. Integration tests against the mock API

- [ ] 5.1 Extend the mock tunnel API to record the written rule list per version so a test can assert what survived
- [ ] 5.2 Prune removes an eligible orphan and leaves the declared rules and the catch-all intact
- [ ] 5.3 No opt-in: the same input deletes nothing
- [ ] 5.4 Opt-in but hostname still resolving: deletes nothing and logs the sparing reason
- [ ] 5.5 Opt-in but the rule is untagged: deletes nothing
- [ ] 5.6 Opt-in but the rule belongs to another instance's tag: deletes nothing
- [ ] 5.7 Declared host in a `prune` zone: never a candidate, and its drift is still corrected
- [ ] 5.8 Zone DNS failure: no pruning for that zone, and no write of any kind when the plan is otherwise unchanged
- [ ] 5.9 Two zones, only one with `prune`: only that zone's orphan is removed
- [ ] 5.10 End-to-end failure isolation still holds: a prune error is surfaced without preventing DNS reconciliation

## 6. Documentation

- [ ] 6.1 Document the opt-in and the three conditions in `docs/cloudflare-tunnel.md`, stating plainly that a route is deleted only when it cannot receive traffic
- [ ] 6.2 Explain why ingress prune is stricter than DNS prune: the tag is not rendered by the dashboard, so liveness is the auditable proof
- [ ] 6.3 Add a troubleshooting entry covering a route that was pruned and how to restore it (re-declare the host)
- [ ] 6.4 Add a troubleshooting entry for "my orphaned route was not pruned", enumerating the conditions and pointing at the sparing log line
- [ ] 6.5 Update `docs/architecture.md` with the prune pass, its position in the ingress phase, and the fail-closed behaviour when liveness is unknown
- [ ] 6.6 Update the README's tunnel section to mention that `prune` now also cleans up routes

## 7. Verification

- [ ] 7.1 Run `go build ./...`, `go vet ./...`, `gofmt -l .` and the full `go test ./...`
- [ ] 7.2 Run `openspec validate prune-orphaned-tunnel-routes --strict`
- [ ] 7.3 Confirm the regression baseline: with no `prune` opt-in the pre-existing suite passes unchanged
- [ ] 7.4 Manual E2E in `testenv`: remove a tunnel host, confirm its DNS record and its ingress rule are both gone, and confirm a declared host's rule keeps its `originRequest`
- [ ] 7.5 Manual E2E negative: with the orphan's DNS record left in place by hand, confirm the rule is preserved and the sparing reason is logged
