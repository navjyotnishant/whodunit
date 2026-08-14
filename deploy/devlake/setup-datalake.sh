#!/bin/sh
# Author: Navjyot Nishant
# Created: 2026-08-13
# Last updated: 2026-08-13
# Description: Stand up an Apache DevLake stack for whodunit to publish into.
#
# Step 1 of the datalake setup — run once, on whatever machine holds the
# shared database. Step 2 (import-dashboards.sh) puts the dashboards on it,
# and is offered at the end of this script.
#
#   curl -fsSL https://raw.githubusercontent.com/navjyotnishant/whodunit/main/deploy/devlake/setup-datalake.sh | sh
#
# Needs docker and curl. Does NOT need dun: this is infrastructure, set up by
# whoever owns the server, and the developers who publish into it install the
# CLI separately.
#
# DevLake itself is fetched from upstream rather than vendored here. Our copy
# differed from theirs by nine lines of comment, which is not a fork worth
# maintaining — and a stale copy pinned to an old release is worse than no
# copy at all.

set -eu

# The DevLake release this is tested against. Upstream ships the compose file
# only as a release asset — it is not in their git tree, so there is no raw
# URL for it.
DEVLAKE_VERSION="${DEVLAKE_VERSION:-v1.0.3-beta15}"
DEVLAKE_REPO="${DEVLAKE_REPO:-apache/devlake}"

# Wrapped in main() and called at the bottom, so a download truncated
# mid-flight cannot execute half a script.
main() {
	ASSUME_YES=0
	SKIP_DASHBOARDS=0

	while [ $# -gt 0 ]; do
		case "$1" in
		-y | --yes) ASSUME_YES=1; shift ;;
		--no-dashboards) SKIP_DASHBOARDS=1; shift ;;
		--version) DEVLAKE_VERSION="$2"; shift 2 ;;
		-h | --help) usage; return 0 ;;
		*) echo "unknown option: $1" >&2; usage >&2; return 2 ;;
		esac
	done

	command -v docker >/dev/null 2>&1 || die "docker is not on PATH"
	command -v curl >/dev/null 2>&1 || die "curl is not on PATH"
	docker compose version >/dev/null 2>&1 ||
		die "docker compose is not available (needs Docker Compose v2)"

	# Run from wherever it was invoked when piped from curl, and from its own
	# directory when run from a checkout — so a clone keeps its .env and
	# volumes where they already are.
	if [ -n "${0##*sh}" ] && [ -f "$0" ]; then
		cd "$(dirname "$0")"
	fi

	announce
	confirm

	fetch_upstream
	ensure_env
	start_stack
	wait_for_mysql

	if [ "$SKIP_DASHBOARDS" -eq 0 ]; then
		wait_for_grafana
		ensure_datasource
		offer_dashboards
	fi

	summary
}

usage() {
	cat <<'EOF'
Stand up an Apache DevLake stack for whodunit to publish into.

  -y, --yes           do not pause for confirmation
      --no-dashboards skip step 2 (import them later)
      --version TAG   DevLake release to use (default v1.0.3-beta15)

Files are written into the current directory: docker-compose.yml and
env.example are fetched from upstream, .env is generated once.
EOF
}

die() {
	echo "error: $*" >&2
	exit 1
}

announce() {
	cat <<EOF
This will, in the current directory ($(pwd)):

  1. fetch docker-compose.yml and env.example from $DEVLAKE_REPO $DEVLAKE_VERSION
  2. generate .env with a fresh ENCRYPTION_SECRET (kept if one exists)
  3. start four containers: mysql, devlake, config-ui, grafana
  4. import the whodunit dashboards into grafana

Ports used: 3306 (mysql), 3002 (grafana), 4000 (config UI), 8080 (api).

EOF
}

# Only when someone is watching. Piped into sh from curl there is no TTY, and
# a prompt that cannot be answered would hang a server install forever.
confirm() {
	[ "$ASSUME_YES" -eq 1 ] && return 0
	[ -t 0 ] || return 0
	printf 'continue? [Y/n] '
	read -r reply
	case "$reply" in
	"" | y | Y | yes | YES) return 0 ;;
	*) echo "nothing was changed."; exit 0 ;;
	esac
}

