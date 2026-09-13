## Why

The plugin currently lives in a `cfdnsmanager/` subdirectory while the Go module is declared at the repository root. Because a Go module path must resolve to a package at its root, `xcaddy build --with github.com/gmag11/caddy-cloudflare-dns-manager` fails and users must reference the awkward subpackage path `github.com/gmag11/caddy-cloudflare-dns-manager/cfdnsmanager`. The sibling provider [caddy-dns/cloudflare](https://github.com/caddy-dns/cloudflare) follows the de-facto standard for Caddy DNS/plugin modules: a root-level Go package, `go.mod` at the root, and a root `Dockerfile`. Aligning with that layout makes the module installable with the canonical one-liner and easier to publish, containerize, and contribute to.

## What Changes

- Move all Go sources from `cfdnsmanager/` to the repository root so the module path `github.com/gmag11/caddy-cloudflare-dns-manager` resolves to a package directly. This makes `xcaddy build --with github.com/gmag11/caddy-cloudflare-dns-manager` work without a subdirectory.
- Rename the Go package identifier from the non-idiomatic `cf_dns_manager` to an idiomatic name matching the directory (e.g. `cfdnsmanager`), without changing any Caddy module IDs (`cf_dns_manager`, `http.handlers.cf_dns_manager_host`) or Caddyfile directives.
- Add the missing standard files the reference repo carries: a root `LICENSE` (Apache-2.0, matching upstream Caddy modules), a root `Dockerfile` (built on `caddy:2-builder-alpine` with `--with <module>=.`), and a CI workflow (`.github/workflows/`) that builds and tests on push/PR.
- Update the README build/install and layout documentation to the canonical command and new file locations.
- Update repository tooling that assumes the old subdirectory: the local `testenv/Dockerfile` `--with .../cfdnsmanager` references and the build cache COPY lines, and the planning/spec references where paths are named.

**BREAKING**: The import path `github.com/gmag11/caddy-cloudflare-dns-manager/cfdnsmanager` disappears; any consumer building against it must switch to `github.com/gmag11/caddy-cloudflare-dns-manager`. Caddyfile behavior and module IDs are unchanged.

## Capabilities

### New Capabilities

- `repository-layout`: the standard, tooling-visible shape of this repository — module path resolving to a root-level Go package, canonical `xcaddy --with <module>` installability, required root files (`LICENSE`, `Dockerfile`, `.github/workflows`), and the constraints that Caddy module IDs and the Caddyfile surface are unaffected by the layout.

### Modified Capabilities

<!-- None: Caddyfile behavior, reconciliation, ownership and IP detection requirements are unchanged. -->

## Impact

- **Code**: all files under `cfdnsmanager/` are relocated to the repository root and their `package` clause renamed; no logic change. `go.mod` module path stays `github.com/gmag11/caddy-cloudflare-dns-manager`.
- **Dependencies**: none added for the move; the root `Dockerfile` uses the `caddy:2-builder-alpine` image and `xcaddy`, consistent with the reference.
- **Build/install**: `xcaddy build --with github.com/gmag11/caddy-cloudflare-dns-manager` becomes the supported command; the subpackage form is removed.
- **Docs/tooling**: README build section, `testenv/Dockerfile`, `testenv/docker-compose.yml` build context, and the planning references that name `cfdnsmanager/`.
- **Systems**: no runtime behavior change; existing Caddyfiles and deployments keep working.
