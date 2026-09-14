# Developing and testing locally

How to work on the plugin: run the fast test suite, exercise a real Caddy
against a real Cloudflare zone, and how the pieces fit.

## Layout

```
*.go                plugin package (app, directives, reconcile, cloudflare client)
*_test.go           unit + integration tests, in-package
testenv/            Docker harness for manual/E2E testing (not published; locally
                    excluded via .git/info/exclude)
openspec/           planning artifacts and the normative capability specs
docs/               user guides
```

## Test layers

### 1. Unit/integration tests (fast, no network)

Everything against an in-memory Cloudflare double (`mockcf_test.go`), which
mirrors the real API shapes that matter:

- record names returned as **FQDNs** (the plugin must normalize),
- "identical record already exists" rejection (81058) on duplicate create,
- **CNAME/address coexistence rejection (81054)** — a CNAME cannot coexist with
  A/AAAA at the same name, and vice versa,
- pagination shape for `listRecords`.

IP detection is faked with local `httptest` servers (`testApp`, `testApp6`),
including failure modes (500s) to prove detection failures are non-fatal.

```bash
go test ./...          # seconds, no network
go test -run Tunnel -v ./...
go test -race ./...    # the app accumulates hosts across goroutines
```

Conventions worth keeping:

- Tests construct `App` directly (`testApp`) rather than going through Caddy's
  provisioning; wiring is covered separately (layer 2).
- Log-asserting is avoided: tests assert on recorded API calls
  (`m.hasCall("PUT /zones/...")`) and resulting record state.
- The mock is deliberately strict — when Cloudflare rejects something the plugin
  must handle, teach the mock first (see the 81054 addition), then the code, then
  the test. A permissive mock hides real bugs.

### 2. Wiring tests (Caddy provisioning, still no network)

`e2e_test.go` and `validate_e2e_test.go` build a real Caddy config through the
Caddyfile adapter and assert the resulting JSON wiring — that the app is
registered, hosts are carried through the no-op handlers, and directives are
ordered correctly. Use these when you touch `caddyfile.go`, `app.go` registration
or the handler.

### 3. Real environment (testenv, real Cloudflare)

`testenv/` runs Caddy (with the plugin baked in) and, for tunnels,
`cloudflared` against a real zone. One-time setup and daily use are documented
in its README (`testenv/README.md`). Quick version:

```bash
cd testenv
cp .env.example .env         # fill CF_API_TOKEN, CF_INSTANCE, CF_TUNNEL_ID
docker compose up -d --build
docker compose logs -f caddy
```

The test Caddyfile exercises a normal host (`test1`), a tunnel host (`git`) and
comments showing variants. Reload inside the container after edits:

```bash
docker compose exec caddy caddy reload --config /etc/caddy/Caddyfile
```

Watch for the classic Docker gotchas (see the testenv README): `instance` must
be stable, and editing `tunnel/config.yml` requires
`up -d --force-recreate cloudflared`.

> The testenv `.env` holds a real token. Never commit it (`.gitignore` covers
> `.env*`), and keep the test zone separate from production.

## Manual verification checklist

After a behavior change, exercise the interesting transitions in testenv (each
is a config edit + reload, check the log):

1. New host → `created record`
2. Reload without changes → nothing but debug "already in sync"
3. Change IP → `updated record` with old/new content
4. Remove host (prune zone) → `pruned orphan record`
5. Address ↔ tunnel switch → `deleted conflicting record` + create, **in one
   reload**
6. Untagged record in the way → error naming `force_adopt`, nothing deleted
7. Tunnel-only config → zero IP-detection requests in the log

## Building the image

```bash
docker build -t caddy-cloudflare-dns-manager .          # repo root
# testenv builds its own image via testenv/Dockerfile (also includes
# caddy-dns/cloudflare for ACME DNS-01)
```

## Before opening a PR

- [ ] `gofmt -l .` empty; `go vet ./...` clean
- [ ] `go test -race ./...` green
- [ ] New behavior has a test at the lowest layer that covers it
- [ ] Mock mirrors any Cloudflare behavior the new code depends on
- [ ] If behavior is user-visible: OpenSpec change updated (`openspec/`), specs
      synced, and the relevant guide in `docs/` touched
