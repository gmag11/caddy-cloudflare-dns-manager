# Add a tunnel provisioning script to the test harness

## Why

The test harness needs a remotely-managed tunnel, and creating one is a manual, three-step
dashboard procedure described in `testenv/README.md`: create the tunnel, copy the run token into
`CF_TUNNEL_TOKEN`, copy the tunnel UUID into `CF_TUNNEL_ID`. Every step is a hand copy of an
opaque value from one browser tab into a file, which is exactly the kind of step that gets
fumbled: the token lands in the UUID line, or the UUID of a different tunnel is pasted, and the
failure surfaces later as an unexplained `cloudflared` connection error or a CNAME pointing at a
tunnel that does not exist.

The values the procedure needs are all already in `.env`. The account-scoped token
(`CF_ACCOUNT_TUNNEL_TOKEN`) can create a tunnel and read its run token, and `CF_INSTANCE` is
already the stable identifier the harness uses for this deployment. Nothing about the manual step
requires a human.

The same `.env` file is also the source of a trap. `.env.example` ships `CF_TUNNEL_TOKEN=replace-me`
and `CF_TUNNEL_ID=00000000-0000-0000-0000-000000000000`, so a freshly copied `.env` is not empty —
it is filled with values that look configured and are not.

## What Changes

- Add `testenv/create-tunnel.sh`: a provisioning script that creates the harness's tunnel and
  writes both credentials back into `testenv/.env`.
- The script is a **no-op guard first**: it acts only when both `CF_TUNNEL_TOKEN` and
  `CF_TUNNEL_ID` are unconfigured. A configured pair means the tunnel already exists and the
  script prints that and exits successfully, so it is safe to call unconditionally.
- **A half-configured pair is an error, not a creation.** If exactly one of the two is
  unconfigured, the script stops with a message naming which one and does not create anything.
  Creating there would leave an orphan tunnel in the account and a `.env` that still does not
  describe it.
- The tunnel is created **remotely-managed** (`config_src: cloudflare`), which is what the
  plugin requires: it writes the ingress plan through the API, and a locally-managed tunnel would
  hold its rules in a file the plugin never sees.
- The tunnel **name is `CF_INSTANCE`**, the same stable identifier used for ownership tagging, so
  the tunnel the harness owns is identifiable in the dashboard by the name the harness already
  reports.
- The **account id is derived, never configured**, matching the plugin: `GET /zones?name=<zone>`
  with the zone-scoped `CF_API_TOKEN` yields the owning account, because a tunnel and its zone
  always share an account.
- The **run token is fetched, not returned by creation**: `POST /accounts/{id}/cfd_tunnel`
  returns the tunnel's UUID but never its token, so the script makes a second call to
  `GET /accounts/{id}/cfd_tunnel/{tunnel_id}/token`.
- `testenv/README.md` and `testenv/.env.example` are updated so the documented one-time setup is
  the script rather than the dashboard procedure.
