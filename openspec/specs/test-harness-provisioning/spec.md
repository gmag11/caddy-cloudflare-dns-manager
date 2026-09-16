# test-harness-provisioning Specification

## Purpose

Defines how the test harness's Cloudflare tunnel is provisioned: the guard that makes the
provisioning script act only on an unconfigured tunnel and treat the template's placeholders as
configured, the refusal of a half-configured pair, the remotely-managed tunnel named from the
instance id, the account id derived from the zone rather than configured, the run token fetched
separately and never printed, reuse of an existing tunnel of the same name, and the write-back
that leaves the rest of `.env` untouched.

## Requirements

### Requirement: Provisioning is guarded by an unconfigured tunnel

The harness provisioning script SHALL create a tunnel only when both `CF_TUNNEL_TOKEN` and
`CF_TUNNEL_ID` are unconfigured, and SHALL treat a value as unconfigured only when its line is
absent from `testenv/.env` or present with an empty value.

A recognized placeholder SHALL be treated as configured, so the script does not act on a `.env`
copied from the template without first blanking those two lines. When the script detects that both
values are placeholders, it SHALL report the two lines to blank and exit successfully without
creating anything.

When both values are configured, the script SHALL report that the tunnel is already provisioned
and exit successfully without contacting the API.

#### Scenario: Both values empty

- **WHEN** the script runs with `CF_TUNNEL_TOKEN=` and `CF_TUNNEL_ID=` both present and empty
- **THEN** it proceeds to provision the tunnel and writes both values back into `.env`

#### Scenario: Both values absent

- **WHEN** the script runs with neither line present in `.env`
- **THEN** it proceeds to provision the tunnel and appends both values

#### Scenario: Tunnel already configured

- **WHEN** the script runs with a non-empty `CF_TUNNEL_TOKEN` and `CF_TUNNEL_ID`
- **THEN** it exits successfully without creating a tunnel and without writing to `.env`

#### Scenario: Template placeholders are not unconfigured

- **WHEN** the script runs against a `.env` copied verbatim from `.env.example`, carrying
  `CF_TUNNEL_TOKEN=replace-me` and the all-zero UUID
- **THEN** it creates nothing and reports which two lines must be blanked

### Requirement: A half-configured pair is refused

When exactly one of `CF_TUNNEL_TOKEN` and `CF_TUNNEL_ID` is unconfigured, the script SHALL exit
with a non-zero status, name the unconfigured variable, and create no resource.

The script SHALL NOT fill in the missing value, and SHALL NOT create a tunnel.

#### Scenario: Token set, UUID unconfigured

- **WHEN** the script runs with a non-empty `CF_TUNNEL_TOKEN` and an empty `CF_TUNNEL_ID`
- **THEN** it exits non-zero naming `CF_TUNNEL_ID` and no tunnel is created

#### Scenario: UUID set, token unconfigured

- **WHEN** the script runs with a non-empty `CF_TUNNEL_ID` and an empty `CF_TUNNEL_TOKEN`
- **THEN** it exits non-zero naming `CF_TUNNEL_TOKEN` and no tunnel is created

### Requirement: A remotely-managed tunnel named from the instance id

The script SHALL create the tunnel with `config_src` set to `cloudflare`, so its ingress rules are
stored remotely and are the ones the plugin writes.

The tunnel name SHALL be the value of `CF_INSTANCE`. The script SHALL NOT hardcode a tunnel name
and SHALL fail with a clear error when `CF_INSTANCE` is unset or empty.

#### Scenario: Remotely-managed tunnel

- **WHEN** the script creates a tunnel
- **THEN** the created tunnel reports `config_src` equal to `cloudflare`

#### Scenario: Name taken from the instance id

- **WHEN** `CF_INSTANCE` is `caddy-test` and the script creates a tunnel
- **THEN** the created tunnel's name is `caddy-test`

#### Scenario: Instance id unset

- **WHEN** `CF_INSTANCE` is unset or empty
- **THEN** the script exits non-zero with an error naming `CF_INSTANCE` and creates nothing

### Requirement: The account is derived, never configured

The script SHALL resolve the account id from the zone named by `CF_ZONE` using the zone-scoped
`CF_API_TOKEN`, and SHALL NOT require a configured account id.

The script SHALL use the account-scoped `CF_ACCOUNT_TUNNEL_TOKEN` for the Tunnel API calls, because
the zone-scoped token cannot create a tunnel.

When `CF_ZONE` is unset, or the zone lookup yields no account, the script SHALL exit non-zero
naming the missing or unresolvable value and create nothing.

#### Scenario: Account derived from the zone

- **WHEN** `CF_ZONE` names a zone the `CF_API_TOKEN` can read
- **THEN** the Tunnel API calls target that zone's `account.id`

#### Scenario: Unknown zone

- **WHEN** `CF_ZONE` names a zone the `CF_API_TOKEN` cannot resolve
- **THEN** the script exits non-zero and creates nothing

### Requirement: The run token is fetched separately and never printed

The script SHALL obtain the run token from the tunnel token endpoint, which returns the token as a
bare string, and SHALL validate that it is non-empty before writing it.

The run token SHALL NOT be written to standard output, passed as a command-line argument, or stored
in a temporary file. It SHALL be written only to `testenv/.env`, which is gitignored.

The script SHALL NOT write a partial result: both values SHALL be confirmed present in memory before
`.env` is modified.

#### Scenario: Token obtained and written

- **WHEN** provisioning succeeds
- **THEN** `CF_TUNNEL_TOKEN` in `.env` holds the token returned by the token endpoint and
  `CF_TUNNEL_ID` holds the created tunnel's UUID

#### Scenario: Token is never echoed

- **WHEN** the script runs with standard output and standard error captured
- **THEN** neither stream contains the run token value

#### Scenario: Empty token response

- **WHEN** the token endpoint returns an empty or missing value
- **THEN** the script exits non-zero without modifying `.env` and reports the tunnel UUID so the
  created resource is identifiable

### Requirement: An existing tunnel of the same name is reused

Before creating a tunnel, the script SHALL look for an existing account tunnel named `CF_INSTANCE`.
When exactly one exists, the script SHALL adopt its UUID and run token instead of creating a
tunnel. When more than one exists, the script SHALL exit non-zero and create nothing.

#### Scenario: Reuse on re-run

- **WHEN** the two `.env` values are blanked while a tunnel named `CF_INSTANCE` still exists
- **THEN** the script adopts that tunnel's UUID and token and creates no new tunnel

#### Scenario: Unique name is required

- **WHEN** two or more account tunnels are named `CF_INSTANCE`
- **THEN** the script exits non-zero naming the ambiguity and creates nothing

### Requirement: `.env` is rewritten without disturbing other content

The script SHALL replace the two values in place, or append them when their lines are absent, and
SHALL preserve every other line, comment, and blank line in the file.

#### Scenario: Unrelated values survive

- **WHEN** `.env` holds comments and other variables and the script writes the two values
- **THEN** every other line is byte-identical to before the write

### Requirement: Missing dependencies fail with an actionable message

The script SHALL verify that `curl` and `jq` are available before making any request, and SHALL
exit non-zero with an installation hint when either is missing.

#### Scenario: jq missing

- **WHEN** `jq` is not on `PATH`
- **THEN** the script exits non-zero naming `jq` and makes no API request

