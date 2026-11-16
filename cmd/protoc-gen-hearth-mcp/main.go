// Command protoc-gen-hearth-mcp writes an app's MCP tools from the Protobuf
// options of its service.
//
// For each service with at least 1 RPC that an agent reaches (expose holds
// EXPOSE_AGENT), it writes the part of the API that agents reach, the input and
// output schema of each tool, and the function that returns the tools. 1 tool is
// 1 agent RPC: its name, its effect as the tool's hints, its description and its
// schemas come from the model, so that the tools cannot drift from the manifest.
//
// docs/specs/mcp-tools.md is the contract of the generated code.
package main

import (
	"bytes"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/pluginpb"

	"github.com/frostyard/hearth/internal/model"
	"github.com/frostyard/hearth/internal/protoschema"
)

// The imports of the generated code.
const (
	contextImport   = protogen.GoImportPath("context")
	jsonImport      = protogen.GoImportPath("encoding/json")
	fmtImport       = protogen.GoImportPath("fmt")
	protoImport     = protogen.GoImportPath("google.golang.org/protobuf/proto")
	protojsonImport = protogen.GoImportPath("google.golang.org/protobuf/encoding/protojson")
	mcpImport       = protogen.GoImportPath("github.com/frostyard/hearth/mcpserver")
)

func main() {
	protogen.Options{ParamFunc: acceptParam}.Run(generate)
}

// acceptParam accepts every plugin parameter. The model reads them itself, so
// that 1 reading checks them for both the plugin and its tests.
func acceptParam(_, _ string) error {
	return nil
}

// Generate generates 1 plugin request. It is the plugin's whole work, without
// the standard input and output, so that a test runs it in memory.
func Generate(req *pluginpb.CodeGeneratorRequest) (*pluginpb.CodeGeneratorResponse, error) {
	gen, err := protogen.Options{ParamFunc: acceptParam}.New(req)
	if err != nil {
		return nil, fmt.Errorf("read the plugin request: %w", err)
	}
	if err := generate(gen); err != nil {
		return nil, err
	}
	return gen.Response(), nil
}

// generate builds the app's model, and writes 1 file for each generated file
// that holds a service with at least 1 agent RPC.
func generate(gen *protogen.Plugin) error {
	// protoc and buf refuse a plugin that does not declare this feature for a
	// file with a proto3 optional field, so both main and Generate declare it.
	gen.SupportedFeatures = uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL)
	app, err := model.BuildGo(gen)
	if err != nil {
		return err
	}
	operations := make(map[protoreflect.FullName]model.Operation, len(app.Operations))
	for _, op := range app.Operations {
		operations[op.Method.FullName()] = op
	}

	// Check every agent RPC before any file is written, so that a refused app
	// writes nothing.
	for _, file := range gen.Files {
		if !file.Generate {
			continue
		}
		for _, service := range file.Services {
			for _, method := range agentMethods(service, operations) {
				if err := checkAgentRPC(method); err != nil {
					return err
				}
			}
		}
	}

	for _, file := range gen.Files {
		if !file.Generate {
			continue
		}
		if err := generateFile(gen, file, operations); err != nil {
			return err
		}
	}
	return nil
}

// checkAgentRPC checks 1 agent RPC before its tool is written: a tool's
// arguments and result are JSON objects, and each example must fit the tool's
// input schema, or an agent reads an example that the call refuses.
func checkAgentRPC(method agentMethod) error {
	name := method.method.Desc.FullName()
	for _, side := range []struct {
		what    string
		message *protogen.Message
	}{
		{"request", method.method.Input},
		{"response", method.method.Output},
	} {
		if !protoschema.HasObjectForm(side.message.Desc) {
			return fmt.Errorf("%s: an agent RPC's %s %s has no JSON object form", name, side.what, side.message.Desc.FullName())
		}
	}
	data, err := protoschema.ForMessage(method.method.Input.Desc)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	schema, err := compileSchema(data)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	for i, example := range method.op.Examples {
		value, err := jsonschema.UnmarshalJSON(strings.NewReader(example))
		if err != nil {
			return fmt.Errorf("%s: example %d is no JSON: %w", name, i, err)
		}
		if err := schema.Validate(value); err != nil {
			return fmt.Errorf("%s: example %d is no call of the tool %q: %s", name, i, method.op.Tool, oneLine(err))
		}
	}
	return nil
}

