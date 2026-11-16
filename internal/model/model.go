// Package model builds an app's model from the Protobuf files that a hearth
// plugin is asked to generate, and checks every rule of
// docs/specs/options.md.
//
// Every plugin of hearth (the manifest, and the MCP, permission and event
// plugins of later tasks) shares this model: 1 service gives an app its
// operations, its capabilities, its selectors and its events.
package model

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	hearthv1 "github.com/frostyard/hearth/gen/go/frostyard/hearth/v1"
	"github.com/frostyard/hearth/manifest"
)

// The 3 effects, as the manifest writes them.
const (
	EffectRead    = "read"
	EffectWrite   = "write"
	EffectOutward = "outward"
)

// The 2 exposures, as the manifest writes them.
const (
	ExposeUI    = "ui"
	ExposeAgent = "agent"
)

// anyName is the full name of google.protobuf.Any, which an agent RPC must not
// carry.
const anyName protoreflect.FullName = "google.protobuf.Any"

// App is the model of 1 app, built from the Protobuf files of a plugin run.
type App struct {
	// ID is the app's id: the plugin's app parameter.
	ID string
	// Version is the app's semantic version: the plugin's version parameter.
	Version string
	// Operations are the app's RPCs, sorted by full name.
	Operations []Operation
	// Events are the app's events, sorted by type.
	Events []Event
	// Capabilities are every capability of the operations and events, sorted
	// and without a repeat.
	Capabilities []string
	// Selectors are every selector name of the operations and events, sorted
	// and without a repeat.
	Selectors []string
}

// Operation is 1 RPC of the app's service.
type Operation struct {
	// Method describes the RPC.
	Method protoreflect.MethodDescriptor
	// Name is the RPC's full name.
	Name string
	// Permission is the capability the call needs.
	Permission string
	// Effect is "read", "write" or "outward".
	Effect string
	// Expose is where the RPC is reachable: "ui", "agent", or both.
	Expose []string
	// Selectors are the request fields that carry a selector value.
	Selectors []Selector
	// ObjectIDs are the request fields that carry an object id, as dotted
	// paths of proto field names, or none. An RPC with at least 1 is an RPC on
	// an object id: its permission check needs a hook that reads the stored
	// object's selector value. Object ids are not in the manifest.
	ObjectIDs []string
	// Tool is the RPC's MCP tool name, or "" when agents do not reach it.
	Tool string
	// AgentDescription tells an agent when to call the RPC.
	AgentDescription string
	// Examples are request examples in the proto3 JSON form.
	Examples []string
}

// Selector is 1 request field that carries a selector value.
type Selector struct {
	// Field is the dotted path of proto field names.
	Field string
	// Selector is the selector's name.
	Selector string
}

// Event is 1 event that an RPC of the app publishes.
type Event struct {
	// Message describes the message that carries the event.
	Message protoreflect.MessageDescriptor
	// Type is the event's CloudEvents type.
	Type string
	// Topic is the broker topic.
	Topic string
	// Selector is the selector whose value the event carries, or "".
	Selector string
	// ReadPermission is the capability a consumer needs to read the event.
	ReadPermission string
}

// Build builds the app's model from the files of gen that are marked for
// generation, and checks every rule. It reports every problem it finds, 1 line
// each, sorted, and each line prefixed with the full name of its element. It
// reads the parameters of a hearth plugin (see ParseParams).
func Build(gen *protogen.Plugin) (*App, error) {
	return build(gen, ParseParams)
}

// BuildGo builds the app's model for a Go plugin: it is Build, and it ignores
// the standard parameters that a protoc-gen-go run sends beside hearth's own
// (see ParseGoParams), so that 1 buf template runs the manifest plugin and the
// Go plugins together.
func BuildGo(gen *protogen.Plugin) (*App, error) {
	return build(gen, ParseGoParams)
}

// build builds the app's model, with parse reading the plugin's parameters.
func build(gen *protogen.Plugin, parse func(string) (Params, error)) (*App, error) {
	params, err := parse(gen.Request.GetParameter())
	if err != nil {
		return nil, err
	}
	b := &builder{
		app:    &App{ID: params.App, Version: params.Version},
		tools:  make(map[string]string),
		events: make(map[string]string),
	}
	for _, file := range gen.Files {
		if !file.Generate {
			continue
		}
		b.addFile(file)
	}
	b.finish()
	if err := b.problems.err(); err != nil {
		return nil, err
	}
	return b.app, nil
}

