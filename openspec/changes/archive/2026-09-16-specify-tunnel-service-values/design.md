## Context

`parseTunnelService` (`caddyfile.go`) validates both `tunnel_service` and `tunnel_default_service`
at adapt time. The set it accepts is a hard-coded list of prefixes plus an `http_status:` special
case. No requirement in `openspec/specs/tunnel-ingress-management/spec.md` names that set: the
"Global default tunnel service" requirement only rejects a value that is "neither a supported
service URL nor an `http_status:<code>` service".

That left the accepted set defined only by the implementation, and it drifted. The validator
required `unix://` while Cloudflare's documentation, `cloudflared`'s parser and the plugin's own
`docs/cloudflare-tunnel.md` all write `unix:/path`, so a config derived from any of them was
rejected at adapt time (fixed in `c8b337d`, which accepted every spelling but wrote nothing down).

Two further facts about the accepted set are currently invisible: `bastion` and `hello_world` are
excluded although `cloudflared` accepts them, and `unix+tls:` never receives `matchSNItoHost`
although it performs a TLS handshake. Without a stated intent, both read as omissions.

## Goals / Non-Goals

**Goals:**

- Make the accepted set of service values a written contract, so the validator has a specification
  to be held to rather than being its own specification.
- State the socket-path rule precisely enough that the spelling question cannot reopen.
- Record *why* the two exclusions exist, so a future reader does not "fix" a deliberate decision.

**Non-Goals:**

- Changing `parseTunnelService`. It already accepts exactly what the new requirement mandates.
- Supporting `bastion` or `hello_world`. Neither names a destination, which is the only thing
  these subdirectives are for.
- Validating reachability of a destination. The platform remains the authority on that.
- Changing `matchSNItoHost` behaviour for any scheme.

## Decisions

### D1: State the accepted set, do not point at the platform

The requirement enumerates the accepted values rather than saying "whatever `cloudflared`
accepts", because the two are deliberately not the same (D2). A contract that defers to the
platform would either forbid the exclusions or silently bless `bastion`.

### D2: `bastion` and `hello_world` stay rejected, and the reason is written into the requirement

`bastion` makes `cloudflared` a jump host reachable across the tunnel to any local address —
the opposite of naming a single destination, and a large surface opened by one subdirective line.
`hello_world` is a server `cloudflared` runs itself, for validating connectivity.

Both are decisions, not gaps, so the requirement names them and gives the reason. Without that,
the next person to compare the list against `cloudflared`'s documentation sees an omission.

### D3: A socket is a path, not a URL, and the contract says so

`cloudflared` parses a socket service by trimming the prefix and using the remainder, never
parsing a URL:

```go
// cloudflared: ingress/ingress.go
if prefix := "unix:"; strings.HasPrefix(r.Service, prefix) {
    path := strings.TrimPrefix(r.Service, prefix)   // the remainder IS the path
    service = &unixSocketPath{path: path, scheme: "http"}
}
```

Verified against the real binary by reading back the path it parsed. `String()` reconstructs the
value as `unix%s:%s` from the stored path, discarding any host, so an unchanged echo proves the
whole remainder was taken as the path:

| Written | Echoed by `cloudflared` | Path |
| --- | --- | --- |
| `unix:/run/app.sock` | `unix:/run/app.sock` | `/run/app.sock` |
| `unix:///run/app.sock` | `unix:///run/app.sock` | `//run/app.sock` |
| `unix://run/app.sock` | `unix://run/app.sock` | `//run/app.sock` |

A URL parser would have turned `unix://run/app.sock` into host `run` and path `/app.sock`, and the
echo would have differed. It did not. So the spellings are interchangeable and the contract
mandates accepting all of them, including the form Cloudflare's own examples use.

Cloudflare stores the string **verbatim** — a probe config round-tripped through a live tunnel
returned `unix:/`, `unix:///`, `unix+tls:/` and `unix+tls:///` byte-for-byte — so comparing a
service literally in `ingressRulesEqual` stays a valid idempotence check. Had the API normalised
the spelling, exact comparison would have caused a write on every reload.

### D4: The empty-path guard counts leading slashes as no path

Widening the prefix from `unix://` to `unix:` would have let a bare `unix://` pass the existing
check, since `TrimPrefix("unix://", "unix:")` is `"//"`, not empty. The guard strips leading
slashes before asking, so `unix:`, `unix://`, `unix+tls:` and `unix+tls://` are all rejected.

This is why the requirement phrases the rule as "no path behind the prefix, counting any number of
leading slashes as no path" instead of "an empty remainder": the naive phrasing is exactly the
bug the implementation had to avoid.

### D5: The `unix+tls` exclusion from `matchSNItoHost` is made explicit

`cloudflared` models a unix origin as its own service type, and that type has no such field:

```go
// cloudflared: ingress/origin_service.go
type unixSocketPath struct {
    path, scheme string
    transport    *http.Transport   // no matchSNItoHost
}
```

Only `httpService.start` reads `cfg.MatchSNIToHost`, and only `httpService.RoundTrip` applies it.
So a value on a `unix+tls:` rule would have no effect. The existing requirement already implies
this by scoping the option to `https://`, but the implication sits next to a named exception
(`unix+tls` speaks TLS) that looks like the thing being got wrong. The modified requirement states
the case and says the exclusion is deliberate.

### D6: Both subdirectives are named in the contract

`tunnel_default_service` had a validation scenario and `tunnel_service` did not, although both run
the same validator. Both modified requirements now reference the accepted set, so the per-host path
is covered by contract rather than only by the shared implementation.

## Risks / Trade-offs

- **The requirement duplicates a list that also lives in code.** Two places can drift again. The
  mitigation is that the requirement is now the thing a reviewer checks the code against, which is
  what was missing; the previous state had a single source of truth that was unreadable as intent.

- **Enumerating schemes risks rejecting a future `cloudflared` addition.** Someone adding a new
  scheme upstream would find it refused here. That is the intended trade: these subdirectives
  declare a destination, and an unknown scheme silently reaching a tunnel is a worse failure than
  an adapt-time rejection. Adding one later is a one-line change plus a requirement edit.

- **`unix://run/app.sock` resolves to `//run/app.sock`.** On a POSIX filesystem `//` is
  implementation-defined and collapses to `/` in practice, but it is not guaranteed. The contract
  documents the path as parsed rather than promising an equivalence, so the plugin's acceptance is
  accurate without the spec asserting filesystem behaviour it does not control.

- **No behaviour change means no new test can fail.** The change is specifying, so the value is in
  the contract, not in a diff. The existing table-driven validator test already mirrors the
  mandated set, which is what makes the absence of a behaviour change verifiable.
