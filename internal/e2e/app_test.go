package e2e_test

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/frostyard/hearth/authz"
	"github.com/frostyard/hearth/internal/e2e"
	demov1 "github.com/frostyard/hearth/internal/e2e/gen/frostyard/demo/v1"
	"github.com/frostyard/hearth/mcpserver"
)

// grant returns 1 grant of items, or stops the test.
func grant(t *testing.T, items ...authz.Item) authz.Grant {
	t.Helper()
	g, err := authz.NewGrant(items...)
	if err != nil {
		t.Fatalf("NewGrant: %v", err)
	}
	return g
}

// item returns 1 grant item on 1 group.
func item(cap, group string) authz.Item {
	return authz.Item{Cap: cap, Sel: map[string]string{"group": group}}
}

// caller returns a context with 1 caller and 1 grant.
func caller(t *testing.T, g authz.Grant) context.Context {
	t.Helper()
	return authz.WithCaller(t.Context(), authz.Caller{Principal: "chani", Grant: g})
}

// TestDeniedListNotes checks the generated check of an RPC whose selector is in
// the request: a read grant on /work does not cover /private.
func TestDeniedListNotes(t *testing.T) {
	app := e2e.New()
	api := app.Wired()
	ctx := caller(t, grant(t, item("demo:read", "/work")))
	_, err := api.ListNotes(ctx, &demov1.ListNotesRequest{Group: "/private"})
	if err == nil {
		t.Fatal("ListNotes on /private passed, and the grant holds /work")
	}
	if want := "permission denied: needs demo:read on group=/private"; err.Error() != want {
		t.Errorf("the refusal = %q, want %q", err, want)
	}
	var denied *authz.DeniedError
	if !errors.As(err, &denied) {
		t.Errorf("the refusal is %T, want *authz.DeniedError", err)
	}
}

// TestListNotesFilters checks that the app's list filter leaves out a note the
// caller's grant does not cover, and keeps 1 it covers.
func TestListNotesFilters(t *testing.T) {
	app := e2e.New()
	app.SeedNote("n-1", "/work/2026")
	app.SeedNote("n-2", "/private")
	// A group that is no path is never in a list, though its text starts with
	// the requested group.
	app.SeedNote("n-3", "/work/../private")
	api := app.Wired()
	response, err := api.ListNotes(caller(t, authz.LocalCaller().Grant), &demov1.ListNotesRequest{Group: "/"})
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(response.GetNotes()) != 2 {
		t.Fatalf("the local user reads %d note(s), want 2", len(response.GetNotes()))
	}
	for _, note := range response.GetNotes() {
		if note.GetId() == "n-3" {
			t.Error("the list holds the note n-3, whose group is no path")
		}
	}

	// A narrow grant reads the notes of its own group, and the note of another
	// group stays out of the list.
	narrow := grant(t, item("demo:read", "/work"))
	response, err = api.ListNotes(caller(t, narrow), &demov1.ListNotesRequest{Group: "/work"})
	if err != nil {
		t.Fatalf("ListNotes with the narrow grant: %v", err)
	}
	if got := response.GetNotes(); len(got) != 1 || got[0].GetId() != "n-1" {
		t.Errorf("the narrow grant reads %v, want the note n-1 only", got)
	}
}

// TestDeniedGetNoteByHook checks the hook of an RPC that runs on an object id:
// the generated check reads the capability, and the hook reads the stored group.
func TestDeniedGetNoteByHook(t *testing.T) {
	app := e2e.New()
	app.SeedNote("n-1", "/private")
	api := app.Wired()
	_, err := api.GetNote(caller(t, grant(t, item("demo:read", "/work"))), &demov1.GetNoteRequest{Id: "n-1"})
	if err == nil {
		t.Fatal("GetNote of a note in /private passed, and the grant holds /work")
	}
	if want := "permission denied: needs demo:read on group=/private"; err.Error() != want {
		t.Errorf("the refusal = %q, want %q", err, want)
	}
}