// Manifest builds the app's manifest from the model.
func (a *App) Manifest() *manifest.Manifest {
	m := &manifest.Manifest{
		Hearth:       manifest.FormatMajor,
		App:          a.ID,
		Version:      a.Version,
		Capabilities: slices.Clone(a.Capabilities),
		Selectors:    slices.Clone(a.Selectors),
		Operations:   make([]manifest.Operation, len(a.Operations)),
		Events:       make([]manifest.Event, len(a.Events)),
	}
	for i, op := range a.Operations {
		selectors := make([]manifest.Selector, len(op.Selectors))
		for j, s := range op.Selectors {
			selectors[j] = manifest.Selector{Field: s.Field, Selector: s.Selector}
		}
		m.Operations[i] = manifest.Operation{
			Name:       op.Name,
			Permission: op.Permission,
			Effect:     op.Effect,
			Expose:     slices.Clone(op.Expose),
			Selectors:  selectors,
			Tool:       op.Tool,
		}
	}
	for i, event := range a.Events {
		m.Events[i] = manifest.Event{
			Type:           event.Type,
			Message:        string(event.Message.FullName()),
			Topic:          event.Topic,
			Selector:       event.Selector,
			ReadPermission: event.ReadPermission,
		}
	}
	return m
}

// builder collects the model and the problems of 1 build.
type builder struct {
	app      *App
	problems problems
	// tools maps 1 tool name to the RPC that carries it.
	tools map[string]string
	// events maps 1 event type to the message that declares it.
	events map[string]string
}

// addFile adds the services and the messages of 1 generated file.
func (b *builder) addFile(file *protogen.File) {
	for _, service := range file.Services {
		for _, method := range service.Methods {
			b.addMethod(method)
		}
	}
	for _, message := range file.Messages {
		b.addMessages(message)
	}
}

// addMessages adds the events of 1 message and of its nested messages.
func (b *builder) addMessages(message *protogen.Message) {
	b.addEvent(message)
	for _, nested := range message.Messages {
		b.addMessages(nested)
	}
}

// addMethod checks 1 RPC against rules 1 to 8 and adds it to the model.
func (b *builder) addMethod(method *protogen.Method) {
	name := string(method.Desc.FullName())
	rpc := methodRPC(method)
	if rpc == nil {
		b.problems.addf("%s: hearth_rpc is required", name)
		return
	}
	op := Operation{
		Method:           method.Desc,
		Name:             name,
		Permission:       rpc.GetPermission(),
		AgentDescription: rpc.GetAgentDescription(),
		Examples:         slices.Clone(rpc.GetExamples()),
	}

	switch {
	case op.Permission == "":
		b.problems.addf("%s: permission is required", name)
	case !b.permissionPattern().MatchString(op.Permission):
		b.problems.addf("%s: permission %q must be %q followed by \":\" and a capability of [a-z][a-z0-9_]*", name, op.Permission, b.app.ID+":")
	}

	switch rpc.GetEffect() {
	case hearthv1.Effect_EFFECT_READ:
		op.Effect = EffectRead
	case hearthv1.Effect_EFFECT_WRITE:
		op.Effect = EffectWrite
	case hearthv1.Effect_EFFECT_OUTWARD:
		op.Effect = EffectOutward
	default:
		b.problems.addf("%s: effect is required, and must not be EFFECT_UNSPECIFIED", name)
	}

	exposes := rpc.GetExpose()
	switch {
	case len(exposes) == 0:
		b.problems.addf("%s: expose is required, and must not be empty", name)
	case slices.Contains(exposes, hearthv1.Expose_EXPOSE_UNSPECIFIED):
		b.problems.addf("%s: expose holds EXPOSE_UNSPECIFIED, which is not an exposure", name)
	case repeatsExpose(exposes):
		b.problems.addf("%s: expose holds an exposure twice", name)
	}
	op.Expose = exposeNames(exposes)

	b.checkExamples(name, method, op.Examples)

	if slices.Contains(op.Expose, ExposeAgent) {
		b.checkAgentShape(name, method)
		op.Tool = snakeCase(string(method.Desc.Name()))
		if previous, ok := b.tools[op.Tool]; ok {
			b.problems.addf("%s: the tool name %q is also the tool name of %s", name, op.Tool, previous)
		} else {
			b.tools[op.Tool] = name
		}
	}

	op.Selectors = b.selectors(name, method.Desc.Input(), "", false, map[selectorVisit]bool{})
	op.ObjectIDs = objectIDs(method.Desc.Input(), "", map[selectorVisit]bool{})
	b.app.Operations = append(b.app.Operations, op)
}

