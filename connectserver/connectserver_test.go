package connectserver_test

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/frostyard/hearth/authz"
	"github.com/frostyard/hearth/connectserver"
	"github.com/frostyard/hearth/lodge"
)

// procedure is the Connect procedure of the test's 1 method.
const procedure = "/hearth.test.v1.TestService/Call"

// testGrant is the grant that lodge's header carries: a read of /work.
const testGrant = `[{"cap":"demo:read","sel":{"group":"/work"}}]`

// shortSocket returns a path for a Unix socket in a directory of its own,
// because a socket's path is 108 bytes at most and a test's directory can be
// longer.
func shortSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hc")
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

// listen returns 1 Unix listener, and the socket's path.
func listen(t *testing.T) (net.Listener, string) {
	t.Helper()
	path := shortSocket(t)
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return listener, path
}

// serve serves 1 handler on 1 listener, and it returns the socket's path.
func serve(t *testing.T, listener net.Listener, handler http.Handler) string {
	t.Helper()
	path, ok := listener.Addr().(*net.UnixAddr)
	if !ok {
		t.Fatalf("the listener's address = %T, want a Unix address", listener.Addr())
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() {
		served <- connectserver.Serve(ctx, listener, handler)
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return path.Name
}

// serveUser serves 1 handler on the socket that admits the connections of 1
// user only, and it returns the socket's path.
func serveUser(t *testing.T, uid int, handler http.Handler) string {
	t.Helper()
	listener, _ := listen(t)
	return serve(t, lodge.Listener(listener, uid), handler)
}

// callerHandler returns the handler of the test's 1 method: it answers with the
// caller's name, and whether the caller may read /work.
func callerHandler() *connect.Handler {
	return connect.NewUnaryHandlerSimple(
		procedure,
		func(ctx context.Context, _ *emptypb.Empty) (*wrapperspb.StringValue, error) {
			caller, ok := authz.CallerFrom(ctx)
			if !ok {
				return nil, authz.ErrNoCaller
			}
			allowed := caller.Grant.Allows("demo:read", "group", "/work")
			return wrapperspb.String(caller.Principal + "|" + strconv.FormatBool(allowed)), nil
		},
		connectserver.HandlerOptions()...,
	)
}

// headers is a client interceptor that sets the headers of each call.
type headers map[string]string

// WrapUnary implements connect.Interceptor.
func (h headers) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		for name, value := range h {
			req.Header().Set(name, value)
		}
		return next(ctx, req)
	}
}

// WrapStreamingClient implements connect.Interceptor.
func (h headers) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler implements connect.Interceptor.
func (h headers) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

// lodgeHeaders returns the 3 headers of a call that lodge forwards for 1
// principal with 1 grant.
func lodgeHeaders(grant string) headers {
	return headers{
		connectserver.CallerHeader: connectserver.PrincipalCaller,
		lodge.PrincipalHeader:      "chani",
		lodge.GrantHeader:          base64.RawURLEncoding.EncodeToString([]byte(grant)),
	}
}

// call makes 1 unary call on 1 socket, with 1 set of headers, and it returns
// the answer's text.
func call(t *testing.T, socket, baseURL string, set headers) (string, error) {
	t.Helper()
	client := connect.NewClient[emptypb.Empty, wrapperspb.StringValue](
		connectserver.UnixClient(socket), strings.TrimSuffix(baseURL, "/")+procedure,
		connect.WithInterceptors(set),
	)
	response, err := client.CallUnary(t.Context(), connect.NewRequest(&emptypb.Empty{}))
	if err != nil {
		return "", err
	}
	return response.Msg.GetValue(), nil
}

// post writes 1 POST of 1 raw request on 1 socket, with 1 set of headers, and
// it returns the response's status and body.
func post(t *testing.T, socket, url string, set headers) (int, string) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range set {
		request.Header.Set(name, value)
	}
	response, err := connectserver.UnixClient(socket).Do(request)
	if err != nil {
		t.Fatalf("the request: %v", err)
	}
	defer func() {
		_ = response.Body.Close()
	}()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(body)
}

