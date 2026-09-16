## 1. Reconcile the contract with the implementation

- [x] 1.1 Confirm `parseTunnelService` accepts exactly the set the new "Accepted tunnel service values" requirement mandates: the six network schemes, the two socket prefixes with a path, and a three-digit `http_status:` code
- [x] 1.2 Confirm it rejects every value the "Built-in test server", "Jump host", "URL with a scheme but no address" and "Status code of the wrong shape" scenarios name
- [x] 1.3 Confirm a bare prefix (`unix:`, `unix://`, `unix+tls:`, `unix+tls://`) is rejected under the "Socket prefix with no path" scenario, counting leading slashes as no path
- [x] 1.4 Confirm `docs/cloudflare-tunnel.md` states the same accepted set and the socket-path rule, so the reference page and the requirement agree

## 2. Verify the socket-path contract against the platform

- [x] 2.1 Re-run `cloudflared tunnel ingress rule` against each accepted socket spelling and confirm the echoed path matches the "Everything after the prefix is the path" scenario, so the contract describes what the platform actually does
- [x] 2.2 Re-run `cloudflared tunnel ingress validate` against each accepted spelling and confirm every value the contract mandates is accepted by the binary

## 3. Cover the gaps the contract closes

- [x] 3.1 Add a validator case for `tunnel_service` alongside the existing `tunnel_default_service` one, so the per-host path is exercised by the same accepted-set rule
- [x] 3.2 Confirm `matchSNItoHost` is not applied to a `unix+tls:` service, matching the "A TLS socket is left alone" scenario, and that the reasoning is recorded where a reader would look
