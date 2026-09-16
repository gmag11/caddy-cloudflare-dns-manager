# Prune orphaned tunnel routes

## Why

The plugin writes an instance tag into every ingress rule it authors, but never deletes a rule: removing a host from the Caddyfile leaves its route on the tunnel forever, and the Routes list in the dashboard silently accumulates dead entries. DNS records already solve this — `prune` deletes owned records whose name is no longer declared — and the tag now exists on ingress rules too, so the same cleanup is possible.

## What Changes

- Prune the tunnel's ingress rules on reconcile, gated on the zone-style opt-in: rules are deleted only when the operator has asked for it **and** the rule is unambiguously dead.
- A rule is a prune candidate only when **all** of these hold:
  - its `description` equals this instance's ownership tag;
  - its hostname is not declared by this configuration;
  - the hostname does not resolve (no DNS record at that name).
- The third condition is what makes this safe to ship. The plugin owns both halves of a tunnel host — the CNAME and the ingress rule — so it can prove a route is unreachable rather than assume it. A route whose DNS record still exists is never deleted, whatever its tag says.
- Prune never deletes the last rule: the mandatory catch-all is always re-emitted by the reconciler, so the configuration can never become invalid.
- **BREAKING** for the tag's meaning only: `description` becomes actionable for deletion under those conditions. It is still never trusted on its own.
- Document the opt-in, the three conditions, and how to audit what was deleted.

## Capabilities

### New Capabilities

- `tunnel-ingress-prune`: The opt-in and its scope, the three-condition eligibility rule, the DNS-liveness precondition and why it is required, the catch-all exemption, logging and auditability of deletions, and the interaction with the existing no-delete default.

### Modified Capabilities

- `tunnel-ingress-management`: The requirement "Undeclared ingress rules are not deleted" changes from an unconditional guarantee to a conditional one: rules are preserved unless the operator opted into prune and the eligibility conditions hold. The ownership-tag requirement gains the tag's role in eligibility. All other requirements are unchanged.
- `record-ownership`: The `prune` opt-in is currently zone-scoped and described as covering DNS records. Its scope is clarified to cover this instance's orphaned artifacts in that zone's tunnels as well.

## Impact

- **Code**: a prune pass in the ingress module (`tunnelingress.go`), invoked from the tunnel reconcile after the plan write; it needs the set of resolving names, which the DNS phase already computes, so the zone reconciliation must surface the names it left in place (or the ingress pass re-reads them).
- **Config**: reuses the existing per-zone `prune` flag. No new directive is introduced.
- **Safety model**: the first destructive path in the ingress code, so it ships with a conservative precondition (DNS-liveness) rather than mirroring DNS prune exactly.
- **Tests**: eligibility unit tests for each condition and each combination; a test that a rule with a live DNS record is never deleted; a catch-all exemption test; an opt-out no-op test; an audit-log test.
- **Docs**: `docs/cloudflare-tunnel.md` (prune section), `docs/troubleshooting.md` (recovering a wrongly pruned route), `docs/architecture.md` (the prune pass and why the liveness precondition exists).
- **Not included**: pruning rules that have no tag (they cannot be attributed), pruning across zones, or reconciling routes for tunnels that are not declared.
