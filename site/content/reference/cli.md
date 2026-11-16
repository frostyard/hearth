# Commands

hearth ships 2 commands: `hearth`, the CLI, and
`protoc-gen-hearth-manifest`, the plugin that `buf` runs. The CLI reads and
checks a manifest; it writes nothing.

## `hearth`

Run it with no arguments for its usage.

| Command | What it does |
| --- | --- |
| `hearth manifest diff OLD NEW` | print each breaking change of NEW against OLD, then the verdict |
| `hearth manifest validate FILE` | check 1 manifest against the format's schema |
| `hearth help` | print the usage |

### `hearth manifest diff OLD NEW`

| Option | Meaning |
| --- | --- |
| `--verbose` | print the additions as well |
| `--help`, `-h` | print the usage and exit |

A path that does not exist as OLD prints
`no old manifest: nothing to compare` and exits 0.

Write the gate recipe so that it fails closed: check the base commit
(`git rev-parse --verify`), test the manifest path with `git cat-file -e`, and
let only an absent path at an existing base leave OLD absent. A `git show` that
fails stops the gate. [The manifest guide](../guides/manifest.md) holds the
recipe.

### `hearth manifest validate FILE`

It prints `FILE: valid`, or exits 2 with the reason.

## `protoc-gen-hearth-manifest`

`buf` runs it; it is not a command a person calls. It takes 2 parameters:

| Parameter | Meaning |
| --- | --- |
| `app` | the app's id, `^[a-z][a-z0-9]{0,15}$` |
| `version` | the app's semantic version, `MAJOR.MINOR.PATCH` |

It writes `manifest.json` at the output root. When a rule fails it writes
nothing, and prints 1 line for each problem to its standard error.

## Exit status

| Code | Meaning |
| --- | --- |
| 0 | success: no breaking change, or the next major version carries it |
| 1 | `diff`: the breaking changes need the app's next major version |
| 2 | wrong usage, or a manifest that cannot be read, is not valid, or has an unknown format major |

See [the manifest](../guides/manifest.md) for a task that uses these commands.
