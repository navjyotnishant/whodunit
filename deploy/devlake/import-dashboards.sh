#!/bin/sh
# Author: Navjyot Nishant
# Created: 2026-08-13
# Last updated: 2026-08-13
# Description: Import the whodunit dashboards into any Grafana.
#
# Step 2 of the datalake setup, and the only part that runs more than once:
# dashboards change when whodunit ships a release, not when the stack is
# built. Re-run this after upgrading and nothing else is touched.
#
#   curl -fsSL https://raw.githubusercontent.com/navjyotnishant/whodunit/main/deploy/devlake/import-dashboards.sh | sh
#
#   sh import-dashboards.sh --grafana https://devlake.internal:3002 --user admin
#
# Needs curl and a reachable Grafana. No Docker, no jq, no python, no dun —
# so it works against a Grafana someone else runs, on a machine that has
# never heard of this project.

set -eu

# Wrapped in main() and called at the very bottom, so a download truncated
# mid-flight cannot execute half a script. Piping to sh is convenient and
# this is the price of it.
main() {
	GRAFANA="${GRAFANA_URL:-http://localhost:3002}"
	GRAFANA_USER="${GRAFANA_USER:-admin}"
	PASSWORD="${GRAFANA_PASSWORD:-}"

	# Discovered rather than assumed. DevLake's entrypoint tries to create a
	# datasource with the fixed uid "devlake-mysql-api", but authenticates as
	# admin:$GF_SECURITY_ADMIN_PASSWORD — which upstream's own compose file
	# never sets, so every call 401s and the datasource is never created:
	#
	#   Deleting old MySQL datasources...
	#   ... POST /api/datasources status=401 error="no password provided"
	#
	# Binding to that uid would therefore fail on a stock install. Empty
	# means "find the mysql datasource yourself"; --datasource pins one.
	DATASOURCE_UID="${DATASOURCE_UID:-}"

	# The folder these land in.
	#
	# Not "General", which is where an unfoldered import goes and where
	# DevLake's own dashboards already live. The whole reason every table
	# is prefixed whodunit_ is that this tool coexists with a stack someone
	# else runs; the dashboard list deserves the same courtesy.
	#
	# A folder is also a permissions boundary in Grafana, so this makes it
	# possible to grant someone the whodunit dashboards without the rest of
	# the instance.
	FOLDER="${WHODUNIT_FOLDER:-Whodunit}"

	# Raw from main by default. A tagged release also ships the dashboards
	# as an asset; set WHODUNIT_VERSION to pin to it.
	VERSION="${WHODUNIT_VERSION:-}"
	REPO="${WHODUNIT_REPO:-navjyotnishant/whodunit}"

	while [ $# -gt 0 ]; do
		case "$1" in
		--grafana) GRAFANA="$2"; shift 2 ;;
		--user) GRAFANA_USER="$2"; shift 2 ;;
		--password) PASSWORD="$2"; shift 2 ;;
		--datasource) DATASOURCE_UID="$2"; shift 2 ;;
		--folder) FOLDER="$2"; shift 2 ;;
		--version) VERSION="$2"; shift 2 ;;
		-h | --help) usage; return 0 ;;
		*) echo "unknown option: $1" >&2; usage >&2; return 2 ;;
		esac
	done

	command -v curl >/dev/null 2>&1 || die "curl is required"

	# Asked for rather than taken from the command line when possible: an
	# argument is visible in `ps` to every user on the machine, and lands in
	# shell history. --password stays available for unattended runs.
	if [ -z "$PASSWORD" ]; then
		if [ -t 0 ]; then
			printf 'Grafana password for %s: ' "$GRAFANA_USER" >&2
			stty -echo 2>/dev/null || true
			read -r PASSWORD
			stty echo 2>/dev/null || true
			printf '\n' >&2
		else
			die "no password: pass --password, or set GRAFANA_PASSWORD"
		fi
	fi

	AUTH="$GRAFANA_USER:$PASSWORD"
	GRAFANA="${GRAFANA%/}"

	# One scratch directory for everything, removed on any exit — including
	# the failure paths below, which carry response bodies that may quote
	# the request.
	WORK="$(mktemp -d)"
	WORK_API="$WORK/response"
	trap 'rm -rf "$WORK"' EXIT INT TERM

	check_grafana
	check_datasource
	ensure_folder

	imported=0
	failed=0
	# The list is here rather than discovered because this script is
	# curl-piped: there is no checkout to glob, only whatever the fetch
	# step pulls down.
	#
	# It is therefore the one place a new dashboard has to be registered by
	# hand, and forgetting is silent — the file is generated, exported and
	# committed, CI passes because the two copies agree, and the dashboard
	# simply never appears in Grafana. That happened to whodunit-cost.
	# check-dashboard-list.py fails the build when this list and
	# dashboards/ disagree.
	for name in whodunit whodunit-adoption whodunit-cost whodunit-dora \
		whodunit-exec whodunit-hours whodunit-funnel whodunit-teams whodunit-mcp whodunit-productivity whodunit-board whodunit-leadership; do
		if import_one "$name"; then
			imported=$((imported + 1))
		else
			failed=$((failed + 1))
		fi
	done

	echo
	if [ "$failed" -gt 0 ]; then
		echo "imported $imported dashboard(s), $failed failed" >&2
		return 1
	fi
	echo "imported $imported dashboard(s)"
	echo
	echo "  $GRAFANA/dashboards?tag=whodunit"
	echo
	echo "Empty panels mean no data has been published yet, not a failed"
	echo "import. On a developer machine:  dun config datalake && dun sync"
}

