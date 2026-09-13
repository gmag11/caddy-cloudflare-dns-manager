## Context

The plugin is a Go module declared at the repository root (`module github.com/gmag11/caddy-cloudflare-dns-manager`, `go 1.25.1`), but all Go sources live in `cfdnsmanager/` with `package cf_dns_manager`. The reference provider `caddy-dns/cloudflare` keeps its package at the repository root, uses `go.mod` there, and ships a root `Dockerfile` built with `caddy:2-builder-alpine` plus `xcaddy build --with <module>=.`. Because Go requires the module path to correspond to a package at the module root, our current layout forces consumers to import the subpackage path, which is non-standard and breaks the canonical install command.

This change is a mechanical relocation plus the addition of standard repo files. The Caddy HTTP/middleware surface must remain identical: module IDs, directive names, and reconciliation behavior are not touched.

## Goals / Non-Goals

**Goals:**

- Make `github.com/gmag11/caddy-cloudflare-dns-manager` resolve to a package at the repository root so the canonical `xcaddy build --with github.com/gmag11/caddy-cloudflare-dns-manager` works.
- Align the repository with the `caddy-dns/cloudflare` layout: root package, root `go.mod`, root `Dockerfile`, root `LICENSE`, and a CI workflow.
- Keep Caddy module IDs and the Caddyfile surface byte-for-byte compatible.
- Update every internal reference to the old `cfdnsmanager/` path (README, `testenv/` harness, planning docs).

**Non-Goals:**

- No changes to reconciliation, ownership, prune, public-IP detection, or Caddyfile parsing behavior.
- No change to the module path itself (`github.com/gmag11/caddy-cloudflare-dns-manager` stays).
- Not adopting the `caddy:2-builder-alpine` base for the local `testenv/` harness (that harness already pins a Caddy version and builds locally; only its `--with` path changes).
- Not publishing the image to a registry as part of this change (the CI workflow builds; pushing is out of scope unless desired later).

## Decisions

**Move sources to the root rather than keeping a subpackage.** Go modules are rooted at `go.mod`, and a module path only resolves to a root package. Keeping `cfdnsmanager/` forces the `/cfdnsmanager` suffix. The reference repo and other Caddy DNS providers use root packages. Chosen: `git mv cfdnsmanager/*.go .`.

**Rename package `cf_dns_manager` → `cfdnsmanager`.** Idiomatic Go package names are lowercase, no underscores, and match the directory. The directory becomes the repo root, so a short lowercase name is right. Crucially, the Caddy module IDs are string literals (`AppID = "cf_dns_manager"`, `"http.handlers.cf_dns_manager_host"`, `RegisterGlobalOption("cf_dns_manager", …)`) and do not depend on the Go identifier, so renaming is invisible to Caddy. Alternative considered: keep `package cf_dns_manager` at root — legal but non-idiomatic and confusing given the directory name; rejected.

**Root `Dockerfile` modeled on the reference.** Use `caddy:2-builder-alpine` as the builder, `xcaddy build --with github.com/gmag11/caddy-cloudflare-dns-manager=.` (the `=.` local replacement is the standard trick used by `caddy-dns/cloudflare`), and an `alpine` runtime stage with `ca-certificates`, `libcap`, `mailcap`, and a `setcap` on the binary. This differs from the local `testenv/Dockerfile`, which pins `CADDY_VERSION`/`XCADDY_VERSION` and asserts module registration; the two serve different purposes (publishable image vs local test harness). Alternative considered: fold `testenv/` into the root Dockerfile — rejected because `testenv/` is a local, git-excluded harness.

**CI workflow mirrors the reference.** A `.github/workflows/continuous-integration.yml` running `go build -v` and `go test -v` on Go matching the toolchain, on push/PR to main. Optionally a second job building via `docker/build-push-action` with the root Dockerfile; pushing gated to tags, exactly like the reference. This keeps us aligned without inventing a bespoke pipeline.

**`LICENSE` = Apache-2.0.** The reference and upstream Caddy modules are Apache-2.0; adopting it matches the ecosystem.

**README build command updated to canonical form.** `xcaddy build --with github.com/gmag11/caddy-cloudflare-dns-manager`, plus a short "Repository layout" note.

## Risks / Trade-offs

- **Build-time import path break for existing consumers** → This is the intended breaking change; documented in the proposal and README. Module IDs and Caddyfile stay stable, so deployed Caddyfiles keep working; only re-builds that named the subpackage must drop the suffix.
- **`git mv` on a case-sensitive/Linux FS is fine, but the package rename touches every file** → Mechanical and compiler-verified: `go build ./...`, `go vet ./...`, `go test ./...` must pass after the move.
- **`testenv/Dockerfile` build cache and context assumptions** → Update `COPY cfdnsmanager ./cfdnsmanager` to copy the root package and change `--with …/cfdnsmanager` to `--with …=.`; verify by building the image. `testenv/` stays git-excluded.
- **CI base image/pinning drift** → Use the same `caddy:2-builder-alpine` and versions as the reference to minimize surprises; the workflow is non-blocking for local dev.
- **`doc.go` package comment and `package` clauses across test files** → All 15 files must change their package clause; missing one fails the build, which is a strong safety net.
- **`openspec/changes/add-cloudflare-dns-manager-plugin/tasks.md` references `cfdnsmanager/` in some task text** → Historical tasks are records of past work; leave the archived/closed change's tasks as-is and only update forward-looking docs (README, testenv, this change's artifacts). Rationale: rewriting history in a completed change would misrepresent what was done.

## Migration Plan

1. `git mv cfdnsmanager/*.go .` and rename the package clause in every moved file to `cfdnsmanager`.
2. Update any intra-repo import paths (there should be none beyond test files in the same package) and confirm `go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l` are clean.
3. Add root `LICENSE` (Apache-2.0), root `Dockerfile`, and `.github/workflows/continuous-integration.yml`.
4. Update `README.md` (build command + layout section) and `testenv/Dockerfile` / `testenv/docker-compose.yml` to reference the module root.
5. Build the root `Dockerfile` and the `testenv` image; confirm `caddy list-modules` shows `cf_dns_manager`.
6. Rollback: `git revert`/revert the branch; because module IDs and Caddyfiles are unchanged, reverting has no runtime impact.

## Open Questions

- Should the CI workflow's second job actually push the image to GHCR on tags, or only build it? The reference pushes; we can mirror it or keep build-only for now. Default assumption: mirror the reference (push gated to tags).
- Should the local `testenv/` harness remain separate, or be replaced by the root `Dockerfile` once it exists? Default assumption: keep `testenv/` (local-only, git-excluded) and keep the root `Dockerfile` as the publishable artifact.
