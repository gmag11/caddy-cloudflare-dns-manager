## Context

The harness has one tunnel and needs it remotely-managed. "Remotely-managed" is not a preference:
the plugin writes the ingress plan through `PUT /accounts/{id}/cfd_tunnel/{tunnel_id}/configurations`,
and a locally-managed tunnel keeps its rules in a YAML file on the origin machine, so the plugin's
writes would never be read by `cloudflared`. `config_src: cloudflare` at creation time is
therefore load-bearing, not cosmetic.

Three facts about the API constrain the implementation, all verified against the live API with the
harness's own credentials rather than assumed:

- **Creation does not return the token.** `POST /accounts/{id}/cfd_tunnel` accepts
  `{name, config_src}` and returns a `CloudflareTunnel` object whose `id` is the tunnel UUID. There
  is no token in the response. The run token comes from a separate endpoint,
  `GET /accounts/{id}/cfd_tunnel/{tunnel_id}/token`, whose `result` is a bare JSON **string**, not
  an object — a shape difference that silently yields `null` from a naive `.result.token` jq path.
  So provisioning is two calls, not one.
- **The account-scoped token cannot find its own account.** `GET /accounts` with
  `CF_ACCOUNT_TUNNEL_TOKEN` returns `{"success":true,"result":[]}`: the token is scoped to Tunnel
  Write, which carries no account-read permission, and the endpoint answers 200 with an empty list
  rather than 403. The same token calling `GET /zones?name=<zone>` also returns an empty `result`.
  Consequently the account id **cannot** be derived from the token that does the creating.
- **The zone-scoped token can.** `GET /zones?name=<zone>` with `CF_API_TOKEN` returns the zone and,
  nested in it, `account.id`. This is exactly the derivation the plugin uses
  (`cloudflareClient.zoneByName`, `cloudflare.go`), and it works because a tunnel and its zone
  always live in the same account — a `cfargotunnel.com` CNAME only proxies records in the same
  account, so zone and tunnel cannot straddle a boundary.

The token the plugin uses to write ingress, and the token the script uses to create the tunnel, are
the same credential (`CF_ACCOUNT_TUNNEL_TOKEN`, account-scoped, Tunnel Write). The script therefore
introduces no new permission: it exercises a capability the harness already holds.

Constraints carried from the harness: it is not part of the published module (`testenv/README.md`
says so), `.env` is gitignored while `.env.example` is tracked, and the documented workflow is a
copy of `.env.example` followed by editing the copy.

## Goals / Non-Goals

**Goals:**

- Provision the harness tunnel end-to-end from `testenv/.env` alone, with no dashboard visit and no
  hand-copied value.
- Make the script safe to call unconditionally: a configured harness is left untouched, and the
  exit status distinguishes "already provisioned" from a real failure.
- Keep the account id derived rather than configured, so the script needs no value the harness does
  not already hold.
- Never write the run token to stdout, a temp file, or a shell history line.

**Non-Goals:**

- Provisioning anything but the tunnel: no DNS records, no ingress rules, no Caddyfile edits. The
  plugin writes DNS and ingress; the script only makes the tunnel exist and records its identity.
- Becoming a general Cloudflare tunnel CLI. It handles the harness's single tunnel, named from
  `CF_INSTANCE`, and refuses to guess beyond that.
- Deleting or rotating tunnels. Rotating a token is a deliberate act the script does not perform
  implicitly.
- Managing tunnels for real deployments. `docs/docker-deployment.md` documents the operator-facing
  flow and stays as it is; this is test-harness tooling.

## Decisions

### Literal emptiness is the guard, with its consequence documented

The script creates only when `CF_TUNNEL_TOKEN` and `CF_TUNNEL_ID` are unconfigured, and the
definition of unconfigured is **a line that is absent, or present with an empty value**. The
template's placeholders (`replace-me`, `00000000-0000-0000-0000-000000000000`) are *not* treated
as unconfigured.

This is the stricter reading, chosen deliberately. The consequence is concrete and worth stating
plainly: `cp .env.example .env && ./create-tunnel.sh` on a clean checkout **provisions nothing**,
because the copied file is not empty. The operator must blank those two lines — or delete them —
before the first run. The script prints that instruction when it detects placeholder values, so the
requirement is discoverable rather than a silent no-op.

*Alternative rejected:* treating placeholder shapes as unconfigured, which makes the clean-checkout
path work with one command. It was rejected because "looks like a placeholder" is a heuristic on
values the script does not own, and a heuristic that misfires is worse than an explicit step: it
would act on a `.env` an operator had deliberately filled with something unusual. The cost is one
documented edit; the benefit is that the script only ever acts on an unambiguous signal.

### A half-configured pair fails loudly instead of proceeding

If exactly one of the two values is unconfigured, the script exits non-zero with a message naming
the unconfigured one and creates nothing.

The reasoning is about which failure is recoverable. A tunnel created on a half-configured harness
is a real, billable resource in the account with no line in `.env` describing it; the operator has
to find it in the dashboard and delete it. A refusal costs one command and a clear message. The
asymmetry is large enough that guessing is not worth it — and the two values are only ever half-set
by manual error, so the refusal is also the more informative outcome.

*Alternative rejected:* creating and filling the missing half. It sounds helpful and is the worst
case: if `CF_TUNNEL_TOKEN` is set but `CF_TUNNEL_ID` is not, the token already belongs to *some*
tunnel, and filling in the UUID of a freshly created one would pair a token and an id that describe
different tunnels — precisely the inconsistency the script exists to prevent.

### Creation is two calls, and the second one is typed for the bare string

