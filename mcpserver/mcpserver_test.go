package mcpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/frostyard/hearth/authz"
	"github.com/frostyard/hearth/mcpserver"
)

// emptySchema is the input schema of a tool that takes no argument.
var emptySchema = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)

// callerReport is what a test tool saw: the caller of its context.
type callerReport struct {
	mutex  sync.Mutex
	caller authz.Caller
	calls  int
}

// record remembers 1 caller.
func (r *callerReport) record(caller authz.Caller) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.caller = caller
	r.calls++
}

// last returns the last caller, and the number of calls.
func (r *callerReport) last() (authz.Caller, int) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.caller, r.calls
}

// whoamiTool returns a tool that reports the caller's principal.
func whoamiTool(effect mcpserver.Effect, report *callerReport) mcpserver.Tool {
	return mcpserver.Tool{
		Name:        "whoami",
		Description: "Report the caller.",
		Effect:      effect,
		InputSchema: emptySchema,
		Call: func(ctx context.Context, _ json.RawMessage) (proto.Message, error) {
			caller, ok := authz.CallerFrom(ctx)
			if !ok {
				return nil, errors.New("the context carries no caller")
			}
			if report != nil {
				report.record(caller)
			}
			return &structpb.Struct{Fields: map[string]*structpb.Value{
				"principal": structpb.NewStringValue(caller.Principal),
			}}, nil
		},
	}
}

// newClient returns a client with the given name.
func newClient() *mcp.Client {
	return mcp.NewClient(&mcp.Implementation{Name: "hearth-test", Version: "0.0.1"}, nil)
}

// connectSession connects 1 server and 1 client over 2 in-memory transports.
func connectSession(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := t.Context()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("connect the server: %v", err)
	}
	session, err := newClient().Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect the client: %v", err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Logf("close the client session: %v", err)
		}
	})
	return session
}

// listTools returns the tools that 1 client sees.
func listTools(t *testing.T, session *mcp.ClientSession) []*mcp.Tool {
	t.Helper()
	result, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	return result.Tools
}

// testAnnotations checks the 3 hints that 1 effect gives, through a real client
// session.
func testAnnotations(t *testing.T, effect mcpserver.Effect, readOnly, destructive, openWorld bool) {
	t.Helper()
	server := mcpserver.NewServer(&mcp.Implementation{Name: "hearth-test", Version: "0.0.1"}, authz.LocalCaller(), []mcpserver.Tool{whoamiTool(effect, nil)})
	tools := listTools(t, connectSession(t, server))
	if len(tools) != 1 {
		t.Fatalf("the client sees %d tool(s), want 1", len(tools))
	}
	annotations := tools[0].Annotations
	if annotations == nil {
		t.Fatalf("the tool %q has no annotations", tools[0].Name)
	}
	if annotations.ReadOnlyHint != readOnly {
		t.Errorf("readOnlyHint = %v, want %v", annotations.ReadOnlyHint, readOnly)
	}
	if annotations.DestructiveHint == nil {
		t.Error("destructiveHint is absent, and the MCP schema reads an absent hint as true")
	} else if *annotations.DestructiveHint != destructive {
		t.Errorf("destructiveHint = %v, want %v", *annotations.DestructiveHint, destructive)
	}
	if annotations.OpenWorldHint == nil {
		t.Error("openWorldHint is absent, and the MCP schema reads an absent hint as true")
	} else if *annotations.OpenWorldHint != openWorld {
		t.Errorf("openWorldHint = %v, want %v", *annotations.OpenWorldHint, openWorld)
	}
}

// TestAnnotationsRead checks the hints of a read tool.
func TestAnnotationsRead(t *testing.T) {
	testAnnotations(t, mcpserver.EffectRead, true, false, false)
}

// TestAnnotationsWrite checks the hints of a write tool.
func TestAnnotationsWrite(t *testing.T) {
	testAnnotations(t, mcpserver.EffectWrite, false, false, false)
}

// TestAnnotationsOutward checks the hints of an outward tool.
func TestAnnotationsOutward(t *testing.T) {
	testAnnotations(t, mcpserver.EffectOutward, false, true, true)
}

