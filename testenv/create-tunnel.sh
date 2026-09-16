#!/usr/bin/env bash
#
# Provision the test harness's Cloudflare tunnel.
#
# Creates a remotely-managed tunnel named after CF_INSTANCE and writes its run
# token and UUID back into testenv/.env. Remotely-managed is required, not
# cosmetic: the plugin writes the ingress plan through the API, and a
# locally-managed tunnel keeps its rules in a file the plugin never sees.
#
# This is harness tooling under testenv/. It is not part of the published
# module and no plugin behaviour depends on it.
#
# The script acts only when CF_TUNNEL_TOKEN and CF_TUNNEL_ID are both
# unconfigured. "Unconfigured" means the line is absent or present with an
# empty value; the placeholders in .env.example (replace-me and the all-zero
# UUID) count as configured, so a .env copied straight from the template must
# have those two lines blanked first. The script says so when it detects them.
#
# Usage:
#   testenv/create-tunnel.sh
#
# Requires: curl, jq

set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
env_file="${script_dir}/.env"
api_base="https://api.cloudflare.com/client/v4"

die() {
	printf 'error: %s\n' "$1" >&2
	exit 1
}

info() {
	printf '%s\n' "$1"
}

# --- dependencies -----------------------------------------------------------

for tool in curl jq; do
	command -v "$tool" >/dev/null 2>&1 ||
		die "$tool is required but not on PATH (install it, then re-run)"
done

[ -f "$env_file" ] ||
	die "$env_file not found; copy .env.example to .env in testenv/ first"

# --- read the two target values ---------------------------------------------

# Classify each value as configured | empty | absent. Absent and empty differ
# only in how the write-back behaves later; both count as unconfigured.
classify() {
	local key="$1"
	if ! grep -qE "^[[:space:]]*${key}=" "$env_file"; then
		printf 'absent'
		return
	fi
	local val
	val=$(grep -E "^[[:space:]]*${key}=" "$env_file" | head -n1 | cut -d= -f2-)
	# Strip surrounding whitespace and a single pair of optional quotes.
	val="${val#"${val%%[![:space:]]*}"}"
	val="${val%"${val##*[![:space:]]}"}"
	val="${val%\"}"
	val="${val#\"}"
	if [ -z "$val" ]; then
		printf 'empty'
	else
		printf 'configured'
	fi
}

value_of() {
	local key="$1"
	grep -E "^[[:space:]]*${key}=" "$env_file" | head -n1 | cut -d= -f2- |
		sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' -e 's/^"//' -e 's/"$//'
}

token_state=$(classify CF_TUNNEL_TOKEN)
uuid_state=$(classify CF_TUNNEL_ID)

# A value is a placeholder when it is the template's own sentinel. These are
# treated as configured on purpose: "looks like a placeholder" is a heuristic on
# values this script does not own, and acting on a heuristic is worse than
# asking for one explicit edit.
is_placeholder() {
	case "$1" in
	replace-me | 00000000-0000-0000-0000-000000000000) return 0 ;;
	*) return 1 ;;
	esac
}

placeholder_hint() {
	info "Both values still hold the .env.example placeholders, which count as configured."
	info "Blank these two lines in $env_file, then re-run:"
	info "  CF_TUNNEL_TOKEN="
	info "  CF_TUNNEL_ID="
}

if [ "$token_state" = configured ] && [ "$uuid_state" = configured ]; then
	token_val=$(value_of CF_TUNNEL_TOKEN)
	uuid_val=$(value_of CF_TUNNEL_ID)
	if is_placeholder "$token_val" && is_placeholder "$uuid_val"; then
		placeholder_hint
		exit 0
	fi
	info "Tunnel already provisioned (CF_TUNNEL_TOKEN and CF_TUNNEL_ID are set); nothing to do."
	exit 0
fi

# A half-configured pair is the one case where guessing is destructive: the
# configured half already belongs to some tunnel, so filling in the other would
# pair a token and an id describing different tunnels.
if [ "$token_state" = configured ] || [ "$uuid_state" = configured ]; then
	if [ "$token_state" = configured ]; then
		die "CF_TUNNEL_TOKEN is set but CF_TUNNEL_ID is not; set both or blank both (nothing was created)"
	fi
	die "CF_TUNNEL_ID is set but CF_TUNNEL_TOKEN is not; set both or blank both (nothing was created)"
fi

# --- required configuration -------------------------------------------------

# Source the file for the credentials we are about to use. Values already
# exported in the environment win, which keeps the script usable from CI.
set -a
. "$env_file"
set +a

for var in CF_ZONE CF_API_TOKEN CF_ACCOUNT_TUNNEL_TOKEN CF_INSTANCE; do
	eval "val=\${$var:-}"
	[ -n "$val" ] || die "$var is unset or empty in $env_file"
done

# --- account id, derived not configured -------------------------------------

# GET /accounts with the account-scoped token returns an empty result: Tunnel
# Write carries no account-read permission, and the endpoint answers 200 with an
# empty list rather than an error. The zone lookup is the same derivation the
# plugin uses (cloudflareClient.zoneByName), and it is exact because a
# cfargotunnel.com CNAME only proxies records in the same account.
zone_resp=$(curl -sS -H "Authorization: Bearer ${CF_API_TOKEN}" \
	"${api_base}/zones?name=${CF_ZONE}&per_page=1") ||
	die "request to the Cloudflare API failed (network or DNS)"

