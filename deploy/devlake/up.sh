#!/bin/sh
# Author: Navjyot Nishant
# Created: 2026-08-12
# Last updated: 2026-08-13
# Description: Bring up a local DevLake stack (wrapper around setup-datalake.sh).
#
# Kept because it is what the README and muscle memory reach for from a
# checkout. The work moved to setup-datalake.sh, which also runs standalone
# from curl on a machine that has never cloned this repo — see README.md.
set -eu

DIR="$(cd "$(dirname "$0")" && pwd)"
exec sh "$DIR/setup-datalake.sh" "$@"
