# repository-layout Specification

## Purpose

Defines the standard, tooling-visible shape of this repository: a module path resolving to a root-level Go package, canonical xcaddy installability, the standard root files (LICENSE, Dockerfile, CI), and the constraint that Caddy module IDs and the Caddyfile surface are unaffected by the layout.

## Requirements

### Requirement: Module root resolves to the plugin package

The repository SHALL declare its Go module at the repository root and SHALL define the plugin's Go package directly in that root directory, so that the module path resolves to an importable package. No subdirectory SHALL be required to import the plugin.

#### Scenario: Root-level package build

- **WHEN** `go build ./...` is run from the repository root
- **THEN** it compiles successfully using the package in the root directory, and no Go source files remain under `cfdnsmanager/`

#### Scenario: Package name matches the module directory

- **WHEN** the Go sources are inspected
- **THEN** all plugin files share one idiomatic package name matching the root directory, and the previous `cf_dns_manager` package identifier is no longer used

### Requirement: Canonical xcaddy installation

The plugin SHALL be installable with the canonical single-module xcaddy command that names only the module path, without a subpackage suffix.

#### Scenario: Install without subdirectory

- **WHEN** a user runs `xcaddy build --with github.com/gmag11/caddy-cloudflare-dns-manager`
- **THEN** the build resolves the module at its root, compiles it into the Caddy binary, and the resulting binary lists the `cf_dns_manager` module

#### Scenario: Subpackage path no longer required

- **WHEN** documentation and tooling reference the plugin for a build
- **THEN** they reference `github.com/gmag11/caddy-cloudflare-dns-manager` and do not reference a `/cfdnsmanager` import path

### Requirement: Standard repository files

The repository SHALL include the standard files expected of a Caddy module, consistent with the reference layout: a root `LICENSE`, a root `Dockerfile` that builds a Caddy binary with the module baked in, and a CI workflow that builds and tests the module. The CI workflow SHALL run only on tag pushes, published releases, and pull requests targeting the main branch, and SHALL NOT run on ordinary branch pushes.

#### Scenario: Root license present

- **WHEN** the repository root is listed
- **THEN** a `LICENSE` file exists containing the Apache-2.0 license text

#### Scenario: Root Dockerfile builds the module

- **WHEN** the root `Dockerfile` is built
- **THEN** it produces a Caddy image that includes the plugin built from the repository's own source via a module replacement referring to the current directory

#### Scenario: CI builds and tests on tag, release, or pull request

- **WHEN** a tag is pushed, a release is published, or a pull request targets the main branch
- **THEN** a GitHub Actions workflow runs `go build` and `go test` for the module and reports success or failure

#### Scenario: CI does not run on branch pushes

- **WHEN** a commit is pushed to a branch without a tag
- **THEN** the CI workflow does not run

### Requirement: Caddy module IDs and Caddyfile surface unchanged

Adopting the new layout SHALL NOT change any Caddy module ID or Caddyfile syntax. The module IDs `cf_dns_manager` and `http.handlers.cf_dns_manager_host`, the global `cf_dns_manager` block, and the per-site `cf_dns_manager` directive SHALL keep their existing names and behavior.

#### Scenario: Module IDs stable after relocation

- **WHEN** a Caddy binary built from the relocated sources is inspected with `caddy list-modules`
- **THEN** it lists `cf_dns_manager` exactly as before the change

#### Scenario: Existing Caddyfile still adapts

- **WHEN** a Caddyfile using the global `cf_dns_manager` block and per-site `cf_dns_manager` directives is adapted after the change
- **THEN** it adapts successfully with the same resulting configuration as before the relocation
