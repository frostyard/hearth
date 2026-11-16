# Build from source

The gate needs Go, `make` and the 4 tools that `mise.toml` pins. It needs no
package manager beyond that, and no network access in its tests.

## Build and check

```sh
mise install   # golangci-lint, buf, protoc-gen-go, svu
make check     # format, then verify
make ci        # what CI runs: verify, the generated code, race, cross-builds
```

| Target | What it runs |
| --- | --- |
| `make verify` | `go mod tidy -diff`, `go vet`, a gofmt check, golangci-lint, `buf lint`, `buf format --diff`, `go test ./...` |
| `make generate` | `buf generate` for the options, and for the end-to-end demo app |
| `make gen-check` | regenerate into a temporary directory, then fail when the committed output differs |
| `make race` | the tests under the race detector |
| `make cross` | build every package for linux/amd64 and arm64, darwin/arm64 and windows/amd64 |
| `make corrections-base` | compare the corrections inbox with `BASE` |
| `make site` | build this site into `site/dist`, in strict mode |

The proto files come from `buf.yaml` and `buf.lock`. `buf.lock` is in the tree
and `buf dep update` keeps it: it pins each module dependency by digest, and it
pins none today because `google/protobuf/descriptor.proto` is built into buf.

## The documentation site

```sh
make site        # strict build into site/dist
make site-serve  # preview at http://127.0.0.1:8000/hearth/
```

The site needs the Python lock in `site/requirements.txt` and writes its
virtual environment to `build/site-venv`. `make ci` does not build the site, so
a change that does not touch `site/` needs no Python toolchain.

## Write for users

Pages live in `site/content/`. A page explains what a user does, not how the
code works: the maintainer contracts stay in `docs/`. Read
[the options](../guides/options.md) for the shape of a guide page, and
[the manifest format](../reference/manifest.md) for the shape of a reference
page.