# fetch_one FILENAME — from the release assets, which is the only place
# upstream publishes the compose file.
fetch_one() {
	_name="$1"
	if [ -f "$_name" ]; then
		echo "  $_name already here, keeping it"
		return 0
	fi
	_url="https://github.com/$DEVLAKE_REPO/releases/download/$DEVLAKE_VERSION/$_name"
	printf '  fetching %s ... ' "$_name"
	if curl -fsSL -o "$_name.partial" "$_url"; then
		mv "$_name.partial" "$_name"
		echo "ok"
	else
		rm -f "$_name.partial"
		echo "failed"
		die "could not fetch $_url
	   check that $DEVLAKE_VERSION is a real DevLake release:
	   https://github.com/$DEVLAKE_REPO/releases"
	fi
}

fetch_upstream() {
	echo "DevLake $DEVLAKE_VERSION"
	fetch_one docker-compose.yml
	fetch_one env.example
}

ensure_env() {
	[ -f .env ] && return 0

	echo "  creating .env"
	cp env.example .env

	# DevLake refuses to start without an encryption secret, and generating
	# one per deployment is the point — a shared default would be worse than
	# none.
	secret="$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 128)"
	# BSD and GNU sed disagree about -i, so rewrite via a temp file instead.
	awk -v s="$secret" '/^ENCRYPTION_SECRET=/ {print "ENCRYPTION_SECRET=" s; next} {print}' \
		.env >.env.tmp && mv .env.tmp .env
	chmod 600 .env
	echo "  generated ENCRYPTION_SECRET"
}

start_stack() {
	echo
	echo "starting containers"
	docker compose up -d
}

# Waited for rather than assumed. First run migrates the schema, which takes
# long enough that anything touching the database immediately after
# `compose up` fails against a server that is not listening yet.
wait_for_mysql() {
	printf 'waiting for mysql '
	_i=0
	_user="$(db_field user)"
	_pw="$(db_field password)"
	while [ "$_i" -lt 90 ]; do
		if docker compose exec -T mysql \
			mysqladmin ping -u"$_user" -p"$_pw" --silent >/dev/null 2>&1; then
			echo " ok"
			return 0
		fi
		printf '.'
		sleep 2
		_i=$((_i + 1))
	done
	echo
	die "mysql did not come up within 3 minutes — check: docker compose logs mysql"
}

# Waits for Grafana to answer an authenticated write, not merely to be up.
#
# /api/health returns 200 well before the API will accept one: on a clean
# machine the datasource POST came back 500 while health was already green,
# because Grafana was still initialising. Polling the endpoint we actually
# need means the readiness check tests the thing it is standing in for.
#
# It also outlasts DevLake's own entrypoint, which deletes any datasource
# named "mysql" and recreates it a few seconds after Grafana starts. Racing
# that is how a datasource created here vanishes moments later.
wait_for_grafana() {
	printf 'waiting for grafana '
	_pass="$(grafana_password)"
	_i=0
	while [ "$_i" -lt 90 ]; do
		if grafana_auth "$_pass" | curl -fsS --config - -o /dev/null \
			http://localhost:3002/api/datasources 2>/dev/null; then
			echo " ok"
			# Let the entrypoint's delete-then-create settle before we
			# touch datasources ourselves.
			sleep 5
			return 0
		fi
		printf '.'
		sleep 2
		_i=$((_i + 1))
	done
	echo
	die "grafana did not come up within 3 minutes — check: docker compose logs grafana"
}

# Create the datasource the dashboards need, because DevLake does not.
#
# Its Grafana entrypoint tries on every start and fails: it authenticates as
# admin:$GF_SECURITY_ADMIN_PASSWORD, which upstream's compose file never sets,
# so the POST 401s and no datasource appears. Verified in the container logs:
#
#   Deleting old MySQL datasources...
#   ... POST /api/datasources status=401 error="no password provided"
#
# Named "mysql" because every stock DevLake dashboard binds to that name, so
# one datasource serves both theirs and ours. Skipped when it already exists,
# to avoid clobbering credentials an operator has adjusted.
ensure_datasource() {
	_pass="$(grafana_password)"
	printf 'checking the mysql datasource ... '

	if grafana_auth "$_pass" | curl -fsS --config - \
		-o /dev/null "http://localhost:3002/api/datasources/name/mysql" 2>/dev/null; then
		echo "already there"
		return 0
	fi

	_body="$(printf '{"name":"mysql","type":"mysql","access":"proxy",'
		printf '"url":"%s","database":"%s","user":"%s","isDefault":true,' \
			"$(db_field host)" "$(db_field database)" "$(db_field user)"
		printf '"secureJsonData":{"password":"%s"}}' "$(db_field password)")"

	# Retried, because the first attempt on a clean machine can land while
	# Grafana is still initialising and come back 500 — and because DevLake's
	# entrypoint may delete a datasource named "mysql" moments after we
	# create it. One shot at this meant a fresh install silently ended up
	# with no datasource and no dashboards.
	_try=0
	while [ "$_try" -lt 5 ]; do
		_out="$(grafana_auth "$_pass" | curl -sS --config - \
			-w '\n%{http_code}' \
			-H 'Content-Type: application/json' \
			-d "$_body" "http://localhost:3002/api/datasources" 2>/dev/null)"
		_code="$(printf '%s' "$_out" | tail -1)"

		case "$_code" in
		200 | 409)
			# 409 is "already exists", which is success for our purposes:
			# the entrypoint won the race and made the same thing.
			echo "created"
			return 0
			;;
		esac
		_try=$((_try + 1))
		sleep 3
	done

	echo "could not create (http $_code)"
	printf '%s\n' "$_out" | head -2 | sed 's/^/    /' >&2
	echo "  add it by hand at http://localhost:3002/connections/datasources" >&2
}

