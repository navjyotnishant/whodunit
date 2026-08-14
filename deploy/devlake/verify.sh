#!/bin/sh
# Author: Navjyot Nishant
# Created: 2026-08-13
# Last updated: 2026-08-13
# Description: Check that an imported datalake actually answers, not just imports.
#
# Run before a release, against a stack that has had `dun sync` run into it.
# Not part of CI: it needs four containers and real data, which makes it slow
# and flaky as a job and useful as a checklist item.
#
#   sh verify.sh                     # against localhost
#   sh verify.sh --grafana URL       # against another instance
#
# A successful import proves nothing on its own. Every check here is about
# whether the dashboards return rows, because the failure that matters looks
# identical to success from the import side: valid SQL, bound datasource,
# empty chart.

set -eu

GRAFANA="${GRAFANA_URL:-http://localhost:3002}"
GRAFANA_USER="${GRAFANA_USER:-admin}"
PASSWORD="${GRAFANA_PASSWORD:-}"
MYSQL_CONTAINER="${MYSQL_CONTAINER:-devlake-mysql-1}"

PASS=0
FAIL=0

main() {
	while [ $# -gt 0 ]; do
		case "$1" in
		--grafana) GRAFANA="$2"; shift 2 ;;
		--user) GRAFANA_USER="$2"; shift 2 ;;
		--password) PASSWORD="$2"; shift 2 ;;
		--container) MYSQL_CONTAINER="$2"; shift 2 ;;
		-h | --help)
			echo "usage: sh verify.sh [--grafana URL] [--user NAME] [--password PASS]"
			return 0
			;;
		*) echo "unknown option: $1" >&2; return 2 ;;
		esac
	done

	command -v curl >/dev/null 2>&1 || die "curl is required"
	command -v python3 >/dev/null 2>&1 || die "python3 is required (this is a dev tool)"

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

	echo "verifying $GRAFANA"
	echo

	check_datasource_single
	check_tables_have_rows
	check_no_leaked_values
	check_panels_return_rows

	echo
	echo "$PASS passed, $FAIL failed"
	[ "$FAIL" -eq 0 ] || return 1
}

die() {
	echo "error: $*" >&2
	exit 1
}

# Credentials on stdin rather than -u, which puts them in the process
# argument list where any user on the host can read them. The escaping covers
# the two characters curl treats specially inside a quoted value.
gauth() {
	printf 'user = "%s"\n' "$(
		printf '%s' "$AUTH" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'
	)"
}

ok() {
	PASS=$((PASS + 1))
	echo "  PASS  $1"
}

bad() {
	FAIL=$((FAIL + 1))
	echo "  FAIL  $1"
}

# 1. Exactly one mysql datasource, so nothing is ambiguous about which one
# the dashboards were bound to.
check_datasource_single() {
	echo "datasource"
	_n=$(gauth | curl -sS --config - "$GRAFANA/api/datasources" |
		sed 's/},{/}\n{/g' | grep -c '"type":"mysql"' || true)
	case "$_n" in
	1) ok "exactly one mysql datasource" ;;
	0) bad "no mysql datasource — the dashboards have nothing to query" ;;
	*) bad "$_n mysql datasources — which one the dashboards use is a coin flip" ;;
	esac
}

# 2. The tables exist and hold something. Everything below is meaningless
# against an empty database, so this failing explains the rest.
check_tables_have_rows() {
	echo "data"
	if ! command -v docker >/dev/null 2>&1; then
		echo "  SKIP  no docker — cannot inspect mysql directly"
		return 0
	fi
	for t in whodunit_commits whodunit_events; do
		_c=$(docker exec "$MYSQL_CONTAINER" mysql -N -umerico -pmerico lake \
			-e "SELECT COUNT(*) FROM $t" 2>/dev/null || echo "")
		if [ -z "$_c" ]; then
			bad "$t is missing — has dun sync run?"
		elif [ "$_c" -gt 0 ]; then
			ok "$t has $_c row(s)"
		else
			bad "$t is empty — has dun sync run?"
		fi
	done
}

# 3. Nothing from the author's machine survived the export. The Linear team
# uuid is the specific one that bit: it made valid SQL return nothing on
# every other install, which reads as "no data" rather than "misconfigured".
check_no_leaked_values() {
	echo "portability"
	_leaks=0
	for uid in whodunit whodunit-adoption whodunit-dora \
		whodunit-exec whodunit-hours whodunit-funnel; do
		_body=$(gauth | curl -sS --config - "$GRAFANA/api/dashboards/uid/$uid" 2>/dev/null || echo "")
		[ -n "$_body" ] || continue
		case "$_body" in
		*8842e00a*)
			bad "$uid still pins the author's Linear team"
			_leaks=$((_leaks + 1))
			;;
		esac
		case "$_body" in
		*'${DS_WHODUNIT}'*)
			bad "$uid has an unresolved datasource placeholder"
			_leaks=$((_leaks + 1))
			;;
		esac
	done
	[ "$_leaks" -eq 0 ] && ok "no machine-specific values in the imported dashboards"
}

# 4. The check the others exist to support: run each panel's SQL and see
# whether it answers. Template variables are substituted with something
# plausible — the point is to catch SQL that errors or returns nothing at
# all, not to reproduce a specific dashboard state.
check_panels_return_rows() {
	echo "panels"
	if ! command -v docker >/dev/null 2>&1; then
		echo "  SKIP  no docker — cannot run panel sql"
		return 0
	fi

	for uid in whodunit whodunit-adoption whodunit-dora \
		whodunit-exec whodunit-hours whodunit-funnel; do
		_sqlfile=$(mktemp)
		gauth | curl -sS --config - "$GRAFANA/api/dashboards/uid/$uid" 2>/dev/null |
			python3 "$(dirname "$0")/panel-sql.py" >"$_sqlfile" 2>/dev/null || true

		if [ ! -s "$_sqlfile" ]; then
			bad "$uid — could not extract any panel sql"
			rm -f "$_sqlfile"
			continue
		fi

		_total=0
		_errors=0
		_answered=0
		while IFS= read -r q; do
			[ -n "$q" ] || continue
			_total=$((_total + 1))
			_out=$(printf '%s' "$q" | docker exec -i "$MYSQL_CONTAINER" \
				mysql -N -umerico -pmerico lake 2>&1 || true)
			case "$_out" in
			*"ERROR "*) _errors=$((_errors + 1)) ;;
			"") : ;;
			*) _answered=$((_answered + 1)) ;;
			esac
		done <"$_sqlfile"
		rm -f "$_sqlfile"

		if [ "$_errors" -gt 0 ]; then
			bad "$uid — $_errors of $_total panel queries failed with a sql error"
		elif [ "$_answered" -eq 0 ]; then
			bad "$uid — all $_total panel queries ran but returned nothing"
		else
			ok "$uid — $_answered of $_total panels returned rows"
		fi
	done
}

main "$@"