// compileSchema compiles 1 tool's input schema.
func compileSchema(data []byte) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("read the tool's input schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	const id = "https://example.com/hearth/tool-input.json"
	if err := compiler.AddResource(id, doc); err != nil {
		return nil, fmt.Errorf("load the tool's input schema: %w", err)
	}
	schema, err := compiler.Compile(id)
	if err != nil {
		return nil, fmt.Errorf("compile the tool's input schema: %w", err)
	}
	return schema, nil
}

// oneLine returns 1 error's text on 1 line.
func oneLine(err error) string {
	return strings.Join(strings.Fields(err.Error()), " ")
}

// agentMethod is 1 RPC that an agent reaches, with its operation.
type agentMethod struct {
	method *protogen.Method
	op     model.Operation
}

// agentMethods returns the RPCs of 1 service that an agent reaches.
func agentMethods(service *protogen.Service, operations map[protoreflect.FullName]model.Operation) []agentMethod {
	var out []agentMethod
	for _, method := range service.Methods {
		op, ok := operations[method.Desc.FullName()]
		if ok && slices.Contains(op.Expose, model.ExposeAgent) {
			out = append(out, agentMethod{method: method, op: op})
		}
	}
	return out
}

// generateFile writes the MCP tools of 1 generated file, and nothing when no
// service of it has an agent RPC.
func generateFile(gen *protogen.Plugin, file *protogen.File, operations map[protoreflect.FullName]model.Operation) error {
	var services []*protogen.Service
	for _, service := range file.Services {
		if len(agentMethods(service, operations)) > 0 {
			services = append(services, service)
		}
	}
	if len(services) == 0 {
		return nil
	}
	out := &output{g: gen.NewGeneratedFile(file.GeneratedFilenamePrefix+"_hearth_mcp.go", file.GoImportPath)}
	out.p("// Code generated by protoc-gen-hearth-mcp. DO NOT EDIT.")
	out.p("")
	out.p("package %s", file.GoPackageName)
	out.p("")
	for _, service := range services {
		if err := out.service(service, agentMethods(service, operations)); err != nil {
			return err
		}
	}
	return nil
}

// output writes the generated file.
type output struct {
	g *protogen.GeneratedFile
}

// p writes 1 line.
func (o *output) p(format string, args ...any) {
	o.g.P(fmt.Sprintf(format, args...))
}

// ident returns 1 qualified identifier of 1 import path.
func (o *output) ident(path protogen.GoImportPath, name string) string {
	return o.g.QualifiedGoIdent(path.Ident(name))
}

// requestType returns the pointer type of 1 message.
func (o *output) requestType(message *protogen.Message) string {
	return "*" + o.g.QualifiedGoIdent(message.GoIdent)
}

