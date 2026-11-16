# Spec: Manifest

This contract governs an app's `manifest.json`, the JSON Schema that defines it
(`schema/manifest-v1.json`), and the compatibility rules of
`hearth manifest diff OLD NEW`. Consumers: hearth's plugins, which write the
file; lodge, which serves the app from it; an app's `make ci`, which runs the
diff; and every agent that reads an app's contract.

## Interface

A manifest is 1 JSON object, in format major 1. Its members are these, in this
order:

| Member | Type | Required | Constraints |
| --- | --- | --- | --- |
| `hearth` | integer | yes | `1`: the format's major version |
| `app` | string | yes | `^[a-z][a-z0-9]{0,15}$` |
| `version` | string | yes | `^(0\|[1-9][0-9]*)\.(0\|[1-9][0-9]*)\.(0\|[1-9][0-9]*)$` |
| `capabilities` | array of string | yes | each `^[a-z][a-z0-9]{0,15}:[a-z][a-z0-9_]*$`, sorted, no repeat |
| `selectors` | array of string | yes | each `^[a-z][a-z0-9_]*$`, sorted, no repeat |
| `operations` | array of operation | yes | sorted by `name` |
| `events` | array of event | yes | sorted by `type` |

1 operation holds:

| Member | Type | Required | Constraints |
| --- | --- | --- | --- |
| `name` | string | yes | the RPC's full name |
| `permission` | string | yes | capability, as above |
| `effect` | string | yes | `read`, `write` or `outward` |
| `expose` | array of string | yes | not empty, no repeat, at most `ui` and `agent`, in that order |
| `selectors` | array of selector | yes | may be empty, sorted by `field` |
| `tool` | string | only when `expose` holds `agent` | `^[a-z][a-z0-9_]*$`, never with `ui` alone |

