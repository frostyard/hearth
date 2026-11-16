# Spec: Options

This contract governs `proto/frostyard/hearth/v1/options.proto`, the Protobuf
options that an app writes on its own API, and the rules that
`internal/model` checks when a hearth plugin generates from those files.
Consumers: every hearth plugin, every app on hearth, and the tests of both.

## Interface

The file declares 2 enums, 2 messages and 3 extensions, and nothing else.

| Element | Value | Meaning |
| --- | --- | --- |
| `Effect.EFFECT_UNSPECIFIED` | 0 | not an effect; generation fails |
| `Effect.EFFECT_READ` | 1 | reads data and changes nothing |
| `Effect.EFFECT_WRITE` | 2 | changes data reversibly |
| `Effect.EFFECT_OUTWARD` | 3 | irreversible, or leaves the system |
| `Expose.EXPOSE_UNSPECIFIED` | 0 | not an exposure; generation fails |
| `Expose.EXPOSE_UI` | 1 | reachable from the app's own user interface |
| `Expose.EXPOSE_AGENT` | 2 | an MCP tool that an agent can call |

| Extension | On | Number | Type |
| --- | --- | --- | --- |
| `hearth_rpc` | `google.protobuf.MethodOptions` | 51370 | `Rpc` |
| `hearth_selector` | `google.protobuf.FieldOptions` | 51371 | `string` |
| `hearth_event` | `google.protobuf.MessageOptions` | 51372 | `Event` |

| Message | Field | Number | Type | Required |
| --- | --- | --- | --- | --- |
| `Rpc` | `permission` | 1 | `string` | yes |
| `Rpc` | `effect` | 2 | `Effect` | yes |
| `Rpc` | `expose` | 3 | `repeated Expose` | yes, not empty |
| `Rpc` | `agent_description` | 4 | `string` | no |
| `Rpc` | `examples` | 5 | `repeated string` | no |
| `Event` | `object` | 1 | `string` | yes |
| `Event` | `verb` | 2 | `string` | yes |
| `Event` | `selector` | 3 | `string` | no |
| `Event` | `read_permission` | 4 | `string` | yes |

The numbers 51370 to 51379 are hearth's in-house range until the Protobuf
global extension registry grants a number. No other use of that range is
allowed.

```proto
message BookmarkCreated {
  option (frostyard.hearth.v1.hearth_event) = {
    object: "bookmark"
    verb: "created"
    selector: "folder"
    read_permission: "marks:read"
  };
  string id = 1;
}

service Marks {
  rpc ListBookmarks(ListBookmarksRequest) returns (ListBookmarksResponse) {
    option (frostyard.hearth.v1.hearth_rpc) = {
      permission: "marks:read"
      effect: EFFECT_READ
      expose: EXPOSE_UI
      expose: EXPOSE_AGENT
      agent_description: "List the bookmarks of a folder."
      examples: "{\"folder\": {\"name\": \"reading\"}}"
    };
  }
}
```

### Plugin parameters

| Parameter | Required | Pattern |
| --- | --- | --- |
| `app` | yes | `^[a-z][a-z0-9]{0,15}$` |
| `version` | yes | `^(0\|[1-9][0-9]*)\.(0\|[1-9][0-9]*)\.(0\|[1-9][0-9]*)$` |

Any other parameter is an error. The parameters arrive as
`app=<id>,version=<major.minor.patch>`.

### proto3 optional

The plugin declares `FEATURE_PROTO3_OPTIONAL` in its
`CodeGeneratorResponse`. Without that feature protoc and buf refuse the plugin
with an error that names the file with the `optional` field, so a hearth app
may use a proto3 `optional` field.

## Rules

Only the files in `file_to_generate` are read. Generation fails on each of these
rules, reports every problem it finds at once, 1 line each, sorted, and each
line is prefixed with the full name of the element it is about. No file is
written when a rule fails.

### An RPC

1. The method has `hearth_rpc`; without it, generation fails with
   `hearth_rpc is required`.
2. `permission` is not empty and matches `^<app>:[a-z][a-z0-9_]*$`, where
   `<app>` is the `app` parameter.
3. `effect` is not `EFFECT_UNSPECIFIED`.
4. `expose` is not empty, holds no `EXPOSE_UNSPECIFIED` and holds no value
   twice.
5. Each `examples[i]` is a JSON object in the proto3 JSON form of the request
   message.
6. For an RPC that `expose` holds `agent` for:
   - it is not client-streaming, server-streaming or bidi-streaming;
   - its request message and its response message, followed recursively through
     message fields (a cycle stops the walk), hold no
     `google.protobuf.Any` field and no map whose value is a message.
7. Every field with `hearth_selector` in the request message, followed
   recursively through message fields (a cycle stops the walk), is a selector
   at its dotted path of proto field names, such as `folder.name`. The
   selector's name matches `^[a-z][a-z0-9_]*$`, and the field is a singular
   `string`: not repeated and not a map.
   A selector must be on a path of **singular** message fields. Generation
   fails when a selector field is found inside a map value message or inside a
   repeated message field, followed recursively, with
   `field <path> carries the selector "<name>" inside a map or a repeated field; a selector must be on a path of singular fields`:
   a map value or a repeated message has no single value for the permission
   check to read.
8. The tool name is the method name in snake_case: an uppercase letter starts a
   word (`CreateBookmark` is `create_bookmark`, and `GetURL` is `get_url`).
   2 agent RPCs with the same tool name are an error.

### An event

9. `object` and `verb` match `^[a-z][a-z0-9_]*$`; `selector` is empty or
   matches `^[a-z][a-z0-9_]*$`; `read_permission` matches the permission
   pattern of rule 2.
10. The event's type is `org.frostyard.<app>.<object>.<verb>`, and no 2 events
    share a type. Its topic is `<app>.<object>.<verb>`, plus `.<selector>` when
    `selector` is not empty.
11. A read permission that a selector scopes is carried by the topic: let S be
    the set of selector names of the RPCs whose permission equals the event's
    `read_permission`. When S is not empty, S is exactly
    `{event.selector}`, or generation fails with
    `read permission <p> is scoped by selector(s) <S>, which the topic <topic> does not carry`.
    When S is empty, the event may carry any selector, or none.

### The model

- `capabilities` is the sorted set of every RPC permission and every event read
  permission.
- `selectors` is the sorted set of every selector name of the RPC fields and of
  the events.

## Derived artifacts

| Artifact | Derivation |
| --- | --- |
| `gen/go/frostyard/hearth/v1/options.pb.go` | `buf generate` with the pinned `protoc-gen-go` |
| `cmd/protoc-gen-hearth-manifest/testdata/golden/<case>/manifest.json` | the plugin's output on that case, committed by `make generate` |
| `testdata/e2e/gen/manifest.json` | `buf generate --template testdata/e2e/buf.gen.yaml`, committed |
| every app's `manifest.json` | its own `make generate`, with its `app` and `version` |

## References

- Rationale: [ADR-0004](../adr/0004-generate-the-manifest-from-protobuf-options.md)
- Context: [design/overview.md](../design/overview.md)
- The format it feeds: [specs/manifest.md](manifest.md)
- Builds on: [frostyard/core ADR-0085](https://github.com/frostyard/core/blob/main/docs/adr/0085-build-new-first-party-apps-on-hearth-with-lodge-as-their-hub.md)