// TestDeniedToolError checks that a refused call is a tool error with the
// permission runtime's own text.
func TestDeniedToolError(t *testing.T) {
	narrow, err := authz.NewGrant(authz.Item{Cap: "demo:read", Sel: map[string]string{"group": "/work"}})
	if err != nil {
		t.Fatalf("NewGrant: %v", err)
	}
	tool := mcpserver.Tool{
		Name:        "read_private",
		Description: "Read the private group.",
		Effect:      mcpserver.EffectRead,
		InputSchema: emptySchema,
		Call: func(ctx context.Context, _ json.RawMessage) (proto.Message, error) {
			grant, err := authz.GrantFrom(ctx)
			if err != nil {
				return nil, err
			}
			if err := grant.Check("demo:read", authz.Sel{Selector: "group", Path: "/private"}); err != nil {
				return nil, err
			}
			return &structpb.Struct{}, nil
		},
	}
	caller := authz.Caller{Principal: "chani", Grant: narrow}
	server := mcpserver.NewServer(&mcp.Implementation{Name: "hearth-test", Version: "0.0.1"}, caller, []mcpserver.Tool{tool})
	session := connectSession(t, server)

	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "read_private"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatal("the refused call is no tool error")
	}
	if len(result.Content) != 1 {
		t.Fatalf("the refused call has %d content item(s), want 1", len(result.Content))
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("the content is %T, want text", result.Content[0])
	}
	if want := "permission denied: needs demo:read on group=/private"; text.Text != want {
		t.Errorf("the refusal = %q, want %q", text.Text, want)
	}
}

// TestInvalidArguments checks that an argument that is no JSON object is a tool
// error.
func TestInvalidArguments(t *testing.T) {
	server := mcpserver.NewServer(&mcp.Implementation{Name: "hearth-test", Version: "0.0.1"}, authz.LocalCaller(), []mcpserver.Tool{whoamiTool(mcpserver.EffectRead, nil)})
	session := connectSession(t, server)
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "whoami", Arguments: []any{"not", "an", "object"}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatal("the bad arguments are no tool error")
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("the content is %T, want text", result.Content[0])
	}
	if want := "invalid arguments: the arguments are not 1 JSON object"; text.Text != want {
		t.Errorf("the refusal = %q, want %q", text.Text, want)
	}
}

// TestStdioLocalUser checks the stdio server: no token of any kind, the local
// user, and a tool call over 2 pipes.
func TestStdioLocalUser(t *testing.T) {
	report := &callerReport{}
	server := mcpserver.NewServer(&mcp.Implementation{Name: "hearth-test", Version: "0.0.1"}, authz.LocalCaller(), []mcpserver.Tool{whoamiTool(mcpserver.EffectRead, report)})

	clientToServer, clientToServerWriter := io.Pipe()
	serverToClient, serverToClientWriter := io.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	served := make(chan error, 1)
	go func() {
		served <- mcpserver.ServeStdio(ctx, server, clientToServer, serverToClientWriter)
	}()

	transport := &mcp.IOTransport{Reader: serverToClient, Writer: clientToServerWriter}
	session, err := newClient().Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect the client: %v", err)
	}

	tools := listTools(t, session)
	if len(tools) != 1 || tools[0].Name != "whoami" {
		t.Fatalf("the client sees %v, want the tool whoami", tools)
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "whoami", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("the call failed: %v", result.Content)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("close the client session: %v", err)
	}
	if err := <-served; err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("ServeStdio: %v", err)
	}

	caller, calls := report.last()
	if calls != 1 {
		t.Fatalf("the tool ran %d time(s), want 1", calls)
	}
	if caller.Principal != "local" {
		t.Errorf("the handler saw the principal %q, want local", caller.Principal)
	}
	if err := caller.Grant.Check("demo:write", authz.Sel{Selector: "group", Path: "/private"}); err != nil {
		t.Errorf("the handler's grant is not the local grant: %v", err)
	}
}