# Credentials as a curl config stanza on stdin, not -u on the command line:
# an argument is readable by every user on the host through ps. The two
# characters curl treats specially inside a quoted value are escaped —
# verified, because an unescaped " truncates the password at that point and
# an unescaped \ is swallowed, either of which authenticates as something
# else and returns a 401 nobody can explain.
grafana_auth() {
	printf 'user = "admin:%s"\n' "$(
		printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'
	)"
}

# Step 2 is a separate script with its own lifecycle — dashboards change when
# whodunit releases, not when this stack is built. Running it here is a
# convenience so a first install is one sitting; declining leaves a working
# stack and the command to finish later.
#
# The password is handed over as an environment variable rather than an
# argument. Both are readable on Linux, but /proc/PID/environ only by the
# same user or root, while /proc/PID/cmdline is world-readable — so an
# argument would expose it to every account on a shared host.
offer_dashboards() {
	echo
	_local="$(dirname "$0")/import-dashboards.sh"
	_remote="https://raw.githubusercontent.com/navjyotnishant/whodunit/main/deploy/devlake/import-dashboards.sh"
	_pass="$(grafana_password)"

	if [ -f "$_local" ]; then
		echo "importing dashboards"
		GRAFANA_PASSWORD="$_pass" sh "$_local" || {
			echo "dashboards were not imported — run this when ready:" >&2
			echo "  sh $_local" >&2
		}
	else
		echo "importing dashboards"
		GRAFANA_PASSWORD="$_pass" sh -c "curl -fsSL '$_remote' | sh" || {
			echo "dashboards were not imported — run this when ready:" >&2
			echo "  curl -fsSL $_remote | sh" >&2
		}
	fi
}

# Grafana's admin password comes from ADMIN_PASS, which upstream's compose
# file leaves commented out — so the default is Grafana's own admin/admin.
# Read it anyway: an operator who set it in .env would otherwise watch the
# dashboard import fail with no explanation, and be told the wrong password
# in the summary.
grafana_password() {
	_p="$(sed -n 's/^[[:space:]]*ADMIN_PASS=[[:space:]]*//p' .env 2>/dev/null | tail -1)"
	[ -n "$_p" ] || _p=admin
	printf '%s' "$_p"
}

# The database credentials, read from .env rather than assumed.
#
# DB_URL is the one place an operator edits them, and mysql://merico:merico@…
# is only upstream's published default — a shared instance should not keep
# it. Hardcoding the default here would mean this script silently fails
# against any deployment that changed it, which is exactly the deployment
# that matters.
#
# Note the credentials also appear literally in upstream's compose file
# (MYSQL_USER/MYSQL_PASSWORD), so changing DB_URL alone is not enough — said
# plainly in the summary rather than left to be discovered.
db_field() {
	# db_field <user|password|host|database>
	_url="$(sed -n 's/^[[:space:]]*DB_URL=[[:space:]]*//p' .env 2>/dev/null | tail -1)"
	[ -n "$_url" ] || _url="mysql://merico:merico@mysql:3306/lake"

	# mysql://USER:PASS@HOST:PORT/DB?params
	_creds="${_url#mysql://}"
	_creds="${_creds%%\?*}"
	_userpass="${_creds%%@*}"
	_hostdb="${_creds#*@}"

	case "$1" in
	# Percent-decoded: a DSN carrying a password with a '@' or '!' in it
	# must encode them, and handing the encoded form to mysql or Grafana
	# authenticates with the wrong string. Only the credential halves need
	# it; hosts and database names are not encoded in practice.
	user) urldecode "${_userpass%%:*}" ;;
	password) urldecode "${_userpass#*:}" ;;
	host) printf '%s' "${_hostdb%%/*}" ;;
	database) printf '%s' "${_hostdb#*/}" ;;
	esac
}