usage() {
	cat <<'EOF'
Import the whodunit dashboards into a Grafana instance.

  --grafana URL     Grafana base url (default http://localhost:3002)
  --folder NAME     folder to import into (default Whodunit)
  --user NAME       Grafana user (default admin)
  --password PASS   prompted for when not given; GRAFANA_PASSWORD also works
  --datasource UID  datasource to bind (default: find the mysql one)
  --version TAG     whodunit release to take dashboards from (default: main)

Re-running is safe: dashboards are replaced in place, keeping their urls.
EOF
}

die() {
	echo "error: $*" >&2
	exit 1
}

# api METHOD PATH [BODY-FILE] — prints the response body, returns the status
# in HTTP_STATUS. Separated so failures can report what Grafana actually
# said; a bare "import failed" sends people to the wrong place.
#
# Credentials go in through --config on stdin rather than -u on the command
# line. An argument is visible to every user on the host through ps: macOS
# happens to blank curl's -u, but Linux procfs does not, and the server this
# is aimed at is Linux.
api() {
	_method="$1"
	_path="$2"
	_body="${3:-}"
	_out="$WORK_API"

	if [ -n "$_body" ]; then
		HTTP_STATUS=$(curl_config |
			curl -sS -o "$_out" -w '%{http_code}' --config - \
				-X "$_method" \
				-H 'Content-Type: application/json' \
				--data-binary "@$_body" \
				"$GRAFANA$_path")
	else
		HTTP_STATUS=$(curl_config |
			curl -sS -o "$_out" -w '%{http_code}' --config - \
				-X "$_method" "$GRAFANA$_path")
	fi
	cat "$_out"
}

# The credentials as a curl config stanza, with the two characters curl
# treats specially inside a quoted value escaped.
#
# Verified rather than assumed: an unescaped " truncates the password at that
# point and an unescaped \ is swallowed, so a Grafana password containing
# either authenticates as something else and returns a 401 nobody can
# explain.
curl_config() {
	printf 'user = "%s"\n' "$(
		printf '%s' "$AUTH" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'
	)"
}

