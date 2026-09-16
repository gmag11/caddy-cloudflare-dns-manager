# Tasks

## 1. Script skeleton and guards

- [x] 1.1 Create `testenv/create-tunnel.sh` with `set -euo pipefail` and a header comment stating
      what it provisions and that it is harness tooling, not part of the published module.
- [x] 1.2 Resolve the script's own directory and source `testenv/.env` from there, so the script
      works from any working directory.
- [x] 1.3 Check for `curl` and `jq` on `PATH` before any request; exit non-zero with an install
      hint naming the missing tool.
- [x] 1.4 Read `CF_TUNNEL_TOKEN` and `CF_TUNNEL_ID` from the file, distinguishing absent from
      empty, and classify each as `configured | empty | absent`.
- [x] 1.5 Implement the guard: both configured and neither a placeholder → report already
      provisioned, exit 0. Both placeholders → report the two lines to blank, exit 0. Exactly one
      unconfigured → exit non-zero naming it. Both unconfigured → continue.
- [x] 1.6 Validate `CF_ZONE`, `CF_API_TOKEN`, `CF_ACCOUNT_TUNNEL_TOKEN` and `CF_INSTANCE` are
      non-empty, exiting non-zero with an error naming the first missing one.

## 2. Account derivation

- [x] 2.1 Resolve the account id with `GET /zones?name=<CF_ZONE>&per_page=1` using
      `CF_API_TOKEN`; read `.result[0].account.id`.
- [x] 2.2 Fail non-zero with a message naming `CF_ZONE` when the lookup yields no zone or no
      account id, rather than sending an empty account id to the Tunnel API.

## 3. Reuse detection

- [x] 3.1 List the account's tunnels with `GET /accounts/<id>/cfd_tunnel` using
      `CF_ACCOUNT_TUNNEL_TOKEN`; filter client-side for `name == CF_INSTANCE`.
- [x] 3.2 Exactly one match → adopt its `id` as the tunnel UUID and skip creation.
- [x] 3.3 More than one match → exit non-zero naming the ambiguity and create nothing.
- [x] 3.4 No match → proceed to creation.

## 4. Creation and token retrieval

- [x] 4.1 Create the tunnel with
      `POST /accounts/<id>/cfd_tunnel {"name":"<CF_INSTANCE>","config_src":"cloudflare"}` and read
      `.result.id` as the UUID. Fail non-zero when it is missing.
- [x] 4.2 Fetch the run token with `GET /accounts/<id>/cfd_tunnel/<uuid>/token`, reading
      `.result` as a bare string — not `.result.token`.
- [x] 4.3 Validate the token is non-empty; on failure, report the created/adopted UUID so the
      resource is identifiable and exit non-zero without touching `.env`.
- [x] 4.4 Confirm both values are present in memory before any write to `.env`.

## 5. `.env` write-back

- [x] 5.1 Replace the `CF_TUNNEL_TOKEN` and `CF_TUNNEL_ID` lines in place, preserving comments,
      ordering, blank lines and unrelated variables; append the lines when absent.
- [x] 5.2 Write atomically: build the new content in a variable and write it once, so a failure
      cannot leave `.env` half-updated.
- [x] 5.3 Keep the run token out of stdout, stderr, command-line arguments and temporary files;
      print only the tunnel UUID and name.
- [x] 5.4 Make the script executable (`chmod +x`).

## 6. Documentation

- [x] 6.1 Update `testenv/README.md` so the one-time tunnel setup is the script; keep the
      dashboard procedure as an explicit fallback.
- [x] 6.2 Update `testenv/.env.example` comments to point at the script and state the
      strict-emptiness rule: the placeholder values count as configured, so blank both lines
      before the first run.
- [x] 6.3 Note in the README that the script derives the account id from `CF_ZONE` with
      `CF_API_TOKEN`, matching the plugin, and that `CF_ACCOUNT_TUNNEL_TOKEN` does the creation.

## 7. Verification

- [x] 7.1 Guard matrix, no network: run against a `.env` with (a) both lines empty, (b) both
      absent, (c) both configured, (d) both placeholders, (e) only the token empty, (f) only the
      UUID empty. Confirm the exit status and message for each, and that (c), (d) and the
      half-configured cases make no API call.
- [x] 7.2 Live provisioning: blank both values and run the script. Confirm the tunnel exists with
      `config_src: cloudflare` and the name from `CF_INSTANCE`, and that both values are written.
- [x] 7.3 Re-run idempotence: run again unchanged and confirm it reports already provisioned and
      exits 0.
- [x] 7.4 Reuse: blank both values while the tunnel still exists and run again; confirm the same
      UUID is adopted and no second tunnel appears in the account.
- [x] 7.5 Token hygiene: confirm the token value appears in neither stdout nor stderr and that
      `.env` is the only file modified.
- [x] 7.6 End-to-end with the harness: `docker compose up`, confirm `cloudflared` connects and the
      plugin writes the ingress plan using the provisioned credentials.
