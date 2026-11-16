.PHONY: help generate gen-check verify race manifest-check ci

# Tracked or new Go files of the example, skipping files deleted but not yet
# staged.
GO_FILES = $(shell git ls-files --cached --others --exclude-standard '*.go' | while read -r f; do [ -e "$$f" ] && echo "$$f"; done)

help: ## List targets
	@grep -E '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | sed 's/:.*## /\t/' | expand -t 16

# The plugins of buf.gen.yaml run with go run of the hearth version that go.mod
# requires: go.sum pins them, and no plugin comes from a registry.
generate: ## Regenerate gen/ with the local plugins
	buf generate

gen-check: ## Regenerate into a temp dir, and fail when gen/ differs from a fresh run
	@tmp=$$(mktemp -d) || exit 1; \
	trap 'rm -rf "$$tmp"' EXIT; \
	buf generate -o "$$tmp" || exit 1; \
	if ! diff -r "$$tmp/gen" gen; then \
		echo "gen-check: gen/ differs from a fresh buf generate; run make generate and commit the result"; \
		exit 1; \
	fi; \
	echo "gen-check: gen/ matches a fresh run"

verify: ## Tidy, vet, format, lint, buf and the tests
	go mod tidy -diff
	go vet ./...
	test -z "$$(gofmt -l $(GO_FILES))"
	golangci-lint run
	buf lint
	buf format --diff --exit-code
	go test ./...

race: ## Run the tests under the race detector
	CGO_ENABLED=1 go test -race ./...

# The manifest is the app's contract: 1 change of it decides whether a consumer
# needs a new consent (ADR-0004). BASE is the commit to compare with, and the
# gate fails closed when it cannot look the manifest up (hearth's AGENTS.md,
# "A gate recipe fails closed"): only a base commit that is there, and a path
# that git ls-tree finds absent, skip the comparison.
manifest-check: ## Compare gen/manifest.json with the 1 at BASE (skipped when BASE is empty)
	@if [ -z "$(BASE)" ]; then \
		echo "manifest-check: BASE is not set, so the manifest is not compared"; \
	elif ! git rev-parse --verify -q "$(BASE)^{commit}" >/dev/null; then \
		echo "manifest-check: BASE $(BASE) is not a commit of this repository"; \
		exit 2; \
	elif ! listing=$$(git ls-tree "$(BASE)" -- gen/manifest.json); then \
		echo "manifest-check: git ls-tree $(BASE) -- gen/manifest.json failed"; \
		exit 2; \
	elif [ -z "$$listing" ]; then \
		echo "manifest-check: no manifest at $(BASE)"; \
	else \
		tmp=$$(mktemp) || exit 2; \
		trap 'rm -f "$$tmp"' EXIT; \
		if ! git show "$(BASE):./gen/manifest.json" > "$$tmp"; then \
			echo "manifest-check: git show $(BASE):./gen/manifest.json failed"; \
			exit 2; \
		fi; \
		go run github.com/frostyard/hearth/cmd/hearth manifest diff "$$tmp" gen/manifest.json; \
	fi

ci: verify gen-check race manifest-check ## verify plus the generated code, race and the manifest diff