// TestMoveNoteBothEnds checks a move: the generated check reads the
// destination, and the hook reads the source.
func TestMoveNoteBothEnds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		items []authz.Item
		want  string
	}{
		{"the source only", []authz.Item{item("demo:write", "/a")}, "permission denied: needs demo:write on group=/b"},
		{"the destination only", []authz.Item{item("demo:write", "/b")}, "permission denied: needs demo:write on group=/a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := e2e.New()
			app.SeedNote("n-1", "/a")
			api := app.Wired()
			_, err := api.MoveNote(caller(t, grant(t, tc.items...)), &demov1.MoveNoteRequest{Id: "n-1", ToGroup: "/b"})
			if err == nil {
				t.Fatalf("the move of n-1 from /a to /b passed, and the grant holds %s", tc.name)
			}
			if err.Error() != tc.want {
				t.Errorf("the refusal = %q, want %q", err, tc.want)
			}
		})
	}

	app := e2e.New()
	app.SeedNote("n-1", "/a")
	api := app.Wired()
	response, err := api.MoveNote(caller(t, grant(t, item("demo:write", "/a"), item("demo:write", "/b"))),
		&demov1.MoveNoteRequest{Id: "n-1", ToGroup: "/b"})
	if err != nil {
		t.Fatalf("the move with the grant on both ends: %v", err)
	}
	if got := response.GetNote().GetGroup(); got != "/b" {
		t.Errorf("the note's group is %q, want /b", got)
	}
}

// TestCreateNoteByAgent checks that the generated tools of the demo app serve 1
// agent call as the local user, over the stdio server, with no token.
func TestCreateNoteByAgent(t *testing.T) {
	app := e2e.New()
	app.SeedNote("n-1", "/work")
	tools := demov1.NoteServiceTools(app.Wired())
	server := mcpserver.NewServer(&mcp.Implementation{Name: "demo", Version: "0.1.0"}, authz.LocalCaller(), tools)

	clientToServer, clientToServerWriter := io.Pipe()
	serverToClient, serverToClientWriter := io.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	served := make(chan error, 1)
	go func() {
		served <- mcpserver.ServeStdio(ctx, server, clientToServer, serverToClientWriter)
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "hearth-e2e", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.IOTransport{Reader: serverToClient, Writer: clientToServerWriter}, nil)
	if err != nil {
		t.Fatalf("connect the client: %v", err)
	}

	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := make([]string, 0, len(list.Tools))
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
	}
	want := []string{"create_note", "get_note", "list_notes", "move_note", "publish_note"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("the client sees the tools %q, want %q", names, want)
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_notes",
		Arguments: map[string]any{"group": "/work"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("the call failed: %v", result.Content)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("the content is %T, want text", result.Content[0])
	}
	if !strings.Contains(text.Text, `"n-1"`) {
		t.Errorf("the result %q does not carry the note n-1", text.Text)
	}

	result, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_note",
		Arguments: map[string]any{"id": "n-1", "extra": true},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Error("a call with an undeclared argument passed, and the server decodes its arguments with protojson")
	}

	if err := session.Close(); err != nil {
		t.Fatalf("close the client session: %v", err)
	}
	if err := <-served; err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("ServeStdio: %v", err)
	}
}

// TestMissingHookFailsBuild checks the guard of the generated permission check:
// an app whose type has no AuthorizeGetNote hook does not build.
func TestMissingHookFailsBuild(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the go binary is not on PATH, so the build case cannot run: %v", err)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "missinghook")
	command := exec.CommandContext(t.Context(), goTool, "build", "-tags", "hearth_missinghook", "-o", output, "./testdata/e2e/missinghook")
	command.Dir = root
	command.Env = append(os.Environ(), "GOFLAGS=-mod=readonly", "GOPROXY=off", "GOWORK=off")
	out, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("the build passed, and the app has no AuthorizeGetNote hook: %s", out)
	}
	if !strings.Contains(string(out), "AuthorizeGetNote") {
		t.Errorf("the build's error does not name the missing hook AuthorizeGetNote:\n%s", out)
	}
}
