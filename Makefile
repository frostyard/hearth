.PHONY: help build fmt lint lint-version-check test race cross verify generate gen-check corrections-base vulncheck check ci clean site site-serve bump svu-version-check

# golangci-lint's release, read from mise.toml (frostyard/core ADR-0043).
GOLANGCI_LINT_VERSION := $(strip $(shell sed -n 's/^golangci-lint = "\(.*\)"/\1/p' mise.toml))
# govulncheck's release; the Go checksum database verifies the module.
GOVULNCHECK_VERSION := v1.9.0
# svu's release, read from mise.toml; make bump tags releases with it.
SVU_VERSION := $(strip $(shell sed -n 's/^svu = "\(.*\)"/\1/p' mise.toml))
SVU ?= svu
# Tracked or new Go files, skipping files deleted but not yet staged.
GO_FILES = $(shell git ls-files --cached --others --exclude-standard '*.go' | while read -r f; do [ -e "$$f" ] && echo "$$f"; done)
# Every buf template of the tree: the options, and the end-to-end test app.
E2E_TEMPLATE := testdata/e2e/buf.gen.yaml
E2E_PROTO := testdata/e2e/proto

help: ## List targets
	@grep -E '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | sed 's/:.*## /\t/' | expand -t 18

build: ## Build the commands into build/
	CGO_ENABLED=0 go build -trimpath -o build/ ./cmd/...

fmt: ## Format Go code and the proto files
	gofmt -w $(GO_FILES)
	buf format -w

lint: lint-version-check ## Run the pinned golangci-lint
	golangci-lint run

lint-version-check:
	@test -n "$(GOLANGCI_LINT_VERSION)" || { echo "mise.toml pins no golangci-lint"; exit 1; }
	@installed="$$(golangci-lint version --short 2>/dev/null)" || { \
		echo "golangci-lint $(GOLANGCI_LINT_VERSION) is required (install with: mise install)"; exit 1; }; \
	if [ "$$installed" != "$(GOLANGCI_LINT_VERSION)" ]; then \
		echo "expected golangci-lint $(GOLANGCI_LINT_VERSION), found $$installed (install with: mise install)"; exit 1; \
	fi

test: ## Run the Go tests
	go test ./...

race: ## Run the Go tests under the race detector
	CGO_ENABLED=1 go test -race ./...

# hearth is pure Go (CGO_ENABLED=0): the commands of a developer's machine and
# of a build host must build on every platform the fleet uses.
cross: ## Build every package for linux/amd64 and arm64, darwin/arm64 and windows/amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build ./...
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...

# The gate triad (frostyard/core ADR-0043, ADR-0044, ADR-0083). verify is
# credential-free and leaves a clean checkout clean; check formats first; ci
# adds the generated-code check, the race detector, the cross-builds and the
# corrections check.
verify: ## Tidy, vet, format, lint, buf and the tests
	go mod tidy -diff
	go vet ./...
	test -z "$$(gofmt -l $(GO_FILES))"
	$(MAKE) --no-print-directory lint
	buf lint
	buf format --diff --exit-code
	$(MAKE) --no-print-directory test

# The generated code and the end-to-end manifest are committed, so that a
# change of the options or of the plugin shows up in review (frostyard/core
# ADR-0085).
generate: ## Regenerate gen/ and the end-to-end manifest with the local plugins
	buf generate
	buf generate --template $(E2E_TEMPLATE) $(E2E_PROTO)

gen-check: ## Regenerate into a temp dir, and fail when the tree differs from a fresh run
	@test -f $(E2E_TEMPLATE) || { echo "$(E2E_TEMPLATE) is missing"; exit 1; }
	@tmp=$$(mktemp -d) || exit 1; \
	trap 'rm -rf "$$tmp"' EXIT; \
	buf generate -o "$$tmp" || exit 1; \
	buf generate --template $(E2E_TEMPLATE) -o "$$tmp" $(E2E_PROTO) || exit 1; \
	ok=1; \
	diff -r "$$tmp/gen" gen || ok=0; \
	diff -r "$$tmp/testdata/e2e/gen" testdata/e2e/gen || ok=0; \
	if [ "$$ok" = 0 ]; then \
		echo "the generated code or the committed manifest differs from a fresh run; run make generate and commit the result"; \
		exit 1; \
	fi; \
	echo "gen-check: the generated code and the committed manifest match a fresh run"

