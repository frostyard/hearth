// Package protoschema builds the JSON Schema of the proto3 JSON form of 1
// message, for the input and output schema of a generated MCP tool.
//
// A tool's arguments are what an agent sends, so the schema is the draft
// 2020-12 mapping of docs/specs/mcp-tools.md: an object whose properties are
// the message's JSON names, 1 schema for each field, and no property that the
// message does not declare. A recursive message is a definition under $defs
// with a $ref, so that the schema ends.
package protoschema

import (
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// draft is the JSON Schema draft of every schema this package writes.
const draft = "https://json-schema.org/draft/2020-12/schema"

// wellKnownPrefix is the package of the well-known types, which have their own
// mapping and are never expanded from their fields.
const wellKnownPrefix = "google.protobuf."

// nullValueName is the full name of google.protobuf.NullValue, an enum whose
// proto3 JSON form is the JSON null.
const nullValueName protoreflect.FullName = wellKnownPrefix + "NullValue"

// ForMessage returns the JSON Schema of the proto3 JSON form of 1 message, as
// canonical JSON: the object keys sorted, 2 spaces of indent, and no trailing
// newline. The result is deterministic: the same message gives the same bytes.
//
// The message must have a JSON object form, because a tool's arguments and
// result are JSON objects: a well-known type that protojson writes as a string,
// a number, an array or a JSON null (Timestamp, Duration, FieldMask, Value,
// ListValue, every wrapper type) has no such schema, and ForMessage refuses it.
//
// A root message that is a well-known type with an object form uses that type's
// JSON mapping, and not its descriptor's fields: a Struct is any JSON object, so
// its schema is 1 object with no fixed property, and 1 built from its `fields`
// map would refuse the object that protojson writes. The same rule holds for a
// Struct and an Empty field of another message.
func ForMessage(md protoreflect.MessageDescriptor) ([]byte, error) {
	if md == nil {
		return nil, fmt.Errorf("build the schema: the message is nil")
	}
	if !HasObjectForm(md) {
		return nil, fmt.Errorf("build the schema of %s: its proto3 JSON form is no object", md.FullName())
	}
	root, mapping := wellKnown(md.FullName())
	if !mapping {
		b := &builder{recursive: map[protoreflect.FullName]bool{}}
		messages := map[protoreflect.FullName]protoreflect.MessageDescriptor{}
		collect(md, messages)
		b.markRecursive(messages)

		schema, err := b.messageSchema(md)
		if err != nil {
			return nil, err
		}
		root = schema
		if len(b.defs) > 0 {
			defs := make(map[string]any, len(b.defs))
			for name := range b.defs {
				defs[string(name)] = b.defs[name]
			}
			root["$defs"] = defs
		}
	}
	root["$schema"] = draft
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("write the schema of %s: %w", md.FullName(), err)
	}
	return data, nil
}

// HasObjectForm reports whether 1 message's proto3 JSON form is 1 JSON object,
// which is what a tool's arguments and result must be. A message that is not a
// well-known type is an object, whatever its name: an ordinary message named
// "Timestamp" in a proto file with no package is no well-known type. Of the
// well-known types, Struct, Empty and Any are objects, and every other 1 is
// not.
func HasObjectForm(md protoreflect.MessageDescriptor) bool {
	if md == nil {
		return false
	}
	if schema, ok := wellKnown(md.FullName()); ok {
		return schema["type"] == "object"
	}
	return true
}

// builder builds 1 message's schema.
type builder struct {
	// recursive holds the messages that reach themselves, which the schema
	// writes as 1 definition under $defs and refers to.
	recursive map[protoreflect.FullName]bool
	// defs holds the schema of each recursive message, by full name.
	defs map[protoreflect.FullName]map[string]any
}

// collect adds 1 message and every message its fields reach, well-known types
// left out.
func collect(md protoreflect.MessageDescriptor, out map[protoreflect.FullName]protoreflect.MessageDescriptor) {
	name := md.FullName()
	if _, ok := out[name]; ok {
		return
	}
	out[name] = md
	fields := md.Fields()
	for i := range fields.Len() {
		for _, next := range messageFields(fields.Get(i)) {
			if isWellKnown(next.FullName()) {
				continue
			}
			collect(next, out)
		}
	}
}