1 selector holds `field` (a dotted path of proto field names, such as
`folder.name`) and `selector` (the selector's name), both required.

1 event holds:

| Member | Type | Required | Constraints |
| --- | --- | --- | --- |
| `type` | string | yes | `^org\.frostyard\.[a-z][a-z0-9]{0,15}\.[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$` |
| `message` | string | yes | the full name of the message that carries the event |
| `topic` | string | yes | `<app>.<object>.<verb>`, with `.<selector>` when the event carries 1 |
| `selector` | string | only when the event carries 1 | `^[a-z][a-z0-9_]*$` |
| `read_permission` | string | yes | capability, as above |

The schema is JSON Schema draft 2020-12, with `additionalProperties: false` at
every object, `"hearth": {"const": 1}`, the patterns above, and `tool`
required exactly when `expose` holds `agent`.

```json
{
  "hearth": 1,
  "app": "marks",
  "version": "0.1.0",
  "capabilities": ["marks:read", "marks:write"],
  "selectors": ["folder"],
  "operations": [
    {
      "name": "frostyard.frostmarks.v1.Marks.ListBookmarks",
      "permission": "marks:read",
      "effect": "read",
      "expose": ["ui", "agent"],
      "selectors": [{"field": "folder.name", "selector": "folder"}],
      "tool": "list_bookmarks"
    }
  ],
  "events": [
    {
      "type": "org.frostyard.marks.bookmark.created",
      "message": "frostyard.frostmarks.v1.BookmarkCreated",
      "topic": "marks.bookmark.created.folder",
      "selector": "folder",
      "read_permission": "marks:read"
    }
  ]
}
```

## Rules

- **Canonical JSON.** The file is 2-space indented, with 1 final newline, the
  members in the order above, and each array in its stated order.
- **The topic carries the path.** An object at the root of a store publishes on
  `<app>.<object>.<verb>`, plus `.<selector>`. An object at the path `/a/b`
  publishes on `<topic>.a.b`: each path segment follows the topic's own
  segments, percent-encoded by the task that publishes at a path.
- **Written only when valid.** The plugin validates the manifest against the
  schema before it writes the file. A manifest that fails is an internal error
  of the plugin.
- **Format major.** `hearth` is the format's major version, not the app's. A
  reader that does not know a manifest's major refuses it with a reason that
  names both versions: "the manifest is major 2, and this hearth reads major
  1".

### `hearth manifest diff OLD NEW`

The command compares by key: an operation by its `name`, a tool by its name,
and an event by its `type`.

| Breaking change | Detail |
| --- | --- |
| a removed operation | the old operation's `name` is gone |
| a removed tool | an operation lost `agent` from `expose`, or its `tool` name changed |
| a removed event | the old event's `type` is gone |
| a changed operation | its `permission`, its `effect`, or the set of its `{field, selector}` pairs changed (a selector that moves to another request field is breaking: the permission check then reads another field) |
| a changed event | its `read_permission`, its `selector`, or its `topic` changed |
| a changed app | everything of the old app is removed |

An addition is not a breaking change: a new operation, a new tool, a new event,
or an operation that gains `agent`.

"Next major" is the release that may carry a breaking change:

- an app at `>= 1.0.0`: the new manifest's major number is greater than the
  old 1;
- an app at `0.y.z`: the new manifest's major number is greater than 0, or the
  new manifest is `0.y'.z'` with `y' > y` (svu's `v0: true` rule).

### Exit codes

| Code | When |
| --- | --- |
| 0 | no breaking change; or a breaking change and the next major version; or OLD does not exist |
| 1 | a breaking change and not the next major version |
| 2 | wrong usage; a manifest that cannot be read, is not valid against the schema, or has an unknown format major |

The command prints 1 line for each breaking change, sorted, then 1 verdict
line:

- `compatible: <old> -> <new>` when nothing breaks;
- `the major version allows the breaking changes: <old> -> <new>` when the next
  major version carries them, after their lines;
- `the breaking changes need the next major version: <old> -> <new>` when it
  does not.

When OLD does not exist, it prints `no old manifest: nothing to compare` and
exits 0: a new app has no contract to break. `--verbose` also prints the
additions, 1 line each, sorted.

### The app's gate recipe

An app runs the diff in its `make ci`, against its default branch:

```sh
# the app's gate, against the default branch; it fails closed
base=origin/main
git rev-parse --verify --quiet "$base^{commit}" >/dev/null ||
  { echo "no base commit $base: fetch it first"; exit 2; }
old="$(mktemp -d)/manifest.json"
if git cat-file -e "$base:gen/manifest.json" 2>/dev/null; then
  git show "$base:gen/manifest.json" >"$old" || exit 2
fi
hearth manifest diff "$old" gen/manifest.json
```

The recipe fails closed:

- it stops with exit 2 when the base commit is not there, which is what a
  shallow clone gives;
- a `git show` failure stops it; such a failure is not an absent manifest;
- only an absent manifest path at an existing base, which `git cat-file -e`
  tests, leaves OLD absent, so the command prints
  `no old manifest: nothing to compare` and exits 0.

`hearth manifest validate FILE` reads 1 manifest and prints `FILE: valid`, or
exits 2 with the reason.

## Derived artifacts

| Artifact | Derivation |
| --- | --- |
| `schema/manifest-v1.json` | the format's schema, embedded in `schema/schema.go` |
| every app's `manifest.json` | its `make generate` |
| `manifest/testdata/diff/<case>/` | the diff's fixtures, 1 folder for each change |

## References

- Rationale: [ADR-0004](../adr/0004-generate-the-manifest-from-protobuf-options.md)
- Context: [design/overview.md](../design/overview.md)
- What fills it: [specs/options.md](options.md)
- Builds on: [frostyard/core ADR-0085](https://github.com/frostyard/core/blob/main/docs/adr/0085-build-new-first-party-apps-on-hearth-with-lodge-as-their-hub.md),
  [frostyard/core ADR-0016](https://github.com/frostyard/core/blob/main/docs/adr/0016-reverse-dns-org-frostyard-identifiers.md)