// TestErrorMapping checks the code and the text of each error that the
// permission runtime and the context report.
func TestErrorMapping(t *testing.T) {
	pathErr := &authz.PathError{Selector: "group", Path: "work", Reason: `it must start with "/"`}
	connectErr := connect.NewError(connect.CodeNotFound, errors.New("no note"))
	for _, tc := range []struct {
		name string
		err  error
		code connect.Code
		text string
	}{
		{"a refusal of the permission runtime", &authz.DeniedError{Cap: "demo:read", Selector: "group", Path: "/private"},
			connect.CodePermissionDenied, "permission denied: needs demo:read on group=/private"},
		{"a refusal that names no selector", &authz.DeniedError{Cap: "demo:read"},
			connect.CodePermissionDenied, "permission denied: needs demo:read"},
		{"no caller", authz.ErrNoCaller, connect.CodePermissionDenied, "permission denied: no caller"},
		{"an invalid path", pathErr, connect.CodeInvalidArgument, pathErr.Error()},
		{"a canceled call", context.Canceled, connect.CodeCanceled, context.Canceled.Error()},
		{"a call past its deadline", context.DeadlineExceeded, connect.CodeDeadlineExceeded, context.DeadlineExceeded.Error()},
		{"an app's own error", errors.New("no such note"), connect.CodeInternal, "no such note"},
	} {
		mapped := connectserver.Error(tc.err)
		if got := connect.CodeOf(mapped); got != tc.code {
			t.Errorf("%s: Error = %v, want the code %v", tc.name, got, tc.code)
		}
		var got *connect.Error
		if !errors.As(mapped, &got) {
			t.Errorf("%s: Error = %T, want a *connect.Error", tc.name, mapped)
			continue
		}
		if got.Message() != tc.text {
			t.Errorf("%s: the message = %q, want %q", tc.name, got.Message(), tc.text)
		}
	}

	if mapped := connectserver.Error(nil); mapped != nil {
		t.Errorf("Error(nil) = %v, want nil", mapped)
	}
	// A Connect error is the app's own: it stays as it is.
	if mapped := connectserver.Error(connectErr); mapped != connectErr {
		t.Errorf("Error(a *connect.Error) = %v, want the error itself", mapped)
	}
}

// TestErrorOnTheWire checks that the interceptor maps the refusal of 1 call, so
// that the client reads the permission runtime's code and text.
func TestErrorOnTheWire(t *testing.T) {
	denied := &authz.DeniedError{Cap: "demo:read", Selector: "group", Path: "/private"}
	handler := connect.NewUnaryHandlerSimple(
		procedure,
		func(context.Context, *emptypb.Empty) (*wrapperspb.StringValue, error) {
			return nil, denied
		},
		connectserver.HandlerOptions()...,
	)
	socket := serveUser(t, os.Getuid(), connectserver.Private(procedure, handler))
	_, err := call(t, socket, "http://localhost", nil)
	if err == nil {
		t.Fatal("the call passed, and the handler refused it")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Errorf("the call's code = %v, want %v", got, connect.CodePermissionDenied)
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("the call's error = %T, want a *connect.Error", err)
	}
	if want := "permission denied: needs demo:read on group=/private"; connectErr.Message() != want {
		t.Errorf("the refusal = %q, want %q", connectErr.Message(), want)
	}
}

// TestPrivateIgnoresLodgeHeaders checks that a call on the app's private socket
// is the local user's, whatever Lodge-* headers it carries.
func TestPrivateIgnoresLodgeHeaders(t *testing.T) {
	socket := serveUser(t, os.Getuid(), connectserver.Private(procedure, callerHandler()))
	forged := headers{
		lodge.PrincipalHeader: "chani",
		lodge.GrantHeader:     base64.RawURLEncoding.EncodeToString([]byte(testGrant)),
	}
	got, err := call(t, socket, "http://localhost", forged)
	if err != nil {
		t.Fatalf("the call: %v", err)
	}
	if want := "local|true"; got != want {
		t.Errorf("the caller = %q, want %q: a forged header reached the handler", got, want)
	}
}

