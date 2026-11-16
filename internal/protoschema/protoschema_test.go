package protoschema_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bufbuild/protocompile"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/frostyard/hearth/internal/protoschema"
)

// source is the test app: 1 message per mapping of docs/specs/mcp-tools.md.
const source = `syntax = "proto3";
package frostyard.schema.v1;
import "google/protobuf/duration.proto";
import "google/protobuf/empty.proto";
import "google/protobuf/field_mask.proto";
import "google/protobuf/struct.proto";
import "google/protobuf/timestamp.proto";
import "google/protobuf/wrappers.proto";
option go_package = "example.com/schema/v1;schemav1";

enum Color {
  COLOR_UNSPECIFIED = 0;
  COLOR_RED = 1;
}

message Note {
  string id = 1;
}

message Node {
  string id = 1;
  Node child = 2;
}

message Tree {
  Node root = 1;
}

message Everything {
  int32 my_i32 = 1;
  sint32 my_s32 = 2;
  sfixed32 my_sf32 = 3;
  uint32 my_u32 = 4;
  fixed32 my_f32 = 5;
  int64 my_i64 = 6;
  uint64 my_u64 = 7;
  sint64 my_s64 = 8;
  float my_float = 9;
  double my_double = 10;
  bool my_bool = 11;
  string my_string = 12;
  bytes my_bytes = 13;
  Color color = 14;
  repeated string my_list = 15;
  map<string, int32> my_map = 16;
  map<string, Note> my_notes = 17;
  Note note = 18;
  optional string my_optional = 19;
  oneof choice {
    string choice_a = 20;
    int32 choice_b = 21;
  }
  oneof second {
    bool second_a = 22;
    string second_b = 23;
  }
  google.protobuf.Timestamp at = 24;
  google.protobuf.Duration duration = 25;
  google.protobuf.FieldMask mask = 26;
  google.protobuf.Struct struct = 27;
  google.protobuf.Value value = 28;
  google.protobuf.ListValue list_value = 29;
  google.protobuf.Empty empty = 30;
  google.protobuf.Int64Value i64_value = 31;
  google.protobuf.StringValue string_value = 32;
  google.protobuf.BoolValue bool_value = 33;
}
`

// descriptors compiles source and returns its messages, by name.
func descriptors(t *testing.T) map[string]protoreflect.MessageDescriptor {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "schema.proto"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	compiler := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
			ImportPaths: []string{dir},
		}),
	}
	files, err := compiler.Compile(t.Context(), "schema.proto")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out := map[string]protoreflect.MessageDescriptor{}
	for _, file := range files {
		messages := file.Messages()
		for i := range messages.Len() {
			md := messages.Get(i)
			out[string(md.Name())] = md
		}
	}
	return out
}

// property returns the schema of 1 property of 1 message's schema.
func property(t *testing.T, messages map[string]protoreflect.MessageDescriptor, message, name string) map[string]any {
	t.Helper()
	schema := schemaOf(t, messages, message)
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("%s: the schema has no properties object", message)
	}
	value, ok := properties[name]
	if !ok {
		t.Fatalf("%s: the schema has no property %q", message, name)
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s: the property %q is %T, want an object", message, name, value)
	}
	return object
}

// schemaOf returns 1 message's schema as a decoded object.
func schemaOf(t *testing.T, messages map[string]protoreflect.MessageDescriptor, message string) map[string]any {
	t.Helper()
	data, err := protoschema.ForMessage(messages[message])
	if err != nil {
		t.Fatalf("ForMessage(%s): %v", message, err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("%s: the schema is not JSON: %v", message, err)
	}
	return schema
}

// TestScalars checks the mapping of each scalar kind, under its JSON name.
func TestScalars(t *testing.T) {
	messages := descriptors(t)
	for name, want := range map[string]map[string]any{
		"myI32":    {"type": "integer"},
		"myS32":    {"type": "integer"},
		"mySf32":   {"type": "integer"},
		"myU32":    {"type": "integer"},
		"myF32":    {"type": "integer"},
		"myI64":    {"type": "string", "pattern": `^-?[0-9]+$`},
		"myU64":    {"type": "string", "pattern": `^[0-9]+$`},
		"myS64":    {"type": "string", "pattern": `^-?[0-9]+$`},
		"myFloat":  {"type": "number"},
		"myDouble": {"type": "number"},
		"myBool":   {"type": "boolean"},
		"myString": {"type": "string"},
		"myBytes":  {"type": "string", "contentEncoding": "base64"},
		"color":    {"type": "string", "enum": []any{"COLOR_UNSPECIFIED", "COLOR_RED"}},
	} {
		got := property(t, messages, "Everything", name)
		if len(got) != len(want) {
			t.Errorf("%s = %v, want %v", name, got, want)
			continue
		}
		for key, value := range want {
			if !equalJSON(got[key], value) {
				t.Errorf("%s[%s] = %v, want %v", name, key, got[key], value)
			}
		}
	}
}

// equalJSON compares 2 decoded JSON values.
func equalJSON(a, b any) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(left, right)
}