```
POST /accounts/{account_id}/cfd_tunnel
  {"name": "<CF_INSTANCE>", "config_src": "cloudflare"}   -> {"result":{"id":"<uuid>", ...}}
GET  /accounts/{account_id}/cfd_tunnel/<uuid>/token       -> {"result":"<token-string>"}
```

The token endpoint's `result` is a string. The script reads it as `jq -r '.result'` and validates
that it is non-empty before writing anything, so a shape change is caught at provisioning time
rather than surfacing later as `cloudflared` failing to connect with an empty token.

The two calls are not atomic: a create that succeeds followed by a token fetch that fails leaves a
tunnel with no `.env` entry. The script reports the created UUID in that case so the resource is
identifiable, and does not write a partial `.env`. Re-running then reuses the existing tunnel (next
decision) instead of creating a second one.

### An existing tunnel of the same name is reused, not duplicated

Once the guard decides to provision, the script lists the account's tunnels and looks for one named
`CF_INSTANCE`. If exactly one matches, its UUID and token are adopted and written; if none matches,
a tunnel is created; if several match, the script stops.

Without this, the plausible sequence "delete the two `.env` values to re-provision, but the tunnel
is still in the dashboard" creates a second tunnel with a duplicate name — the same orphan the
half-configuration guard exists to prevent. Reuse is also what makes a re-run after a failed token
fetch recover rather than duplicate.

*Alternative rejected:* create unconditionally and let the name collide. Cloudflare permits
duplicate tunnel names, so nothing would stop the second tunnel from existing; the collision would
be discovered by a human in the dashboard, which is the outcome this change removes.

*Alternative rejected:* `GET /accounts/{id}/cfd_tunnel?name=<name>` server-side filtering. Listing
tunnels and filtering client-side is used instead because the harness account holds few tunnels and
the filter parameter's exact semantics across API versions is not worth depending on.

### The tunnel name is `CF_INSTANCE`

`CF_INSTANCE` is already the harness's stable identity: the plugin uses it as the ownership tag
prefix so records survive container recreation, and it is the one value in `.env` that names "this
deployment". Reusing it means the tunnel in the dashboard is labelled with the same string the
harness reports, so a human correlating a dashboard entry with a running harness needs no lookup
table.

*Alternative rejected:* a fixed name like `cfdns-test`. It is what the current README suggests, but
it is a second name for the same concept, and it collides as soon as two harnesses share an account
— which the multi-server documentation (`docs/multiple-servers.md`) expects.

### The account id is derived with the zone token, never configured

```bash
account_id=$(curl ... -H "Authorization: Bearer $CF_API_TOKEN" \
  "…/zones?name=$CF_ZONE&per_page=1" | jq -r '.result[0].account.id')
```

This mirrors `zoneByName` and keeps the harness's property that the account id is not a configured
value. The plugin's own `account_id` subdirective exists as an optional override for the same
derivation; the script does not add an equivalent, because the failure mode it would cover
(`CF_API_TOKEN` lacking Zone Read) is better reported as an error than papered over with a second
source of truth.

*Alternative rejected:* `GET /accounts` with `CF_ACCOUNT_TUNNEL_TOKEN`. Verified not to work: the
endpoint returns an empty list for a Tunnel-Write-scoped token, and the empty list is
indistinguishable from "the account has no accounts" unless the script special-cases it. Deriving
from the zone is both correct and consistent with the plugin.

### `.env` is rewritten in place, preserving every other line

The two values are replaced where they are, or appended with a comment if the line is missing.
Comments, ordering, and unrelated variables survive, because an operator's `.env` holds other
credentials and hand-written notes, and rewriting the file from a template would discard them.

The write is validated first: both values are confirmed non-empty in memory before the file is
touched, so `.env` is never left half-updated. The run token is written to the file with ordinary
file permissions (the file is gitignored); it is never echoed to stdout, passed as a command-line
argument (where it would land in `ps` and the shell history), or written to a temporary file.

*Alternative rejected:* printing the values for the operator to paste. It is what the current README
documents and it is exactly the hand-copying step being removed; it also puts the token in the
terminal scrollback, which the script avoids.

## Risks / Trade-offs

- **The strict emptiness guard costs one manual edit on a clean checkout.** Mitigated by printing
  the instruction with the exact lines to blank when placeholders are detected. Accepted as the
  price of never acting on a heuristic.
- **Deriving the account id ties provisioning to a Zone-Read token.** A harness whose `CF_API_TOKEN`
  cannot read the zone gets a clear error rather than a fallback. Accepted: the same token is
  already required for the DNS side of the harness, so it is never absent.
- **Reuse-by-name can adopt a tunnel the operator did not intend.** Mitigated by requiring an exact
  `CF_INSTANCE` match and by stopping when the name is ambiguous rather than picking one.
- **The run token is a credential the script now handles.** Mitigated by never printing it and
  writing it only to the gitignored `.env`; the token is also revocable from the dashboard, and its
  blast radius is the single tunnel it authenticates, not the account.
- **`jq` and `curl` become hard dependencies of the harness setup.** Both are already assumed by the
  README's verification commands (`dig`, `curl`), and the script checks for them and fails with an
  installation hint rather than a shell error.

## Migration Plan

1. Add `testenv/create-tunnel.sh` and make it executable.
2. Update `testenv/README.md` so the one-time setup is the script, with the dashboard procedure kept
   as a fallback for operators who prefer it.
3. Update `testenv/.env.example` comments to point at the script and state the strict-emptiness rule.
4. Verify: run against a harness whose two values are blanked, confirm the tunnel is created, both
   values written, and a second run is a no-op.
5. No archive-time spec sync is implied for the plugin's own specs: this change adds a harness
   capability and modifies no plugin behaviour.