// TestLodgeRefusesTheCaller checks that the lodge socket refuses a call with no
// Lodge-Caller header, and 1 with another value, before it reads anything else.
func TestLodgeRefusesTheCaller(t *testing.T) {
	handler := connectserver.Lodge(procedure, callerHandler())
	socket := serveUser(t, os.Getuid(), handler)
	for _, tc := range []struct {
		name string
		set  headers
	}{
		{"no caller", headers{
			lodge.PrincipalHeader: "chani",
			lodge.GrantHeader:     base64.RawURLEncoding.EncodeToString([]byte(testGrant)),
		}},
		{"another caller", headers{
			connectserver.CallerHeader: "local",
			lodge.PrincipalHeader:      "chani",
			lodge.GrantHeader:          base64.RawURLEncoding.EncodeToString([]byte(testGrant)),
		}},
	} {
		status, body := post(t, socket, "http://localhost"+connectserver.LodgePrefix+"hearth.test.v1.TestService/Call", tc.set)
		if status != http.StatusForbidden {
			t.Errorf("%s: the status = %d, want %d", tc.name, status, http.StatusForbidden)
		}
		if want := "forbidden: "; len(body) < len(want) || body[:len(want)] != want {
			t.Errorf("%s: the answer = %q, want 1 line that starts with %q", tc.name, body, want)
		}
	}
}

// TestLodgeAdmitsAPrincipal checks the call that lodge forwards: the socket
// reads the 3 headers of a principal and its grant, and the handler reads that
// caller.
func TestLodgeAdmitsAPrincipal(t *testing.T) {
	socket := serveUser(t, os.Getuid(), connectserver.Lodge(procedure, callerHandler()))
	got, err := call(t, socket, "http://localhost"+connectserver.LodgePrefix, lodgeHeaders(testGrant))
	if err != nil {
		t.Fatalf("the call: %v", err)
	}
	if want := "chani|true"; got != want {
		t.Errorf("the caller = %q, want %q", got, want)
	}
	// A grant on another path is the same caller, and no read of /work.
	other := lodgeHeaders(`[{"cap":"demo:read","sel":{"group":"/home"}}]`)
	got, err = call(t, socket, "http://localhost"+connectserver.LodgePrefix, other)
	if err != nil {
		t.Fatalf("the call: %v", err)
	}
	if want := "chani|false"; got != want {
		t.Errorf("the caller = %q, want %q", got, want)
	}
}

// TestLodgeRefusesAnotherSocket checks that the lodge socket refuses a call on
// a connection that lodge's listener did not accept, though its 3 headers are
// right: the peer check comes first.
func TestLodgeRefusesAnotherSocket(t *testing.T) {
	// The listener is the test's own: no lodge.Listener marks the connection.
	listener, _ := listen(t)
	socket := serve(t, listener, connectserver.Lodge(procedure, callerHandler()))
	status, body := post(t, socket, "http://localhost"+connectserver.LodgePrefix+"hearth.test.v1.TestService/Call", lodgeHeaders(testGrant))
	if status != http.StatusForbidden {
		t.Fatalf("the status = %d, want %d: %s", status, http.StatusForbidden, body)
	}
	if want := "forbidden: the connection is not the socket of lodge"; body != want+"\n" {
		t.Errorf("the answer = %q, want %q", body, want)
	}
}

// TestServeStopsOnItsContext checks that the server stops when the context
// ends, and that it then returns nil.
func TestServeStopsOnItsContext(t *testing.T) {
	listener, _ := listen(t)
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() {
		served <- connectserver.Serve(ctx, listener, connectserver.Private(procedure, callerHandler()))
	}()
	cancel()
	if err := <-served; err != nil {
		t.Errorf("Serve = %v, want nil after a clean stop", err)
	}
}
