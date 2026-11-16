# Install hearth

hearth ships 5 commands: `hearth`, the CLI, and 4 `protoc` plugins —
`protoc-gen-hearth-manifest`, which writes an app's manifest,
`protoc-gen-hearth-authz`, which writes its permission checks,
`protoc-gen-hearth-mcp`, which writes its MCP tools, and
`protoc-gen-hearth-events`, which writes its event publishers. All 5 are Go
programs, and all need no service beside them.

## Requirements

- Go 1.27 or later, to install from source. The 5 commands are otherwise
  standalone binaries.
- A Go module for the app, and `buf` to run the plugin.

## Installation

Install the 5 commands with `go install`:

```sh
go install github.com/frostyard/hearth/cmd/hearth@latest
go install github.com/frostyard/hearth/cmd/protoc-gen-hearth-manifest@latest
go install github.com/frostyard/hearth/cmd/protoc-gen-hearth-authz@latest
go install github.com/frostyard/hearth/cmd/protoc-gen-hearth-mcp@latest
go install github.com/frostyard/hearth/cmd/protoc-gen-hearth-events@latest
```

Go puts them in the directory that `go env GOBIN` names, or in
`$(go env GOPATH)/bin`. Keep that directory on `PATH`, and `buf` finds the
plugin by name.

Install 1 version, not the latest 1, when a repository pins it:

```sh
go install github.com/frostyard/hearth/cmd/hearth@v0.1.0
```

### Pin the version with mise

[mise](https://mise.jdx.dev/) installs a tool from its Go module with the
`go:` backend. In the app's `mise.toml`:

```toml
[tools]
"go:github.com/frostyard/hearth/cmd/hearth" = "v0.1.0"
"go:github.com/frostyard/hearth/cmd/protoc-gen-hearth-manifest" = "v0.1.0"
"go:github.com/frostyard/hearth/cmd/protoc-gen-hearth-authz" = "v0.1.0"
"go:github.com/frostyard/hearth/cmd/protoc-gen-hearth-mcp" = "v0.1.0"
"go:github.com/frostyard/hearth/cmd/protoc-gen-hearth-events" = "v0.1.0"
```

Then `mise install` provisions the same 5 versions on every machine and in CI,
and `mise.lock` holds their checksums.

!!! note "Check the version"

    Run `hearth help` after the install. It prints the usage of the 2
    commands. Then run `hearth manifest validate` on the app's `manifest.json`:
    it reads the file only if the install knows the format major.

## Updates

A release is a tag in the `vX.Y.Z` series of
[frostyard/hearth](https://github.com/frostyard/hearth/tags). To move to the
newest 1:

```sh
go install github.com/frostyard/hearth/cmd/hearth@latest
go install github.com/frostyard/hearth/cmd/protoc-gen-hearth-manifest@latest
go install github.com/frostyard/hearth/cmd/protoc-gen-hearth-authz@latest
go install github.com/frostyard/hearth/cmd/protoc-gen-hearth-mcp@latest
go install github.com/frostyard/hearth/cmd/protoc-gen-hearth-events@latest
```

With mise, change all 5 versions in `mise.toml`, then run `mise install`. A
pinned version moves when the app's pull request moves it, so a change of the
plugin and the manifest it writes arrive in the same review.

The manifest has its own format major, which is separate from the release
version. A newer hearth writes a manifest of the same major for the same
`app` and `version` parameters, unless the changelog says otherwise.

### Run the plugins with go run

An app's module can run each plugin at the hearth version its `go.mod`
requires, with no install: `go.sum` then pins the plugin. In the app's
`buf.gen.yaml`:

```yaml
plugins:
  - local: [go, run, github.com/frostyard/hearth/cmd/protoc-gen-hearth-events]
    out: gen
    opt: [paths=source_relative, app=notelite, version=0.1.0]
```

The [notes-lite example](https://github.com/frostyard/hearth/tree/main/examples/notes-lite)
runs every plugin that way.

## First manifest

Continue with [the options](../guides/options.md) to annotate an app's first
service.