// checkExamples checks rule 5: every example is a JSON object in the proto3
// JSON form of the RPC's request.
func (b *builder) checkExamples(name string, method *protogen.Method, examples []string) {
	for i, example := range examples {
		message := dynamicpb.NewMessage(method.Desc.Input())
		if err := protojson.Unmarshal([]byte(example), message); err != nil {
			b.problems.addf("%s: example %d is not a JSON object in the proto3 JSON form of %s: %v", name, i, method.Desc.Input().FullName(), err)
		}
	}
}

// checkAgentShape checks rule 6 for 1 agent RPC: it does not stream, and its
// request and response messages carry neither google.protobuf.Any nor a map
// with a message value, followed recursively.
func (b *builder) checkAgentShape(name string, method *protogen.Method) {
	if method.Desc.IsStreamingClient() || method.Desc.IsStreamingServer() {
		b.problems.addf("%s: an agent RPC must not stream", name)
		return
	}
	b.checkAgentMessage(name, method.Desc.Input(), "", map[protoreflect.FullName]bool{})
	b.checkAgentMessage(name, method.Desc.Output(), "", map[protoreflect.FullName]bool{})
}

// checkAgentMessage follows 1 message's fields for rule 6. onPath holds the
// messages of the path from the RPC's own message, so that a cycle stops and a
// message that 2 fields share is still read twice.
func (b *builder) checkAgentMessage(name string, message protoreflect.MessageDescriptor, path string, onPath map[protoreflect.FullName]bool) {
	if message.FullName() == anyName {
		b.problems.addf("%s: an agent RPC's message must not be google.protobuf.Any", name)
		return
	}
	if onPath[message.FullName()] {
		return
	}
	onPath[message.FullName()] = true
	defer delete(onPath, message.FullName())

	fields := message.Fields()
	for i := range fields.Len() {
		field := fields.Get(i)
		fieldPath := fieldName(field, path)
		if field.IsMap() {
			if field.MapValue().Kind() == protoreflect.MessageKind {
				b.problems.addf("%s: an agent RPC's message must not have a map with a message value: %s", name, fieldPath)
			}
			continue
		}
		if field.Kind() != protoreflect.MessageKind && field.Kind() != protoreflect.GroupKind {
			continue
		}
		if field.Message().FullName() == anyName {
			b.problems.addf("%s: an agent RPC's message must not carry google.protobuf.Any: %s", name, fieldPath)
			continue
		}
		b.checkAgentMessage(name, field.Message(), fieldPath, onPath)
	}
}

// selectorVisit identifies 1 step of the selector walk: a message, and whether
// the path that reaches it enters a map or a repeated field. The cycle guard
// keys on both, so that a message read on a singular path is read again once
// the path enters a compound field, and the walk still ends.
type selectorVisit struct {
	message protoreflect.FullName
	inside  bool
}

// selectors collects rule 7's selector fields of 1 request message: the fields
// with hearth_selector, followed through message fields, as dotted paths of
// proto field names. A selector must be on a path of singular fields: a map
// value or a repeated message has no single value for the permission check, so
// a selector inside 1 of them is a problem. onPath stops a cycle.
func (b *builder) selectors(name string, message protoreflect.MessageDescriptor, path string, inside bool, onPath map[selectorVisit]bool) []Selector {
	visit := selectorVisit{message: message.FullName(), inside: inside}
	if onPath[visit] {
		return nil
	}
	onPath[visit] = true
	defer delete(onPath, visit)

	var out []Selector
	fields := message.Fields()
	for i := range fields.Len() {
		field := fields.Get(i)
		fieldPath := fieldName(field, path)
		if selector, ok := selectorName(field); ok {
			switch {
			case !selectorPattern.MatchString(selector):
				b.problems.addf("%s: the selector name %q of field %s must match %s", name, selector, fieldPath, selectorPattern)
			case field.Kind() != protoreflect.StringKind || field.IsList() || field.IsMap():
				b.problems.addf("%s: field %s carries the selector %q, so it must be a singular string", name, fieldPath, selector)
			case inside:
				b.problems.addf("%s: field %s carries the selector %q inside a map or a repeated field; a selector must be on a path of singular fields", name, fieldPath, selector)
			default:
				out = append(out, Selector{Field: fieldPath, Selector: selector})
			}
		}
		if field.IsMap() {
			value := field.MapValue()
			if value.Kind() == protoreflect.MessageKind || value.Kind() == protoreflect.GroupKind {
				out = append(out, b.selectors(name, value.Message(), fieldPath, true, onPath)...)
			}
			continue
		}
		if field.Kind() != protoreflect.MessageKind && field.Kind() != protoreflect.GroupKind {
			continue
		}
		out = append(out, b.selectors(name, field.Message(), fieldPath, inside || field.IsList(), onPath)...)
	}
	return out
}

