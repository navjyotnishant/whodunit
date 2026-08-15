# Author: Navjyot Nishant
# Created: 2026-08-14
# Last updated: 2026-08-14
# Description: One entrypoint for every check, so CI and a developer run
# the same commands.
#
# `make check` is the whole gate. CI calls these targets rather than
# repeating the commands in YAML — a workflow that inlines its own version
# of a check drifts from the one people run locally, and the drift is only
# discovered when CI fails on something that passed on a laptop.
#
# Everything here uses the Go toolchain and nothing else. No target
# installs anything.

.PHONY: check test race bench lint fmt vet dashboards perf clean help
.DEFAULT_GOAL := help

# The full gate. Ordered cheapest-first so an obvious failure reports in
# seconds rather than after the race detector has run.
check: lint test race perf bench dashboards
	@echo "all checks passed"

# Formatting, as a check rather than a rewrite: `make check` must not
# modify the tree.
fmt:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed for:"; echo "$$unformatted"; exit 1; \
	fi

vet:
	@go vet ./...

lint: fmt vet

test:
	@go test ./...

# Races are invisible to a plain `go test`. Roughly ten times the wall
# clock, which is why it is a separate target and why CI runs it on one
# platform only — a data race is a property of the code, not the OS.
race:
	@go test -race ./...

# The performance gates. These are ordinary tests that assert a budget and
# fail loudly, not benchmarks that print a number nobody reads.
#
# -count=1 defeats the test cache: a cached pass proves the code did not
# change, which is not the same as proving it is still fast enough.
#
# The pattern is matched against test names, so a new timing test must be
# named to match one of these or it silently never runs as a gate. That
# already happened once — ScalesLinearly did not match a "Scaling" pattern.
perf:
	@go test ./... -count=1 \
		-run 'Budget|Perf|Scales|Cheaply|CriticalPath|FailsFast|DoesNotSlow'

# Benchmarks are compiled but never executed by `go test`, so one can rot
# for months unnoticed. One iteration each proves they still run; this is a
# smoke check, not a measurement.
bench:
	@go test ./... -run '^$$' -bench . -benchtime 1x

# The importable dashboards are generated from the mounted ones and must
# not drift. A stale export is only discovered when someone imports it and
# finds a panel missing.
#
# The second check is a different failure: import-dashboards.sh names its
# dashboards in a literal list, so a new one is silently never imported.
# Both copies agree, every test passes, and the dashboard is simply absent
# from Grafana — which is how whodunit-cost shipped without being live.
dashboards:
	@deploy/devlake/export-dashboards.py --check
	@deploy/devlake/check-dashboard-list.py
	@deploy/devlake/check-contributor-filter.py
	@deploy/devlake/check-panel-descriptions.py

clean:
	@rm -rf dist

help:
	@echo "make check       every gate below, cheapest first"
	@echo "make lint        gofmt check + go vet"
	@echo "make test        go test ./..."
	@echo "make race        go test -race ./..."
	@echo "make perf        the budget-asserting tests"
	@echo "make bench       compile-and-run every benchmark once"
	@echo "make dashboards  importable dashboards match the mounted ones"
