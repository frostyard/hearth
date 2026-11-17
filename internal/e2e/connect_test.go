package e2e_test

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/frostyard/hearth/connectserver"
	"github.com/frostyard/hearth/internal/e2e"
	demov1 "github.com/frostyard/hearth/internal/e2e/gen/frostyard/demo/v1"
	"github.com/frostyard/hearth/internal/e2e/gen/frostyard/demo/v1/demov1connect"
	"github.com/frostyard/hearth/lodge"
)

// workGrant is a read of /work, as lodge sends it in its header.
const workGrant = `[{"cap":"demo:read","sel":{"group":"/work"}}]`

// setHeaders is a client interceptor that sets the headers of each call.
type setHeaders map[string]string

// WrapUnary implements connect.Interceptor.
func (h setHeaders) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		for name, value := range h {
			req.Header().Set(name, value)
		}
		return next(ctx, req)
	}
}

// WrapStreamingClient implements connect.Interceptor.
func (h setHeaders) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler implements connect.Interceptor.
func (h setHeaders) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

// lodgeCall returns the headers of a call that lodge forwards for 1 principal
// with 1 grant.
func lodgeCall(grant string) setHeaders {
	return setHeaders{
		connectserver.CallerHeader: connectserver.PrincipalCaller,
		lodge.PrincipalHeader:      "chani",
		lodge.GrantHeader:          base64.RawURLEncoding.EncodeToString([]byte(grant)),
	}
}

// shortSocket returns a path for a Unix socket in a directory of its own,
// because a socket's path is 108 bytes at most and a test's directory can be
// longer.
func shortSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hearth")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Logf("remove %s: %v", dir, err)
		}
	})
	return filepath.Join(dir, "socket")
}

// serve serves 1 handler on 1 Unix socket that admits the connections of 1
// user, and it returns the socket's path.
func serve(t *testing.T, uid int, handler http.Handler) string {
	t.Helper()
	path := shortSocket(t)
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() {
		served <- connectserver.Serve(ctx, lodge.Listener(listener, uid), handler)
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return path
}

// noteHandler returns the demo app's Connect route, and its path prefix.
func noteHandler(t *testing.T) (string, http.Handler) {
	t.Helper()
	return demov1connect.NewNoteServiceHandler(e2e.New().Wired(), connectserver.HandlerOptions()...)
}

// TestConnectClientIsTheAgentAPI checks that the generated Connect client of an
// app's service satisfies the agent interface of its tools.
func TestConnectClientIsTheAgentAPI(t *testing.T) {
	var client demov1.NoteServiceAgentAPI = demov1connect.NewNoteServiceClient(
		connectserver.UnixClient("notelite.sock"), "http://localhost")
	if client == nil {
		t.Fatal("NewNoteServiceClient returned no client")
	}
}

// TestConnectPrivateSocket checks the app's private socket: the local user
// creates and lists a note, and a forged Lodge-Grant header neither narrows nor
// moves that caller.
func TestConnectPrivateSocket(t *testing.T) {
	prefix, handler := noteHandler(t)
	socket := serve(t, os.Getuid(), connectserver.Private(prefix, handler))
	// The generated Connect client is the agent API of the app's tools, so 1
	// client serves the app's own user interface and its tools.
	var client demov1.NoteServiceAgentAPI = demov1connect.NewNoteServiceClient(
		connectserver.UnixClient(socket), "http://localhost")

	created, err := client.CreateNote(t.Context(), &demov1.CreateNoteRequest{
		Note: &demov1.Note{Group: "/private", Text: proto.String("a note")},
	})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	for _, set := range []setHeaders{nil, lodgeCall(`[{"cap":"demo:read","sel":{"group":"/home"}}]`)} {
		listing := demov1connect.NewNoteServiceClient(connectserver.UnixClient(socket), "http://localhost",
			connect.WithInterceptors(set))
		response, err := listing.ListNotes(t.Context(), &demov1.ListNotesRequest{Group: "/private"})
		if err != nil {
			t.Fatalf("ListNotes of /private: %v", err)
		}
		if len(response.GetNotes()) != 1 {
			t.Fatalf("ListNotes of /private = %d note(s), want the 1 of the local user", len(response.GetNotes()))
		}
		if got, want := response.GetNotes()[0].GetId(), created.GetNote().GetId(); got != want {
			t.Errorf("the listed note = %q, want %q", got, want)
		}
	}
}

// TestConnectLodgeSocket checks the app's lodge socket: a caller whose grant
// reads /work lists /work, and the read of /private is refused with the
// permission runtime's own text and code.
func TestConnectLodgeSocket(t *testing.T) {
	prefix, handler := noteHandler(t)
	socket := serve(t, os.Getuid(), connectserver.Lodge(prefix, handler))
	client := demov1connect.NewNoteServiceClient(connectserver.UnixClient(socket), "http://localhost/connect",
		connect.WithInterceptors(lodgeCall(workGrant)))

	if _, err := client.ListNotes(t.Context(), &demov1.ListNotesRequest{Group: "/work"}); err != nil {
		t.Fatalf("ListNotes of /work: %v", err)
	}
	_, err := client.ListNotes(t.Context(), &demov1.ListNotesRequest{Group: "/private"})
	if err == nil {
		t.Fatal("ListNotes of /private passed, and the grant holds /work")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Errorf("the refusal's code = %v, want %v", got, connect.CodePermissionDenied)
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("the refusal = %T, want a *connect.Error", err)
	}
	if want := "permission denied: needs demo:read on group=/private"; connectErr.Message() != want {
		t.Errorf("the refusal = %q, want %q", connectErr.Message(), want)
	}
}

// TestConnectLodgeSocketRefusesAnotherPeer checks the peer check of the lodge
// socket: a connection of another user gets no answer, though its headers are
// right.
func TestConnectLodgeSocketRefusesAnotherPeer(t *testing.T) {
	prefix, handler := noteHandler(t)
	socket := serve(t, os.Getuid()+1, connectserver.Lodge(prefix, handler))
	client := demov1connect.NewNoteServiceClient(connectserver.UnixClient(socket), "http://localhost/connect",
		connect.WithInterceptors(lodgeCall(workGrant)))
	if _, err := client.ListNotes(t.Context(), &demov1.ListNotesRequest{Group: "/work"}); err == nil {
		t.Fatal("the call passed, and the socket admits another user's connections")
	}
}