// messageFields returns the messages of 1 field: its value's message, or the
// element's message when the field is repeated or a map.
func messageFields(fd protoreflect.FieldDescriptor) []protoreflect.MessageDescriptor {
	fd = valueField(fd)
	if fd.Kind() != protoreflect.MessageKind && fd.Kind() != protoreflect.GroupKind {
		return nil
	}
	return []protoreflect.MessageDescriptor{fd.Message()}
}

// valueField returns the field that holds 1 value: a map's value field, or the
// field itself.
func valueField(fd protoreflect.FieldDescriptor) protoreflect.FieldDescriptor {
	if fd.IsMap() {
		return fd.MapValue()
	}
	return fd
}

// markRecursive finds the messages that reach themselves through 1 or more
// fields, and puts them in the schema's definitions.
func (b *builder) markRecursive(messages map[protoreflect.FullName]protoreflect.MessageDescriptor) {
	for name := range messages {
		if b.reaches(messages, name, name, map[protoreflect.FullName]bool{}) {
			b.recursive[name] = true
		}
	}
}

// reaches reports whether the message from reaches the message target.
func (b *builder) reaches(messages map[protoreflect.FullName]protoreflect.MessageDescriptor, from, target protoreflect.FullName, seen map[protoreflect.FullName]bool) bool {
	md, ok := messages[from]
	if !ok || seen[from] {
		return false
	}
	seen[from] = true
	fields := md.Fields()
	for i := range fields.Len() {
		for _, next := range messageFields(fields.Get(i)) {
			if next.FullName() == target {
				return true
			}
			if b.reaches(messages, next.FullName(), target, seen) {
				return true
			}
		}
	}
	return false
}

// messageSchema returns the schema of 1 message's fields: an object with 1
// property for each field, and no other property.
func (b *builder) messageSchema(md protoreflect.MessageDescriptor) (map[string]any, error) {
	schema := map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": false,
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("build the schema of %s: the properties are not an object", md.FullName())
	}
	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		fieldSchema, err := b.fieldSchema(fd)
		if err != nil {
			return nil, err
		}
		properties[string(fd.JSONName())] = fieldSchema
	}

	var oneOfs []any
	for i := range md.Oneofs().Len() {
		oo := md.Oneofs().Get(i)
		if oo.IsSynthetic() {
			// A proto3 optional field's oneof is synthetic: it marks the
			// field's presence, and it is no choice of the message.
			continue
		}
		names := make([]string, 0, oo.Fields().Len())
		for j := range oo.Fields().Len() {
			names = append(names, string(oo.Fields().Get(j).JSONName()))
		}
		if len(names) == 0 {
			continue
		}
		oneOfs = append(oneOfs, oneOfConstraint(names))
	}
	switch len(oneOfs) {
	case 0:
	case 1:
		constraint, ok := oneOfs[0].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("build the schema of %s: the constraint is not an object", md.FullName())
		}
		schema["oneOf"] = constraint["oneOf"]
	default:
		allOf := make([]any, len(oneOfs))
		copy(allOf, oneOfs)
		schema["allOf"] = allOf
	}
	return schema, nil
}

// oneOfConstraint returns the constraint of 1 real oneof: exactly 1 of its
// fields, or none of them.
func oneOfConstraint(names []string) map[string]any {
	alternatives := make([]any, 0, len(names)+1)
	required := make([]any, 0, len(names))
	for _, name := range names {
		alternative := map[string]any{"required": []any{name}}
		alternatives = append(alternatives, alternative)
		required = append(required, map[string]any{"required": []any{name}})
	}
	alternatives = append(alternatives, map[string]any{"not": map[string]any{"anyOf": required}})
	return map[string]any{"oneOf": alternatives}
}

// fieldSchema returns the schema of 1 field, with its cardinality.
func (b *builder) fieldSchema(fd protoreflect.FieldDescriptor) (any, error) {
	switch {
	case fd.IsMap():
		value, err := b.valueSchema(fd.MapValue())
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "object", "additionalProperties": value}, nil
	case fd.IsList():
		item, err := b.valueSchema(fd)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "array", "items": item}, nil
	default:
		return b.valueSchema(fd)
	}
}