// objectIDs collects the object-id fields of 1 request message: every singular
// field named "id" or ending in "_id" that holds no message, followed through
// singular message fields, as dotted paths of proto field names. A string, a
// number, an enum and bytes are all object ids; a map and a repeated field are
// skipped, because a list has no 1 object id. onPath stops a cycle.
func objectIDs(message protoreflect.MessageDescriptor, path string, onPath map[selectorVisit]bool) []string {
	visit := selectorVisit{message: message.FullName()}
	if onPath[visit] {
		return nil
	}
	onPath[visit] = true
	defer delete(onPath, visit)

	var out []string
	fields := message.Fields()
	for i := range fields.Len() {
		field := fields.Get(i)
		if field.IsMap() || field.IsList() {
			continue
		}
		fieldPath := fieldName(field, path)
		switch field.Kind() {
		case protoreflect.MessageKind, protoreflect.GroupKind:
			out = append(out, objectIDs(field.Message(), fieldPath, onPath)...)
		default:
			name := string(field.Name())
			if name == "id" || strings.HasSuffix(name, "_id") {
				out = append(out, fieldPath)
			}
		}
	}
	return out
}

// addEvent checks 1 message against rules 9, 10 and the type of its event, and
// adds the event to the model. Rule 11 is checked once every RPC is known.
func (b *builder) addEvent(message *protogen.Message) {
	name := string(message.Desc.FullName())
	event := messageEvent(message)
	if event == nil {
		return
	}
	object, verb := event.GetObject(), event.GetVerb()
	if !objectPattern.MatchString(object) {
		b.problems.addf("%s: hearth_event.object %q must match %s", name, object, objectPattern)
	}
	if !objectPattern.MatchString(verb) {
		b.problems.addf("%s: hearth_event.verb %q must match %s", name, verb, objectPattern)
	}
	if selector := event.GetSelector(); selector != "" && !selectorPattern.MatchString(selector) {
		b.problems.addf("%s: hearth_event.selector %q must match %s", name, selector, selectorPattern)
	}
	readPermission := event.GetReadPermission()
	if !b.permissionPattern().MatchString(readPermission) {
		b.problems.addf("%s: hearth_event.read_permission %q must be %q followed by \":\" and a capability of [a-z][a-z0-9_]*", name, readPermission, b.app.ID+":")
	}

	e := Event{
		Message:        message.Desc,
		Type:           fmt.Sprintf("org.frostyard.%s.%s.%s", b.app.ID, object, verb),
		Topic:          fmt.Sprintf("%s.%s.%s", b.app.ID, object, verb),
		Selector:       event.GetSelector(),
		ReadPermission: readPermission,
	}
	if e.Selector != "" {
		e.Topic += "." + e.Selector
	}
	if previous, ok := b.events[e.Type]; ok {
		b.problems.addf("%s: the event type %q is also the event type of %s", name, e.Type, previous)
	} else {
		b.events[e.Type] = name
	}
	b.app.Events = append(b.app.Events, e)
}

// finish checks the rules that need every RPC and event (rule 11), computes
// the app's capabilities and selectors, and sorts the model.
func (b *builder) finish() {
	slices.SortFunc(b.app.Operations, func(a, c Operation) int {
		return strings.Compare(a.Name, c.Name)
	})
	slices.SortFunc(b.app.Events, func(a, c Event) int {
		return strings.Compare(a.Type, c.Type)
	})

	// The selector names of the RPCs that 1 capability scopes.
	scoped := make(map[string][]string, len(b.app.Operations))
	for _, op := range b.app.Operations {
		for _, s := range op.Selectors {
			if !slices.Contains(scoped[op.Permission], s.Selector) {
				scoped[op.Permission] = append(scoped[op.Permission], s.Selector)
			}
		}
	}
	for _, event := range b.app.Events {
		selectors := scoped[event.ReadPermission]
		if len(selectors) == 0 {
			continue
		}
		slices.Sort(selectors)
		if len(selectors) == 1 && selectors[0] == event.Selector {
			continue
		}
		b.problems.addf("%s: read permission %s is scoped by selector(s) %s, which the topic %s does not carry",
			event.Message.FullName(), event.ReadPermission, strings.Join(selectors, ", "), event.Topic)
	}

	capabilities := make([]string, 0, len(b.app.Operations)+len(b.app.Events))
	for _, op := range b.app.Operations {
		capabilities = append(capabilities, op.Permission)
	}
	for _, event := range b.app.Events {
		capabilities = append(capabilities, event.ReadPermission)
	}
	b.app.Capabilities = sortedSet(capabilities)

	selectors := make([]string, 0, len(b.app.Operations)+len(b.app.Events))
	for _, op := range b.app.Operations {
		for _, s := range op.Selectors {
			selectors = append(selectors, s.Selector)
		}
	}
	for _, event := range b.app.Events {
		selectors = append(selectors, event.Selector)
	}
	b.app.Selectors = sortedSet(selectors)
}

