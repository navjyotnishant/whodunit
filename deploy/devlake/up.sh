#!/bin/sh
# Author: Navjyot Nishant
# Created: 2026-08-12
# Description: Bring up a local Apache DevLake stack for whodunit to write into.
#
# This exists so `dun sync` has something real to target during development.
# It is a convenience for local evaluation, not a supported deployment — see
# README.md in this directory.
set -eu

DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$DIR"

if ! command -v docker >/dev/null 2>&1; then
	echo "docker is not on PATH" >&2
	exit 1
fi

# DevLake refuses to start without an encryption secret, and generating one
# per deployment is the point — a shared default would be worse than none.
if [ ! -f .env ]; then
	echo "creating .env from env.example"
	cp env.example .env

	secret="$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 128)"
	# BSD and GNU sed disagree about -i, so rewrite via a temp file instead.
	awk -v s="$secret" '/^ENCRYPTION_SECRET=/ {print "ENCRYPTION_SECRET=" s; next} {print}' \
		.env >.env.tmp && mv .env.tmp .env
	echo "generated ENCRYPTION_SECRET"
fi

echo "starting devlake (mysql, devlake, config-ui, grafana)"
docker compose up -d

cat <<'EOF'

up. give it a minute to migrate on first run.

  config UI   http://localhost:4000
  grafana     http://localhost:3002       (admin / admin)
  mysql       127.0.0.1:3306              (merico / merico, database "lake")

whodunit writes its own whodunit_* tables into that same "lake" database
and never touches DevLake's. Point a sync at:

  mysql://merico:merico@127.0.0.1:3306/lake

stop with:  docker compose down
wipe with:  docker compose down -v      (deletes all DevLake data)
EOF