// valueSchema returns the schema of 1 value of a field.
func (b *builder) valueSchema(fd protoreflect.FieldDescriptor) (any, error) {
	fd = valueField(fd)
	if fd.Kind() == protoreflect.EnumKind && fd.Enum().FullName() == nullValueName {
		// google.protobuf.NullValue is an enum, and its proto3 JSON form is the
		// JSON null, not a value's name.
		return map[string]any{"type": "null"}, nil
	}
	switch fd.Kind() {
	case protoreflect.BoolKind:
		return map[string]any{"type": "boolean"}, nil
	case protoreflect.EnumKind:
		values := fd.Enum().Values()
		names := make([]any, 0, values.Len())
		for i := range values.Len() {
			names = append(names, string(values.Get(i).Name()))
		}
		return map[string]any{"type": "string", "enum": names}, nil
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return map[string]any{"type": "integer"}, nil
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return map[string]any{"type": "string", "pattern": `^-?[0-9]+$`}, nil
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return map[string]any{"type": "string", "pattern": `^[0-9]+$`}, nil
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return map[string]any{"type": "number"}, nil
	case protoreflect.StringKind:
		return map[string]any{"type": "string"}, nil
	case protoreflect.BytesKind:
		return map[string]any{"type": "string", "contentEncoding": "base64"}, nil
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return b.messageValue(fd.Message())
	}
	return nil, fmt.Errorf("build the schema of the field %s: its kind %v is not a proto3 JSON value", fd.FullName(), fd.Kind())
}

// messageValue returns the schema of 1 message value: a well-known type's own
// mapping, a definition of a recursive message, or the message's schema.
func (b *builder) messageValue(md protoreflect.MessageDescriptor) (any, error) {
	if schema, ok := wellKnown(md.FullName()); ok {
		return schema, nil
	}
	if b.recursive[md.FullName()] {
		return b.definition(md)
	}
	return b.messageSchema(md)
}

// definition returns the reference to 1 recursive message's definition, and
// adds that definition when it is not there.
func (b *builder) definition(md protoreflect.MessageDescriptor) (any, error) {
	name := md.FullName()
	if b.defs == nil {
		b.defs = map[protoreflect.FullName]map[string]any{}
	}
	if _, ok := b.defs[name]; !ok {
		// Add the place first, so that a message that reaches itself while its
		// own definition is built finds the reference and not a loop.
		b.defs[name] = nil
		schema, err := b.messageSchema(md)
		if err != nil {
			return nil, err
		}
		b.defs[name] = schema
	}
	return map[string]any{"$ref": "#/$defs/" + string(name)}, nil
}

// isWellKnown reports whether 1 full name is in the package of the well-known
// types.
func isWellKnown(name protoreflect.FullName) bool {
	return strings.HasPrefix(string(name), wellKnownPrefix)
}

// wellKnown returns the schema of 1 well-known type, and whether the message is
// 1.
//
// Only a full name that starts with the package of the well-known types is 1: an
// ordinary message named "Empty", in a proto file with no package, is no
// google.protobuf.Empty, and it keeps its own fields.
func wellKnown(name protoreflect.FullName) (map[string]any, bool) {
	if !isWellKnown(name) {
		return nil, false
	}
	switch strings.TrimPrefix(string(name), wellKnownPrefix) {
	case "Timestamp":
		return map[string]any{"type": "string", "format": "date-time"}, true
	case "Duration":
		return map[string]any{"type": "string", "pattern": `^-?[0-9]+(\.[0-9]+)?s$`}, true
	case "FieldMask":
		return map[string]any{"type": "string"}, true
	case "Struct":
		return map[string]any{"type": "object"}, true
	case "Value":
		return map[string]any{}, true
	case "ListValue":
		return map[string]any{"type": "array"}, true
	case "Empty":
		return map[string]any{"type": "object"}, true
	case "DoubleValue", "FloatValue":
		return map[string]any{"type": "number"}, true
	case "Int64Value", "UInt64Value":
		return map[string]any{"type": "string", "pattern": `^-?[0-9]+$`}, true
	case "Int32Value", "UInt32Value":
		return map[string]any{"type": "integer"}, true
	case "BoolValue":
		return map[string]any{"type": "boolean"}, true
	case "StringValue":
		return map[string]any{"type": "string"}, true
	case "BytesValue":
		return map[string]any{"type": "string", "contentEncoding": "base64"}, true
	case "Any":
		return map[string]any{"type": "object"}, true
	}
	return nil, false
}