corrections-base: ## Check the corrections inbox against BASE (skipped when BASE is empty)
	@if [ -z "$(BASE)" ]; then \
		echo "corrections-base: BASE is not set, so the corrections inbox is not compared"; \
	else \
		go run ./internal/cmd/corrections-base -base "$(BASE)"; \
	fi

# Needs the network, and a new upstream advisory can fail it, so it is in no
# gate (check, verify, ci); CI runs it as its own job.
vulncheck: ## Report reachable vulnerabilities (govulncheck; needs the network)
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

check: fmt verify ## Format, then verify (the developer gate)

ci: verify gen-check race cross corrections-base ## verify plus the generated-code check, race, cross-builds and corrections

clean: ## Remove build output
	rm -rf build dist coverage.out coverage.html

# The documentation site (site/, frostyard/core ADR-0070), built with pinned
# ProperDocs and MaterialX in a Python venv under build/. No gate (check,
# verify, ci) builds it, so a change that does not touch site/ needs no Python
# toolchain.
SITE_VENV := build/site-venv

$(SITE_VENV)/bin/properdocs: site/requirements.txt
	python3 -m venv $(SITE_VENV)
	$(SITE_VENV)/bin/pip install -q --require-hashes -r site/requirements.txt
	touch $@

site: $(SITE_VENV)/bin/properdocs ## Build the documentation site into site/dist/ (strict; python3)
	$(SITE_VENV)/bin/properdocs build --strict -f site/properdocs.yml

site-serve: $(SITE_VENV)/bin/properdocs ## Preview the documentation site at http://127.0.0.1:8000/hearth/
	$(SITE_VENV)/bin/properdocs serve -f site/properdocs.yml

# Pinned svu, from mise.toml (core ADR-0043). `svu --version` prints a
# "GitVersion:    MAJOR.MINOR.PATCH" line.
svu-version-check:
	@test -n "$(SVU_VERSION)" || { echo "mise.toml pins no svu"; exit 1; }
	@if ! command -v "$(SVU)" >/dev/null 2>&1; then \
		echo "svu $(SVU_VERSION) is required for make bump (not installed; run: mise install)"; \
		exit 1; \
	fi; \
	installed="$$($(SVU) --version 2>&1 | sed -n 's/^GitVersion:[[:space:]]*//p' | head -1)"; \
	if [ -z "$$installed" ]; then installed=unknown; fi; \
	if [ "$$installed" != "$(SVU_VERSION)" ]; then \
		echo "expected svu $(SVU_VERSION), found $$installed (run: mise install)"; \
		exit 1; \
	fi

# The only sanctioned way to tag a release (frostyard/core ADR-0012, ADR-0053):
# the tag on main is the record of the release.
# svu reads only vX.Y.Z tags (.svu.yml's tag.pattern) and fails when there is
# none, so the first release is v0.1.0.
bump: ## Tag the next version with svu and push the tag (main, clean, equal to origin/main; runs make check)
	@$(MAKE) --no-print-directory svu-version-check
	@test "$$(git rev-parse --abbrev-ref HEAD)" = main || { echo "make bump runs on main"; exit 1; }
	@git fetch --tags origin main
	@test "$$(git rev-parse HEAD)" = "$$(git rev-parse origin/main)" || { echo "main is not origin/main: pull or push first"; exit 1; }
	@test -z "$$(git status --porcelain)" || { echo "the working tree is not clean: commit or stash first"; exit 1; }
	@$(MAKE) --no-print-directory check
	@test -z "$$(git status --porcelain)" || { echo "make check changed the tree: commit that first"; exit 1; }
	@set -eu; \
	version=$$(if [ -z "$$(git tag -l 'v[0-9]*')" ]; then echo v0.1.0; else $(SVU) next; fi); \
	printf '%s\n' "$$version" | grep -Eqx 'v[0-9]+\.[0-9]+\.[0-9]+' || { echo "svu next gave '$$version', not vX.Y.Z"; exit 1; }; \
	git tag -a "$$version" -m "Version $$version"; \
	echo "Tagged $$version; pushing it to origin..."; \
	git push origin "refs/tags/$$version"
