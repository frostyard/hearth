// Package mcpserver serves an app's generated MCP tools over the Go MCP SDK
// (github.com/modelcontextprotocol/go-sdk).
//
// 1 tool is 1 RPC that the app exposes to agents: its name, its JSON Schemas,
// the effect that becomes its hints, and the call itself. The server puts the
// caller in the call's context, so that the generated permission check runs
// inside it.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/frostyard/hearth/authz"
)

// Effect is what 1 tool call does, and it decides the tool's hints.
type Effect string

// The 3 effects, as docs/specs/options.md names them.
const (
	// EffectRead reads data and changes nothing.
	EffectRead Effect = "read"
	// EffectWrite changes data reversibly.
	EffectWrite Effect = "write"
	// EffectOutward is irreversible, or leaves the system.
	EffectOutward Effect = "outward"
)

// emptyObject is the arguments of a tool call that sends none.
const emptyObject = "{}"

// Tool is 1 MCP tool of an app: 1 RPC that the app exposes to agents.
type Tool struct {
	// Name is the tool's name: the RPC name in snake_case.
	Name string
	// Description tells an agent when to call the tool.
	Description string
	// Effect is what the call does; it decides the tool's hints.
	Effect Effect
	// InputSchema is the JSON Schema of the call's arguments.
	InputSchema json.RawMessage
	// OutputSchema is the JSON Schema of the call's result, or nil.
	OutputSchema json.RawMessage
	// Call runs the call with the caller in its context, and returns the
	// response. It decodes args, which are 1 JSON object in the proto3 JSON
	// form of the RPC's request, and reports an error for empty or malformed
	// arguments.
	Call func(ctx context.Context, args json.RawMessage) (proto.Message, error)
}

// Annotations returns the hints of 1 effect:
//
//	read:    readOnlyHint true, destructiveHint false, openWorldHint false
//	write:   readOnlyHint false, destructiveHint false, openWorldHint false
//	outward: readOnlyHint false, destructiveHint true, openWorldHint true
//
// An effect the package does not know gets an outward tool's hints, so that a
// later effect never arrives as a harmless 1.
func Annotations(effect Effect) *mcp.ToolAnnotations {
	switch effect {
	case EffectRead:
		return &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: hint(false), OpenWorldHint: hint(false)}
	case EffectWrite:
		return &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: hint(false), OpenWorldHint: hint(false)}
	case EffectOutward:
		return &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: hint(true), OpenWorldHint: hint(true)}
	}
	return &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: hint(true), OpenWorldHint: hint(true)}
}

// hint returns 1 hint's pointer. The MCP schema reads an absent destructive or
// open-world hint as true, so both are always written.
func hint(value bool) *bool {
	return &value
}

// NewServer returns an MCP server with 1 tool for each tool of tools, and the
// caller of every call.
//
// The tools are registered with the SDK's raw handler: the server validates no
// argument and no result, and each call passes its arguments to the tool as
// they arrive.
func NewServer(impl *mcp.Implementation, caller authz.Caller, tools ...[]Tool) *mcp.Server {
	server := mcp.NewServer(impl, nil)
	for _, group := range tools {
		for _, tool := range group {
			sdkTool := &mcp.Tool{
				Name:         tool.Name,
				Description:  tool.Description,
				InputSchema:  tool.InputSchema,
				Annotations:  Annotations(tool.Effect),
				OutputSchema: tool.OutputSchema,
			}
			server.AddTool(sdkTool, handler(caller, tool))
		}
	}
	return server
}

// handler returns the SDK handler of 1 tool: it puts the caller in the context,
// normalizes the arguments, calls the tool, and turns the result and every error
// into a tool result.
func handler(caller authz.Caller, tool Tool) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ctx = authz.WithCaller(ctx, caller)
		args := json.RawMessage(emptyObject)
		if req != nil && req.Params != nil && len(req.Params.Arguments) > 0 {
			args = req.Params.Arguments
			if string(args) == "null" {
				args = json.RawMessage(emptyObject)
			}
		}
		if !jsonObject(args) {
			return failed("invalid arguments: the arguments are not 1 JSON object"), nil
		}
		if tool.Call == nil {
			return failed("the tool " + tool.Name + " has no call"), nil
		}
		response, err := tool.Call(ctx, args)
		if err != nil {
			return failed(errorText(err)), nil
		}
		return succeeded(response)
	}
}

// jsonObject reports whether args are 1 JSON object.
func jsonObject(args json.RawMessage) bool {
	if !json.Valid(args) {
		return false
	}
	var object map[string]any
	if err := json.Unmarshal(args, &object); err != nil {
		return false
	}
	return object != nil
}

// succeeded returns the result of 1 call: the response as the structured
// content, and the same JSON as 1 text content.
func succeeded(response proto.Message) (*mcp.CallToolResult, error) {
	if response == nil {
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: emptyObject}},
			StructuredContent: map[string]any{},
		}, nil
	}
	data, err := protojson.Marshal(response)
	if err != nil {
		return failed("write the response: " + err.Error()), nil
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		return failed("write the response: " + err.Error()), nil
	}
	text, err := json.Marshal(object)
	if err != nil {
		return failed("write the response: " + err.Error()), nil
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(text)}},
		StructuredContent: object,
	}, nil
}

// failed returns 1 tool error: 1 text content, with the error flag set. A tool
// error is not a protocol error, so the agent reads it and can correct the call.
func failed(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
		IsError: true,
	}
}

// errorText returns the text of 1 call's error: a refused check's own text, and
// every other error's text.
func errorText(err error) string {
	var denied *authz.DeniedError
	if errors.As(err, &denied) {
		return denied.Error()
	}
	return err.Error()
}

// ServeStdio runs server on 1 standard input and 1 standard output, as an app's
// stdio MCP server.
//
// The transport is newline-delimited JSON over those 2 streams, and it needs no
// token: the process belongs to the local user, so build the server with
// authz.LocalCaller, and its tools act as that user. ServeStdio returns when the
// stream ends or the context is cancelled.
func ServeStdio(ctx context.Context, server *mcp.Server, in io.ReadCloser, out io.WriteCloser) error {
	if server == nil {
		return errors.New("serve MCP over stdio: the server is nil")
	}
	transport := &mcp.IOTransport{Reader: in, Writer: out}
	if err := server.Run(ctx, transport); err != nil {
		return fmt.Errorf("serve MCP over stdio: %w", err)
	}
	return nil
}