# printf's %b turns \xNN into the byte, so percent-decoding is a substitution
# away — no python, which this script cannot assume.
urldecode() {
	printf '%b' "$(printf '%s' "$1" | sed 's/+/ /g; s/%\(..\)/\\x\1/g')"
}

summary() {
	# The address a developer types is this machine's, not localhost, unless
	# they are on it. Printing only localhost is the most common reason a
	# remote install looks broken.
	_host="$(hostname 2>/dev/null || echo localhost)"

	# Reported rather than assumed: printing "admin / admin" at an operator
	# who changed it hands them a credential that does not work, on the one
	# line they are most likely to copy.
	_gpass="$(grafana_password)"
	if [ "$_gpass" = admin ]; then
		_gcreds="admin / admin"
	else
		_gcreds="admin / (the ADMIN_PASS set in .env)"
	fi

	_dbuser="$(db_field user)"
	_dbpass="$(db_field password)"
	_dbname="$(db_field database)"

	cat <<EOF

DevLake is up.

  config UI   http://localhost:4000
  grafana     http://localhost:3002        ($_gcreds)
  mysql       127.0.0.1:3306               ($_dbuser / $_dbpass, database "$_dbname")

Developers publish into it with:

  dun config datalake
    host and port   $_host:3306
    database        $_dbname
    username        $_dbuser
    password        $_dbpass

  dun sync

whodunit writes its own whodunit_* tables into that same "$_dbname" database
and never touches DevLake's. The tables are created on the first sync.

stop with:  docker compose down
wipe with:  docker compose down -v       (deletes all DevLake data)
EOF

	credentials_warning
}

# Said at the end, where it is read, and only when the defaults are still in
# place.
#
# These are DevLake's published defaults — they are in upstream's own
# env.example and in this project's README, so they are not a secret being
# leaked. That is exactly why they are dangerous on a shared host: everyone
# already knows them.
credentials_warning() {
	_dbpass="$(db_field password)"
	_gpass="$(grafana_password)"

	[ "$_dbpass" = merico ] || [ "$_gpass" = admin ] || return 0

	_dbuser="$(db_field user)"

	cat <<'EOF'

──────────────────────────────────────────────────────────────────────
  CHANGE THESE PASSWORDS
──────────────────────────────────────────────────────────────────────

This install uses DevLake's default credentials. They are published in
their documentation, so they are not secret from anyone:

EOF
	# Named outright rather than hinted at. Someone who does not know which
	# password is still a default cannot decide whether to care, and the
	# values are public knowledge already — printing them costs nothing and
	# makes the instruction actionable.
	if [ "$_gpass" = admin ]; then
		printf '  %-9s %-20s %s\n' grafana "admin / admin" "http://localhost:3002"
	fi
	if [ "$_dbpass" = merico ]; then
		printf '  %-9s %-20s %s\n' mysql "$_dbuser / $_dbpass" "127.0.0.1:3306"
	fi

	cat <<'EOF'

Fine on a laptop. Anyone who can reach these ports can read every
repository's attribution data and every dashboard, so before this goes
anywhere others can reach:

EOF
	if [ "$_gpass" = admin ]; then
		cat <<'EOF'
  grafana   add ADMIN_PASS=<new> to .env
            docker compose up -d --force-recreate grafana

            Already started once? The password is stored in grafana's
            database and ADMIN_PASS no longer applies. Reset it with:
            docker compose exec grafana \
              grafana cli admin reset-admin-password <new>

EOF
	fi
	if [ "$_dbpass" = merico ]; then
		cat <<'EOF'
  mysql     change it in BOTH places — DB_URL in .env, and
            MYSQL_USER/MYSQL_PASSWORD in docker-compose.yml, which
            upstream hardcodes. Changing only .env leaves the server on
            the old password and nothing connects.
            docker compose down && docker compose up -d

EOF
	fi
	cat <<'EOF'
  network   bind mysql to localhost, or put the stack behind a VPN.
            docker's published ports bypass most host firewalls.
──────────────────────────────────────────────────────────────────────
EOF
}

main "$@"
