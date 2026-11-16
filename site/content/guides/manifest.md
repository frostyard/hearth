# The manifest

An app's `manifest.json` is its contract with lodge: its capabilities, its
selectors, its operations and its events. hearth generates it from the app's
Protobuf service, and `hearth manifest diff` keeps the contract compatible.

## Generate the manifest

Commit the file that `buf generate` writes, and keep its generation in the
app's gate:

```yaml
# buf.gen.yaml
version: v2
plugins:
  - local: protoc-gen-hearth-manifest
    out: gen
    opt:
      - app=marks
      - version=0.1.0
```

```sh
buf generate      # writes gen/manifest.json
git diff --exit-code gen   # fails when the committed manifest is stale
```

The plugin validates the file against the format's JSON Schema before it
writes it.

## Check compatibility

`hearth manifest diff OLD NEW` compares 2 manifests and reports what breaks:

```sh
# the app's gate, against the default branch
git show origin/main:gen/manifest.json > /tmp/old-manifest.json
hearth manifest diff /tmp/old-manifest.json gen/manifest.json
```

It prints 1 line for each breaking change, then the verdict:

```
operation frostyard.frostmarks.v1.Marks.GetBookmark removed
the breaking changes need the next major version: 1.2.0 -> 1.2.1
```

| Exit code | Meaning |
| --- | --- |
| 0 | no breaking change, or the app's next major version carries it |
| 1 | a breaking change that needs the next major version |
| 2 | a manifest that cannot be read, or is not valid |

A breaking change is a removed operation, tool or event, or a changed
permission, effect, selector or topic of an existing 1. A selector is its
request field and its name together, so a selector that moves to another
request field is breaking: the permission check then reads that field. An
addition is not breaking; `--verbose` prints the additions as well.

The command prints a changed selector as its pairs, for example
`selectors changed from [folder.name=folder] to [group.name=folder]`.

### The version rule

A breaking change needs the app's next major version:

- an app at `1.0.0` or later needs a greater major number: `1.2.0` to `2.0.0`;
- an app at `0.y.z` needs a greater minor number: `0.3.1` to `0.4.0`, not
  `0.3.1` to `0.3.2`.

A new app has no old manifest, so the command prints
`no old manifest: nothing to compare` and exits 0. Use `git show`'s failure as
that case, or skip the step while the app has no manifest on the default
branch.

## Validate 1 manifest

```sh
hearth manifest validate gen/manifest.json
```

It prints `gen/manifest.json: valid`, or exits 2 with the reason. Use it in a
script that reads a manifest it did not generate.

Look up every member in the [manifest format](../reference/manifest.md).