// TestCollections checks a repeated field, a map of a scalar and a map of a
// message.
func TestCollections(t *testing.T) {
	messages := descriptors(t)
	list := property(t, messages, "Everything", "myList")
	if list["type"] != "array" {
		t.Errorf("myList.type = %v, want array", list["type"])
	}
	items, ok := list["items"].(map[string]any)
	if !ok || items["type"] != "string" {
		t.Errorf("myList.items = %v, want a string schema", list["items"])
	}

	labels := property(t, messages, "Everything", "myMap")
	if labels["type"] != "object" {
		t.Errorf("myMap.type = %v, want object", labels["type"])
	}
	values, ok := labels["additionalProperties"].(map[string]any)
	if !ok || values["type"] != "integer" {
		t.Errorf("myMap.additionalProperties = %v, want an integer schema", labels["additionalProperties"])
	}

	notes := property(t, messages, "Everything", "myNotes")
	note, ok := notes["additionalProperties"].(map[string]any)
	if !ok || note["type"] != "object" {
		t.Fatalf("myNotes.additionalProperties = %v, want an object schema", notes["additionalProperties"])
	}
	if _, ok := note["additionalProperties"].(bool); !ok {
		t.Errorf("myNotes' value schema has no additionalProperties: false")
	}
}

// TestMessageAndOptional checks a nested message and a proto3 optional field.
func TestMessageAndOptional(t *testing.T) {
	messages := descriptors(t)
	note := property(t, messages, "Everything", "note")
	if note["type"] != "object" || note["additionalProperties"] != false {
		t.Errorf("note = %v, want an object that admits no other property", note)
	}
	optional := property(t, messages, "Everything", "myOptional")
	if optional["type"] != "string" {
		t.Errorf("myOptional = %v, want a string schema", optional)
	}
	if _, ok := schemaOf(t, messages, "Everything")["oneOf"]; ok {
		t.Error("the schema of Everything has a top-level oneOf, and its only singular oneof is the synthetic 1 of my_optional")
	}
}

// TestOneof checks the constraint of a real oneof: none or exactly 1 of its
// fields.
func TestOneof(t *testing.T) {
	messages := descriptors(t)
	schema := schemaOf(t, messages, "Everything")
	allOf, ok := schema["allOf"].([]any)
	if !ok {
		t.Fatalf("the schema has no allOf for its 2 oneofs: %v", schema["allOf"])
	}
	if len(allOf) != 2 {
		t.Fatalf("allOf holds %d constraint(s), want 2", len(allOf))
	}
	first, ok := allOf[0].(map[string]any)
	if !ok {
		t.Fatalf("allOf[0] = %v", allOf[0])
	}
	alternatives, ok := first["oneOf"].([]any)
	if !ok {
		t.Fatalf("allOf[0].oneOf = %v", first["oneOf"])
	}
	if len(alternatives) != 3 {
		t.Fatalf("the oneof has %d alternative(s), want 3: each of 2 fields, and none", len(alternatives))
	}
	last, ok := alternatives[2].(map[string]any)
	if !ok {
		t.Fatalf("the last alternative = %v", alternatives[2])
	}
	if _, ok := last["not"].(map[string]any); !ok {
		t.Errorf("the last alternative = %v, want a not", last)
	}
}

// TestRecursive checks a message that reaches itself: 1 definition under $defs,
// and a $ref from the field.
func TestRecursive(t *testing.T) {
	messages := descriptors(t)
	tree := schemaOf(t, messages, "Tree")
	root := property(t, messages, "Tree", "root")
	if got, want := root["$ref"], "#/$defs/frostyard.schema.v1.Node"; got != want {
		t.Fatalf("Tree.root = %v, want the reference %q", root, want)
	}
	defs, ok := tree["$defs"].(map[string]any)
	if !ok {
		t.Fatalf("Tree has no $defs: %v", tree["$defs"])
	}
	node, ok := defs["frostyard.schema.v1.Node"].(map[string]any)
	if !ok {
		t.Fatalf("$defs holds no Node: %v", defs)
	}
	properties, ok := node["properties"].(map[string]any)
	if !ok {
		t.Fatalf("the Node definition has no properties: %v", node)
	}
	child, ok := properties["child"].(map[string]any)
	if !ok || child["$ref"] != "#/$defs/frostyard.schema.v1.Node" {
		t.Errorf("Node.child = %v, want the reference to itself", properties["child"])
	}
	if _, ok := schemaOf(t, messages, "Note")["$defs"]; ok {
		t.Error("the schema of a message that reaches nothing has $defs")
	}
}

