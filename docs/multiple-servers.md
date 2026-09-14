# Multiple Caddy servers, one Cloudflare zone

Running more than one Caddy server (or container) that manages records in the
same zone — e.g. a primary and a standby, or per-service containers. The plugin
is built for this: every record it touches is tagged, and every decision is
tag-scoped.

## How ownership works

Every record the plugin creates, updates or adopts carries a comment:

```
<tag_prefix>:<instance>
```

Defaults: `tag_prefix` = `caddy-cf-dns`, `instance` = the machine hostname. The
tag is the single source of truth for every operation:

```
record exists at a managed name?
  ├─ no tag at all          → untouched (unless force_adopt on that host)
  ├─ my tag                 → update on drift, prune when undeclared
  └─ another instance's tag → never touched, not even by force_adopt
```

Consequences:

- Server A never updates or deletes server B's records — even if both declare
  the same host (each would fight; see "Declarative split" below).
- Hand-made records and records from other tools are invisible to the plugin.

## Rule 1: give every instance a stable id

Inside Docker the default `instance` (hostname) is the **container ID**, which
changes on every recreate. After an image update you would end up with two
instances — the old records become foreign to the new container and are never
pruned. Always set a stable id:

```
{
	cf_dns_manager {
		zone example.com api_token {$CF_API_TOKEN} prune
		instance edge-primary          # stable across recreations
	}
}
```

A sane scheme: `<role>-<n>` (`edge-primary`, `edge-secondary`) or the DNS name
of the server. Also distinguish *logical* servers that share a machine.

## Rule 2: split hosts declaratively, not by luck

Two instances declaring the **same host** is a conflict: each sees the other's
tagged record as "another instance's" and refuses to touch it, so the record
stays with whoever created it first. Assign each host to exactly one server:

```
# server 1 (edge-primary)
app.example.com  { cf_dns_manager { host app.example.com } }

# server 2 (edge-secondary)
git.example.com  { cf_dns_manager { host git.example.com } }
```

If you must migrate a host from A to B, either delete A's record first (A's tag
identifies it) or use `force_adopt` on B — adopting another instance's tagged
record deliberately requires removing/retagging it manually, which is by design.

## Rule 3: `prune` is per-instance safe

With `prune` enabled, a zone cleanup deletes only records tagged with **this**
instance whose host is no longer declared by *this* config. Therefore:

- Enable `prune` freely on every instance; they prune only their own orphans.
- A stale tag after decommissioning a server means its records are never pruned
  — they linger until deleted by hand. Search the comment prefix in the
  dashboard to find them.

## Failover pattern (shared address, one writer)

For an active/passive pair pointing at the same IP, the simplest safe setup is:

- **Active**: declares the hosts (creates/updates records).
- **Passive**: declares nothing (or hosts it does *not* own), so it never writes.

A periodic failover script can switch DNS by other means (API, load balancer);
the plugin deliberately does not implement leader election.

## Checklist

- [ ] Every instance sets a stable `instance` (or a distinct `tag_prefix`)
- [ ] No host declared by two instances at once
- [ ] `prune` understood as instance-scoped (safe to enable everywhere)
- [ ] Retired instances' leftover records identified by comment and removed
