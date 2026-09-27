# Tasks

## 1. Reject duplicate host declarations at registration

- [x] 1.1 Make `App.addHost` (registry.go) return an error when the incoming `HostConfig.Host` already has a registered declaration, and propagate that error from `hostHandler.Provision` (caddyfile.go); verify with a unit test that calls `addHost` twice with the same host and asserts the error, and once more with a distinct host and asserts success.
- [x] 1.2 Add load-path tests that build a Caddyfile and run it through provisioning (`caddy.Validate`, as `provisionedApp` does) covering: the same literal FQDN twice; a matcher reference colliding with a literal; equivalent spellings (`Foo.Example.com.` vs `foo.example.com`); a duplicate across address and tunnel modes; and a control case of distinct hosts that loads and registers every host. Verify the duplicates return an error naming the host and the control case registers all hosts (the four collision scenarios and the control in the spec's Unique host declarations requirement).
- [x] 1.3 Add an entry for the new error to `docs/troubleshooting.md` (the "config never loads" section, next to `host specified more than once`) explaining the copied-block cause and the fix; verify the documented error text matches the message the implementation returns.

## 2. Integration verification

- [x] 2.1 Run `gofmt -l .`, `go vet ./...`, and `go test ./...`; verify all pass with no formatting or vet findings, and that the pre-existing per-site directive tests still succeed.
