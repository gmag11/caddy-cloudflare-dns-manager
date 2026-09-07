## 1. Project scaffolding and spikes

- [x] 1.1 Initialize a Go module for the plugin and verify `go build ./...` succeeds with a stub package
- [x] 1.2 Add a minimal Caddy module skeleton (register module IDs) and verify `caddy list-modules` or a unit test imports the module without error
- [x] 1.3 Spike: confirm whether a `cf_dns_manager` directive nested inside a `handle` block can resolve the site block's named matchers at Caddyfile adapt time; record the finding and pick the directive placement (nested or site-level) accordingly. Verify by adapting a sample Caddyfile containing a wildcard site with `handle` and reading the produced JSON or error
- [x] 1.4 Decide and record whether the route directive is implemented as a no-op middleware or a small HTTP app hook for config-load reconciliation (see design.md Open Questions). Verify the choice is documented in design.md

## 2. Caddyfile adapter: global options

- [x] 2.1 Register `cf_dns_manager` as a global option and parse zone declarations (`zone <zone> api_token <token>` plus optional `prune`, `ip_url`, `tag_prefix`, `instance`). Verify an adapt-time unit test parses a global block into the expected option struct
- [x] 2.2 Enforce that any host reconciled maps to a declared zone, erroring on undeclared zones. Verify a test adapts a config referencing an undeclared zone and expects a descriptive error
- [x] 2.3 Implement longest-suffix zone assignment and expose the declared zone set to later stages. Verify a unit test maps `a.app.example.com` to zone `app.example.com` when both `example.com` and `app.example.com` are declared

## 3. Caddyfile adapter: per-site directive

- [x] 3.1 Register the route-directive form of `cf_dns_manager` and parse `host @ref`/FQDN, `ip`, `proxied`, `force_adopt`. Verify an adapt-time test parses a site block and a `handle` block into host entries
- [x] 3.2 Resolve `host @ref` to a single literal host at adapt time, erroring on unknown matchers, non-host matchers, or multi-host matchers. Verify tests cover the three error cases and the happy path
- [x] 3.3 Validate `ip` values are IPv4 and that exactly one host is managed per directive (repeated directives for multiple hosts). Verify tests reject IPv6 overrides and multi-host `@ref`
- [x] 3.4 Derive the record name and target zone for each resolved host, including apex (`@`) and nested names; ensure wildcard site addresses never become records. Verify unit tests for apex, nested, and wildcard-ignored cases

## 4. Cloudflare client

- [x] 4.1 Implement a minimal Cloudflare REST client (list zone id by name, list/create/update/delete DNS records, with token auth) using the standard library or a vetted dependency. Verify against the Cloudflare API schema with recorded/httptest fixtures
- [x] 4.2 Verify client error handling surfaces HTTP/API errors with actionable messages. Verify tests simulate API error responses

## 5. Public IP detection

- [x] 5.1 Implement public IPv4 detection with configurable `ip_url`, defaulting to `https://cloudflare.com/cdn-cgi/trace`, run once per config load. Verify a test points `ip_url` at a local httptest server returning a trace/body and asserts the parsed IPv4
- [x] 5.2 Implement IPv4-only parsing and non-blocking failure semantics: warning log, leave existing auto-IP records, skip creating new auto-IP records, still reconcile explicit-IP hosts. Verify tests for endpoint returning IPv6, network error, and non-2xx response

## 6. Reconciliation engine

- [x] 6.1 Implement per-host reconciliation on config load: effective IP selection (override vs shared detected public IP), comparing existing record, and create/update when drift exists, idempotent when matching. Verify unit tests for no-op match, new record creation, and IP/proxy-mode drift update
- [x] 6.2 Enforce private/reserved-IP => DNS-only even when `proxied yes`, and honor explicit `proxied no`. Verify tests with a CGNAT/private IP (e.g. `100.64.10.5`) and a public IP
- [x] 6.3 Tag created/adopted records with the ownership comment (`<tag_prefix>:<instance>`) and default instance to hostname. Verify tests assert the comment on create and the configurable prefix/instance paths
- [x] 6.4 Implement the conservative default (skip untagged records with a log) and `force_adopt` update-and-claim. Verify tests for both paths

## 7. Prune

- [x] 7.1 Implement per-zone `prune`: after forward reconciliation, list the zone's A records and delete only those tagged with this instance and no longer declared. Verify tests cover orphan deletion, kept-if-still-declared, and never-deleting untagged/other-instance records

## 8. Integration and validation

- [x] 8.1 Wire reconciliation into config load and confirm it runs on each reload without blocking startup on detection failure. Verify by running Caddy with the plugin against a fixture Caddyfile and observing logs/config application
- [x] 8.2 Add an end-to-end fixture test (httptest Cloudflare + local detection endpoint) covering a full reconcile + prune cycle. Verify the fixture asserts the expected Cloudflare API call sequence
- [x] 8.3 Write or update Caddyfile documentation/examples for both directive contexts and the policy table (conservative/force_adopt/prune). Verify docs build/read cleanly
- [x] 8.4 Run `go vet`, `go test ./...`, and `gofmt -l` and confirm clean; validate the change's specs with `openspec validate add-cloudflare-dns-manager-plugin`
