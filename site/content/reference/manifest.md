# Manifest format

A manifest is 1 JSON object, in format major 1. The member `hearth` holds the
format's major version, which is separate from the app's version in
`version`.

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

The file is canonical JSON: 2-space indent, 1 final newline, the members in the
order below, and each array in its stated order.

## Top-level members

| Member | Type | Required | Meaning |
| --- | --- | --- | --- |
| `hearth` | integer | yes | the format's major version, always `1` |
| `app` | string | yes | the app's id, such as `marks` |
| `version` | string | yes | the app's semantic version, such as `0.1.0` |
| `capabilities` | array of string | yes | every capability of the operations and events, sorted |
| `selectors` | array of string | yes | every selector name, sorted |
| `operations` | array of operation | yes | the app's RPCs, sorted by `name` |
| `events` | array of event | yes | the app's events, sorted by `type` |

## An operation

| Member | Type | Required | Meaning |
| --- | --- | --- | --- |
| `name` | string | yes | the RPC's full name |
| `permission` | string | yes | the capability the call needs, `<app>:<capability>` |
| `effect` | string | yes | `read`, `write` or `outward` |
| `expose` | array of string | yes | `ui`, `agent`, or both, in that order |
| `selectors` | array of selector | yes | the request fields that carry a selector, as `{field, selector}` pairs; may be empty |
| `tool` | string | only with `agent` | the RPC's MCP tool name |

1 selector holds `field`, the dotted path of proto field names such as
`folder.name`, and `selector`, the selector's name.

## An event

| Member | Type | Required | Meaning |
| --- | --- | --- | --- |
| `type` | string | yes | `org.frostyard.<app>.<object>.<verb>` |
| `message` | string | yes | the full name of the message that carries the event |
| `topic` | string | yes | `<app>.<object>.<verb>`, with `.<selector>` when it carries 1 |
| `selector` | string | only with 1 | the selector whose value the event carries |
| `read_permission` | string | yes | the capability a consumer needs to read the event |

An object at the root of a store publishes on its topic. An object at the path
`/a/b` publishes on `<topic>.a.b`: each path segment follows the topic's own
segments.

## The topic's use

A read grant on a path becomes a topic prefix, so the broker filters events
before delivery. That is why a read permission that a selector scopes must be
on the topic of every event that names it.

## Format majors

A reader that does not know a manifest's major refuses the file with a reason
that names both versions, for example "the manifest is major 2, and this hearth
reads major 1". The major changes when a member changes in a way that an old
reader cannot follow; the addition of an optional member does not change it.

Check a file with `hearth manifest validate`, and compare 2 files with
`hearth manifest diff`: see [the manifest](../guides/manifest.md).