if [ "$(printf '%s' "$zone_resp" | jq -r '.success // false')" != "true" ]; then
	codes=$(printf '%s' "$zone_resp" | jq -rc '[.errors[]?.code] // []')
	die "zone lookup failed for CF_ZONE=${CF_ZONE} (errors: ${codes}); check CF_ZONE and CF_API_TOKEN"
fi

account_id=$(printf '%s' "$zone_resp" | jq -r '.result[0].account.id // ""')
[ -n "$account_id" ] ||
	die "CF_ZONE=${CF_ZONE} resolved to no account; check that CF_API_TOKEN can read the zone"

# --- reuse an existing tunnel of the same name ------------------------------

# Without this, blanking the two values to re-provision while the tunnel is
# still in the dashboard would create a second tunnel with a duplicate name --
# the same orphan the guards exist to prevent.
tunnels_resp=$(curl -sS -H "Authorization: Bearer ${CF_ACCOUNT_TUNNEL_TOKEN}" \
	"${api_base}/accounts/${account_id}/cfd_tunnel") ||
	die "request to the Tunnel API failed (network or DNS)"

[ "$(printf '%s' "$tunnels_resp" | jq -r '.success // false')" = "true" ] ||
	die "listing tunnels failed; check CF_ACCOUNT_TUNNEL_TOKEN (Account -> Cloudflare Tunnel -> Edit)"

match_count=$(printf '%s' "$tunnels_resp" |
	jq --arg name "$CF_INSTANCE" '[.result[]? | select(.name == $name)] | length')

tunnel_id=""
if [ "$match_count" -gt 1 ]; then
	die "${match_count} tunnels in the account are named '${CF_INSTANCE}'; rename or delete the extra ones (nothing was created)"
elif [ "$match_count" -eq 1 ]; then
	tunnel_id=$(printf '%s' "$tunnels_resp" |
		jq -r --arg name "$CF_INSTANCE" '.result[] | select(.name == $name) | .id' | head -n1)
	info "Reusing existing tunnel '${CF_INSTANCE}' (${tunnel_id})."
else
	create_resp=$(curl -sS -X POST \
		-H "Authorization: Bearer ${CF_ACCOUNT_TUNNEL_TOKEN}" \
		-H "Content-Type: application/json" \
		-d "{\"name\":$(printf '%s' "$CF_INSTANCE" | jq -Rs .),\"config_src\":\"cloudflare\"}" \
		"${api_base}/accounts/${account_id}/cfd_tunnel") ||
		die "tunnel creation request failed (network or DNS)"

	[ "$(printf '%s' "$create_resp" | jq -r '.success // false')" = "true" ] ||
		die "tunnel creation failed; check CF_ACCOUNT_TUNNEL_TOKEN and that '${CF_INSTANCE}' is a valid name"

	tunnel_id=$(printf '%s' "$create_resp" | jq -r '.result.id // ""')
	[ -n "$tunnel_id" ] || die "tunnel was created but the API returned no UUID"
	info "Created tunnel '${CF_INSTANCE}' (${tunnel_id})."
fi

# --- run token: fetched separately, never printed ---------------------------

# POST .../cfd_tunnel returns the UUID but never the token, so this second call
# is required. Its result is a bare JSON string, not an object.
token_resp=$(curl -sS -H "Authorization: Bearer ${CF_ACCOUNT_TUNNEL_TOKEN}" \
	"${api_base}/accounts/${account_id}/cfd_tunnel/${tunnel_id}/token") ||
	die "fetching the tunnel token failed (network or DNS); tunnel ${tunnel_id} exists, re-run to adopt it"

run_token=$(printf '%s' "$token_resp" | jq -r '.result // ""')
if [ -z "$run_token" ] || [ "$run_token" = "null" ]; then
	die "the API returned no run token; tunnel ${tunnel_id} exists, re-run to adopt it"
fi

# --- write-back -------------------------------------------------------------

# Both values are confirmed present in memory before .env is touched, so the
# file is never left half-updated. The token goes only to the gitignored .env:
# not to stdout, not as an argument (where it would land in ps and history), not
# to a temp file.
new_content=$(TOKEN="$run_token" UUID="$tunnel_id" awk '
	BEGIN { token_done = 0; uuid_done = 0 }
	/^[[:space:]]*CF_TUNNEL_TOKEN=/ { print "CF_TUNNEL_TOKEN=" ENVIRON["TOKEN"]; token_done = 1; next }
	/^[[:space:]]*CF_TUNNEL_ID=/    { print "CF_TUNNEL_ID=" ENVIRON["UUID"];  uuid_done = 1;  next }
	{ print }
	END {
		if (!token_done) print "CF_TUNNEL_TOKEN=" ENVIRON["TOKEN"]
		if (!uuid_done)  print "CF_TUNNEL_ID=" ENVIRON["UUID"]
	}
' "$env_file")

printf '%s\n' "$new_content" > "$env_file"

info "Wrote CF_TUNNEL_TOKEN and CF_TUNNEL_ID to $env_file"
info "Next: docker compose -f testenv/docker-compose.yml up -d"