// service writes 1 service's agent API and its tools.
func (o *output) service(service *protogen.Service, methods []agentMethod) error {
	name := service.GoName
	full := service.Desc.FullName()

	o.p("// %sAgentAPI is the part of %s that an agent", name, full)
	o.p("// reaches.")
	o.p("type %sAgentAPI interface {", name)
	for _, method := range methods {
		o.p("%s(%s, %s) (%s, error)", method.method.GoName,
			o.ident(contextImport, "Context"), o.requestType(method.method.Input), o.requestType(method.method.Output))
	}
	o.p("}")
	o.p("")

	for _, method := range methods {
		if err := o.schemas(name, method); err != nil {
			return err
		}
	}

	o.p("// %sTools returns the MCP tools of %s: 1 tool for", name, full)
	o.p("// each RPC that an agent reaches.")
	o.p("func %sTools(api %sAgentAPI) []%s {", name, name, o.ident(mcpImport, "Tool"))
	o.p("return []%s{", o.ident(mcpImport, "Tool"))
	for _, method := range methods {
		effect, err := effectName(method.op.Effect)
		if err != nil {
			return fmt.Errorf("%s: %w", method.method.Desc.FullName(), err)
		}
		o.p("{")
		o.p("Name: %q,", method.op.Tool)
		o.p("Description: %s,", strconv.Quote(description(method)))
		o.p("Effect: %s,", o.ident(mcpImport, effect))
		o.p("InputSchema: %s(%s),", o.ident(jsonImport, "RawMessage"), schemaName(name, method, "Input"))
		o.p("OutputSchema: %s(%s),", o.ident(jsonImport, "RawMessage"), schemaName(name, method, "Output"))
		o.p("Call: func(ctx %s, args %s) (%s, error) {", o.ident(contextImport, "Context"),
			o.ident(jsonImport, "RawMessage"), o.ident(protoImport, "Message"))
		o.p("req := &%s{}", o.g.QualifiedGoIdent(method.method.Input.GoIdent))
		o.p("if err := %s(args, req); err != nil {", o.ident(protojsonImport, "Unmarshal"))
		o.p("return nil, %s(\"invalid arguments: %%w\", err)", o.ident(fmtImport, "Errorf"))
		o.p("}")
		o.p("return api.%s(ctx, req)", method.method.GoName)
		o.p("},")
		o.p("},")
	}
	o.p("}")
	o.p("}")
	o.p("")
	return nil
}

// schemas writes the input and the output schema of 1 tool.
func (o *output) schemas(service string, method agentMethod) error {
	for _, side := range []struct {
		suffix  string
		message *protogen.Message
	}{
		{"Input", method.method.Input},
		{"Output", method.method.Output},
	} {
		data, err := protoschema.ForMessage(side.message.Desc)
		if err != nil {
			return fmt.Errorf("%s: %w", method.method.Desc.FullName(), err)
		}
		o.p("// %s is the %s schema of the tool %q.", schemaName(service, method, side.suffix),
			strings.ToLower(side.suffix), method.op.Tool)
		o.p("const %s = %s", schemaName(service, method, side.suffix), goString(string(data)))
		o.p("")
	}
	return nil
}

// schemaName returns the name of 1 tool's schema constant.
func schemaName(service string, method agentMethod, side string) string {
	return unexported(service + method.method.GoName + side + "Schema")
}

// effectName returns the name of 1 effect's constant of the mcpserver package.
func effectName(effect string) (string, error) {
	switch effect {
	case model.EffectRead:
		return "EffectRead", nil
	case model.EffectWrite:
		return "EffectWrite", nil
	case model.EffectOutward:
		return "EffectOutward", nil
	}
	return "", fmt.Errorf("the effect %q is not read, write or outward", effect)
}

// description returns what 1 tool's description says: the agent description, or
// the RPC's leading comment, and 1 example line per example.
func description(method agentMethod) string {
	text := strings.TrimSpace(method.op.AgentDescription)
	if text == "" {
		text = leadingComment(method.method)
	}
	parts := make([]string, 0, len(method.op.Examples)+1)
	if text != "" {
		parts = append(parts, text)
	}
	for _, example := range method.op.Examples {
		parts = append(parts, "Example input: "+example)
	}
	return strings.Join(parts, "\n")
}

// leadingComment returns the text of 1 method's leading comment, on 1 line. The
// Protobuf descriptors hold no comments, so a Go plugin reads them from the
// source information of its request.
func leadingComment(method *protogen.Method) string {
	var lines []string
	for line := range strings.SplitSeq(method.Comments.Leading.String(), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "//"))
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, " ")
}

// goString returns 1 string as a Go literal: a raw literal when it holds no
// backquote, and a quoted literal otherwise.
func goString(text string) string {
	if !strings.Contains(text, "`") {
		return "`" + text + "`"
	}
	return strconv.Quote(text)
}

// unexported returns 1 name with a lowercase first letter.
func unexported(name string) string {
	if name == "" {
		return name
	}
	return strings.ToLower(name[:1]) + name[1:]
}