# Reachability and credentials in one call, against an endpoint that
# actually requires authentication.
#
# /api/health does not: it answers 200 to anyone, so a wrong password sailed
# past this check and surfaced as "datasource not found" one step later —
# pointing at the wrong problem entirely. /api/user is the cheapest endpoint
# that 401s.
check_grafana() {
	printf 'checking %s ... ' "$GRAFANA"
	if ! api GET /api/user >/dev/null 2>&1; then
		echo "unreachable"
		die "cannot reach Grafana at $GRAFANA"
	fi
	case "$HTTP_STATUS" in
	200) echo "ok" ;;
	401 | 403)
		echo "unauthorized"
		die "Grafana rejected user '$GRAFANA_USER' — check the password"
		;;
	000)
		echo "unreachable"
		die "cannot reach Grafana at $GRAFANA"
		;;
	*)
		echo "http $HTTP_STATUS"
		die "unexpected response from $GRAFANA"
		;;
	esac
}

# Resolved before importing rather than after. A dashboard bound to a missing
# datasource imports perfectly happily and then draws nothing, which is the
# hardest failure to diagnose from the other end.
check_datasource() {
	if [ -n "$DATASOURCE_UID" ]; then
		printf 'checking datasource %s ... ' "$DATASOURCE_UID"
		api GET "/api/datasources/uid/$DATASOURCE_UID" >/dev/null 2>&1 || true
		if [ "$HTTP_STATUS" = "200" ]; then
			echo "ok"
			return 0
		fi
		echo "not found"
		die "no datasource with uid '$DATASOURCE_UID' on this Grafana
	   see $GRAFANA/connections/datasources"
	fi

	printf 'finding the mysql datasource ... '
	DATASOURCE_UID="$(api GET /api/datasources | find_mysql_uid)"
	if [ -n "$DATASOURCE_UID" ]; then
		echo "$DATASOURCE_UID"
		return 0
	fi

	echo "none"
	echo >&2
	echo "This Grafana has no MySQL datasource pointing at the lake database." >&2
	echo >&2
	echo "DevLake's Grafana is supposed to create one at startup, but it" >&2
	echo "authenticates with a password its own compose file does not set," >&2
	echo "so the call fails silently (401) and no datasource appears." >&2
	echo >&2
	echo "Add one at $GRAFANA/connections/datasources/new :" >&2
	echo >&2
	echo "  type      MySQL" >&2
	echo "  host      mysql:3306        (the compose service name)" >&2
	echo "  database  lake" >&2
	echo "  user      merico            password  merico" >&2
	echo >&2
	echo "then re-run this script." >&2
	exit 1
}

# Pull the uid of the mysql datasource out of the API's JSON.
#
# Without jq: the response is one line, so split it into one datasource per
# line on the `},{` between records and read the uid off those typed mysql.
#
# Splitting on every `}` looks equivalent and is not — datasources contain
# nested objects (jsonData, secureJsonFields), so a bare `}` lands mid-record
# and the fields of one datasource end up attributed to another. That bug is
# invisible with a single datasource and picks the wrong one as soon as there
# are two.
#
# Prefers the one named "mysql" when there is a choice: every stock DevLake
# dashboard binds to that name, so it is the one pointing at the lake
# database. Picking whichever came first would be a coin flip on an instance
# that also has, say, a staging database attached.
find_mysql_uid() {
	_all="$(sed 's/},{/}\n{/g' | grep '"type":"mysql"')"
	_named="$(printf '%s\n' "$_all" | grep '"name":"mysql"' |
		sed -n 's/.*"uid":"\([^"]*\)".*/\1/p' | head -1)"
	if [ -n "$_named" ]; then
		printf '%s' "$_named"
		return 0
	fi
	printf '%s\n' "$_all" |
		sed -n 's/.*"uid":"\([^"]*\)".*/\1/p' |
		head -1
}

# fetch_dashboard NAME FILE
fetch_dashboard() {
	_name="$1"
	_dest="$2"
	if [ -n "$VERSION" ]; then
		_url="https://raw.githubusercontent.com/$REPO/$VERSION/deploy/devlake/dashboards-import/$_name.json"
	else
		_url="https://raw.githubusercontent.com/$REPO/main/deploy/devlake/dashboards-import/$_name.json"
	fi

	# A local checkout wins over the network, so this script tests the files
	# you are actually editing rather than whatever main happens to hold.
	_local="$(dirname "$0")/dashboards-import/$_name.json"
	if [ -f "$_local" ]; then
		cp "$_local" "$_dest"
		return 0
	fi
	curl -fsSL -o "$_dest" "$_url"
}