// TestWellKnown checks the mapping of the well-known types.
func TestWellKnown(t *testing.T) {
	messages := descriptors(t)
	for name, want := range map[string]map[string]any{
		"at":          {"type": "string", "format": "date-time"},
		"duration":    {"type": "string", "pattern": `^-?[0-9]+(\.[0-9]+)?s$`},
		"mask":        {"type": "string"},
		"struct":      {"type": "object"},
		"value":       {},
		"listValue":   {"type": "array"},
		"empty":       {"type": "object"},
		"i64Value":    {"type": "string", "pattern": `^-?[0-9]+$`},
		"stringValue": {"type": "string"},
		"boolValue":   {"type": "boolean"},
	} {
		got := property(t, messages, "Everything", name)
		if len(got) != len(want) {
			t.Errorf("%s = %v, want %v", name, got, want)
			continue
		}
		for key, value := range want {
			if !equalJSON(got[key], value) {
				t.Errorf("%s[%s] = %v, want %v", name, key, got[key], value)
			}
		}
	}
}

// TestRoot checks the root of every schema: an object with no other property,
// and the draft.
func TestRoot(t *testing.T) {
	messages := descriptors(t)
	schema := schemaOf(t, messages, "Note")
	if got, want := schema["$schema"], "https://json-schema.org/draft/2020-12/schema"; got != want {
		t.Errorf("$schema = %v, want %v", got, want)
	}
	if schema["type"] != "object" {
		t.Errorf("type = %v, want object", schema["type"])
	}
	if schema["additionalProperties"] != false {
		t.Errorf("additionalProperties = %v, want false", schema["additionalProperties"])
	}
}

// TestDeterministic checks that the same message gives the same bytes.
func TestDeterministic(t *testing.T) {
	messages := descriptors(t)
	first, err := protoschema.ForMessage(messages["Everything"])
	if err != nil {
		t.Fatalf("ForMessage: %v", err)
	}
	second, err := protoschema.ForMessage(messages["Everything"])
	if err != nil {
		t.Fatalf("ForMessage: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Error("2 runs gave different schemas")
	}
	if bytes.Contains(first, []byte("\t")) {
		t.Error("the schema is indented with a tab")
	}
	if strings.HasSuffix(string(first), "\n") {
		t.Error("the schema ends with a newline")
	}
}

// TestSchemasCompile checks that every generated schema compiles as a draft
// 2020-12 schema, and that it refuses a property the message does not declare.
func TestSchemasCompile(t *testing.T) {
	messages := descriptors(t)
	for name, md := range messages {
		data, err := protoschema.ForMessage(md)
		if err != nil {
			t.Fatalf("ForMessage(%s): %v", name, err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s: the schema is not JSON: %v", name, err)
		}
		compiler := jsonschema.NewCompiler()
		const id = "https://example.com/schema.json"
		if err := compiler.AddResource(id, doc); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		schema, err := compiler.Compile(id)
		if err != nil {
			t.Fatalf("%s: the schema does not compile: %v", name, err)
		}
		for _, instance := range []string{`{}`, `{"unknown": 1}`} {
			value, err := jsonschema.UnmarshalJSON(strings.NewReader(instance))
			if err != nil {
				t.Fatal(err)
			}
			err = schema.Validate(value)
			if instance == `{}` && err != nil {
				t.Errorf("%s: the empty object does not validate: %v", name, err)
			}
			if instance != `{}` && err == nil {
				t.Errorf("%s: an undeclared property validates", name)
			}
		}
	}
}

// TestOneofValidates checks the oneof constraint on 1 instance: 2 fields of the
// same oneof do not validate.
func TestOneofValidates(t *testing.T) {
	messages := descriptors(t)
	data, err := protoschema.ForMessage(messages["Everything"])
	if err != nil {
		t.Fatalf("ForMessage: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const id = "https://example.com/everything.json"
	if err := compiler.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(id)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, tc := range []struct {
		instance string
		valid    bool
	}{
		{`{"choiceA": "x"}`, true},
		{`{}`, true},
		{`{"choiceA": "x", "choiceB": 1}`, false},
		{`{"choiceA": "x", "secondA": true}`, true},
	} {
		value, err := jsonschema.UnmarshalJSON(strings.NewReader(tc.instance))
		if err != nil {
			t.Fatal(err)
		}
		err = schema.Validate(value)
		if tc.valid && err != nil {
			t.Errorf("%s does not validate: %v", tc.instance, err)
		}
		if !tc.valid && err == nil {
			t.Errorf("%s validates", tc.instance)
		}
	}
}
