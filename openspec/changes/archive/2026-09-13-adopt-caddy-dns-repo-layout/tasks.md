## 1. Relocate sources to the module root

- [x] 1.1 Move all Go files from `cfdnsmanager/` to the repository root with `git mv` and remove the now-empty directory; verify `git status` shows renames and `ls` shows no `cfdnsmanager/`
- [x] 1.2 Rename the package clause in every moved `.go` file from `cf_dns_manager` to `cfdnsmanager`; verify `grep -rn "package cf_dns_manager" .` returns nothing
- [x] 1.3 Confirm the module path still resolves to a root package by running `go build ./...`, `go vet ./...`, `gofmt -l .` and `go test ./...` and getting a clean, passing result

## 2. Standard repository files

- [x] 2.1 Add a root `LICENSE` with the Apache-2.0 license text (matching the reference and upstream Caddy modules)
- [x] 2.2 Add a root `Dockerfile` based on `caddy:2-builder-alpine` that builds the plugin with `xcaddy build --with github.com/gmag11/caddy-cloudflare-dns-manager=.` and uses an `alpine` runtime stage; verify it builds and the resulting `caddy list-modules` includes `cf_dns_manager`
- [x] 2.3 Add `.github/workflows/continuous-integration.yml` running `go build -v` and `go test -v` on push/PR to main with the project's Go toolchain; optionally add the reference-style GHCR build job gated to tags
- [x] 2.4 Verify the workflow YAML is valid (e.g. `actionlint` if available, or `python -c "import yaml,sys; yaml.safe_load(open(...))"`) and references the correct paths

## 3. Update repository documentation and tooling

- [x] 3.1 Update `README.md`'s build/install section to the canonical `xcaddy build --with github.com/gmag11/caddy-cloudflare-dns-manager` and remove the subpackage form
- [x] 3.2 Add a short "Repository layout" note to `README.md` describing the root-level package and standard files
- [x] 3.3 Update the git-excluded `testenv/Dockerfile` to `COPY` the root package (no `cfdnsmanager/` path) and use `--with github.com/gmag11/caddy-cloudflare-dns-manager=.`
- [x] 3.4 Update `testenv/docker-compose.yml` build context/args if any path assumptions changed, and rebuild the `testenv` image to confirm `caddy adapt` still succeeds

## 4. Verification

- [x] 4.1 Run `go test ./...` from the root and confirm all reconciliation, ownership, IP-detection and Caddyfile tests pass with no behavior change
- [x] 4.2 Build the root `Dockerfile` and confirm `caddy list-modules` lists `cf_dns_manager` and, when built with the DNS provider, `dns.providers.cloudflare`
- [x] 4.3 Adapt a Caddyfile using the global and per-site `cf_dns_manager` directives and confirm the output JSON is unchanged relative to before the move
- [x] 4.4 Run `openspec validate adopt-caddy-dns-repo-layout` and confirm the change validates