# ensure_folder creates the destination folder and leaves FOLDER_UID set.
#
# Idempotent by design, because this script re-runs on every release: a
# 409 means the folder already exists, which is the desired end state
# rather than a failure. The same shape as the schema migrations, where
# "already exists" is what success looks like on run two.
#
# A failure here is not fatal. The dashboards are more useful in the wrong
# folder than not imported at all, so FOLDER_UID stays empty and the
# import falls back to General with a warning.
ensure_folder() {
	FOLDER_UID=""
	[ -n "$FOLDER" ] || return 0

	printf 'folder %s ... ' "$FOLDER"

	# The uid is derived from the name rather than random, so a re-run
	# finds the same folder instead of creating a second one with the same
	# title — Grafana allows duplicate titles.
	#
	# Suffixed, because folders and dashboards share one uid namespace and
	# the bare slug of the default name collides with the dashboard whose
	# uid is already "whodunit". Grafana answers that collision with
	# "Dashboard cannot be changed to a folder", which is a 400 rather than
	# the 409 an existing folder returns.
	_uid=$(printf '%s' "$FOLDER" | tr '[:upper:] ' '[:lower:]-' |
		tr -cd 'a-z0-9-' | cut -c1-32)
	_uid="${_uid}-folder"

	_body="$WORK/folder.json"
	# Assembled by hand for the same reason the import envelope is: this
	# script's only hard dependency is curl. No jq, no python, so it runs
	# on a bare server.
	printf '{"uid":"%s","title":"%s"}' "$_uid" "$FOLDER" >"$_body"

	api POST /api/folders "$_body" >/dev/null 2>&1
	case "$HTTP_STATUS" in
	# 200 created it. 409 and 412 both mean it is already there — Grafana
	# answers a duplicate uid with 412 Precondition Failed and a duplicate
	# title with 409, and this script re-runs on every release, so both are
	# the desired end state rather than a failure.
	200 | 409 | 412)
		FOLDER_UID="$_uid"
		echo "ok"
		;;
	*)
		echo "could not create (HTTP $HTTP_STATUS) — importing into General"
		;;
	esac
}

import_one() {
	_name="$1"
	_file="$WORK/$_name.json"

	printf '  %-22s ' "$_name"
	if ! fetch_dashboard "$_name" "$_file"; then
		echo "could not fetch"
		return 1
	fi

	# The import envelope, assembled without a JSON tool: the dashboard is
	# inserted as a value, never parsed, so this is concatenation rather
	# than JSON manipulation. That keeps the script's only hard dependency
	# curl — no jq, no python — which matters on a bare server.
	_payload="$WORK/$_name.payload.json"
	{
		printf '{"dashboard":'
		cat "$_file"
		printf ',"overwrite":true'
		# folderUid is omitted entirely rather than sent empty: an empty
		# string is a valid uid to Grafana and would fail the lookup,
		# where an absent field means General.
		[ -n "$FOLDER_UID" ] && printf ',"folderUid":"%s"' "$FOLDER_UID"
		printf ',"inputs":[{"name":"DS_WHODUNIT",'
		printf '"type":"datasource","pluginId":"mysql","value":"%s"}]}' \
			"$DATASOURCE_UID"
	} >"$_payload"

	_response=$(api POST /api/dashboards/import "$_payload")

	# 412 alongside "imported":true is Grafana moving an existing dashboard
	# into a different folder. It reports a precondition failure on the
	# version check and performs the move anyway, so treating it as an
	# error would make every run after the first look like seven failures
	# while the dashboards were in fact updated.
	#
	# The response body is the authority, not the status.
	if [ "$HTTP_STATUS" = "200" ]; then
		echo "ok"
		return 0
	fi
	if [ "$HTTP_STATUS" = "412" ] && echo "$_response" | grep -q '"imported":true'; then
		echo "ok (moved)"
		return 0
	fi
	echo "failed (http $HTTP_STATUS)"
	echo "      $_response" >&2
	return 1
}

main "$@"