// permissionPattern is the pattern of a capability, once its app prefix is
// fixed: "<app>:<capability>".
func (b *builder) permissionPattern() *regexp.Regexp {
	return regexp.MustCompile("^" + regexp.QuoteMeta(b.app.ID) + ":[a-z][a-z0-9_]*$")
}

// methodRPC returns the hearth_rpc option of 1 method, or nil.
func methodRPC(method *protogen.Method) *hearthv1.Rpc {
	options, ok := method.Desc.Options().(*descriptorpb.MethodOptions)
	if !ok || options == nil || !proto.HasExtension(options, hearthv1.E_HearthRpc) {
		return nil
	}
	rpc, _ := proto.GetExtension(options, hearthv1.E_HearthRpc).(*hearthv1.Rpc)
	return rpc
}

// messageEvent returns the hearth_event option of 1 message, or nil.
func messageEvent(message *protogen.Message) *hearthv1.Event {
	options, ok := message.Desc.Options().(*descriptorpb.MessageOptions)
	if !ok || options == nil || !proto.HasExtension(options, hearthv1.E_HearthEvent) {
		return nil
	}
	event, _ := proto.GetExtension(options, hearthv1.E_HearthEvent).(*hearthv1.Event)
	return event
}

// selectorName returns the hearth_selector name of 1 field.
func selectorName(field protoreflect.FieldDescriptor) (string, bool) {
	options, ok := field.Options().(*descriptorpb.FieldOptions)
	if !ok || options == nil || !proto.HasExtension(options, hearthv1.E_HearthSelector) {
		return "", false
	}
	name, _ := proto.GetExtension(options, hearthv1.E_HearthSelector).(string)
	return name, true
}

// exposeNames returns the manifest's names of the exposures, in the manifest's
// order: ui, then agent. An unspecified exposure is not in the model.
func exposeNames(expose []hearthv1.Expose) []string {
	out := make([]string, 0, len(expose))
	for _, want := range []hearthv1.Expose{hearthv1.Expose_EXPOSE_UI, hearthv1.Expose_EXPOSE_AGENT} {
		if slices.Contains(expose, want) {
			out = append(out, exposeName(want))
		}
	}
	return out
}

// repeatsExpose reports whether 1 exposure appears twice.
func repeatsExpose(expose []hearthv1.Expose) bool {
	seen := make(map[hearthv1.Expose]bool, len(expose))
	for _, e := range expose {
		if seen[e] {
			return true
		}
		seen[e] = true
	}
	return false
}

// exposeName returns the manifest's name of 1 exposure, or "".
func exposeName(expose hearthv1.Expose) string {
	switch expose {
	case hearthv1.Expose_EXPOSE_UI:
		return ExposeUI
	case hearthv1.Expose_EXPOSE_AGENT:
		return ExposeAgent
	}
	return ""
}

// fieldName returns the dotted path of 1 field.
func fieldName(field protoreflect.FieldDescriptor, path string) string {
	if path == "" {
		return string(field.Name())
	}
	return path + "." + string(field.Name())
}

// sortedSet returns list without a repeat, sorted, as an empty slice when it
// is empty.
func sortedSet(list []string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out
}

// problems collects the problems of 1 build.
type problems struct {
	lines []string
}

// addf adds 1 problem.
func (p *problems) addf(format string, args ...any) {
	p.lines = append(p.lines, fmt.Sprintf(format, args...))
}

// err returns the problems as 1 error: 1 line each, sorted. No problem means
// no error.
func (p *problems) err() error {
	if len(p.lines) == 0 {
		return nil
	}
	slices.Sort(p.lines)
	return errors.New(strings.Join(p.lines, "\n"))
}
