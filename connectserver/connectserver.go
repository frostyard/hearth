// Package connectserver serves an app's API over Connect, on the app's 2
// sockets: its private socket, where its own user works, and its lodge socket,
// where lodge forwards a user's calls.
//
// The route is the code that protoc-gen-connect-go writes: the handler of an
// app's service is the API that protoc-gen-hearth-authz generates, and the
// generated client satisfies the agent interface that protoc-gen-hearth-mcp
// generates. This package adds the 3 things that no generator writes: the error
// mapping of the permission runtime, the admission of the 2 sockets, and the
// server itself.
//
// docs/specs/connect.md is the contract.
package connectserver

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/frostyard/hearth/authz"
	"github.com/frostyard/hearth/lodge"
)

// LodgePrefix is the path under which lodge forwards a call on an app's lodge
// socket: POST /connect/<service>/<method>.
const LodgePrefix = "/connect/"

// The header that names the caller of a call on the lodge socket, and the 1
// value that admits it.
const (
	// CallerHeader is lodge's header for the kind of caller of a request. It
	// arrives only on the app's lodge socket: lodge writes it, and the adapter
	// deletes every Lodge-* header of the private socket.
	CallerHeader = "Lodge-Caller"
	// PrincipalCaller is the value of CallerHeader for a call that lodge
	// forwards for an authenticated principal. A call with any other value is
	// refused.
	PrincipalCaller = "principal"
)

// The times of the server: how long the server reads a request's header, and
// how long a stop waits for the calls in flight.
const (
	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 5 * time.Second
)

// mount is the path that the Lodge handler strips from a request.
const mount = "/connect"

// Interceptor returns 1 Connect interceptor that maps the error of each unary
// call with Error, so that a refusal reaches the client with the permission
// runtime's code and text. A streaming call passes through unchanged.
func Interceptor() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			response, err := next(ctx, req)
			if err != nil {
				return nil, Error(err)
			}
			return response, nil
		}
	})
}

// Error returns err as a Connect error whose code names what went wrong, and
// whose message is err's own text.
//
//   - a *connect.Error stays as it is;
//   - a refusal of the permission runtime (*authz.DeniedError, or
//     authz.ErrNoCaller) is CodePermissionDenied, with the text
//     "permission denied: needs <capability> on <selector>=<path>";
//   - an invalid path (*authz.PathError) is CodeInvalidArgument;
//   - context.Canceled is CodeCanceled, and context.DeadlineExceeded is
//     CodeDeadlineExceeded;
//   - any other error is CodeInternal, because an app's own error is its own
//     business.
func Error(err error) error {
	if err == nil {
		return nil
	}
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return err
	}
	var denied *authz.DeniedError
	var pathErr *authz.PathError
	switch {
	case errors.As(err, &denied), errors.Is(err, authz.ErrNoCaller):
		return connect.NewError(connect.CodePermissionDenied, errors.New(err.Error()))
	case errors.As(err, &pathErr):
		return connect.NewError(connect.CodeInvalidArgument, errors.New(err.Error()))
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, errors.New(err.Error()))
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, errors.New(err.Error()))
	default:
		return connect.NewError(connect.CodeInternal, errors.New(err.Error()))
	}
}

// HandlerOptions returns the handler options of an app's service: the error
// mapping of Interceptor, and then opts.
func HandlerOptions(opts ...connect.HandlerOption) []connect.HandlerOption {
	return append([]connect.HandlerOption{connect.WithInterceptors(Interceptor())}, opts...)
}

// Private returns the handler of an app's private socket: h at path, as the
// local user, and no other path.
//
// The adapter deletes every Lodge-* header of the request, so a forged
// Lodge-Principal or Lodge-Grant reaches no handler: the caller of every call
// on this socket is authz.LocalCaller().
func Private(path string, h http.Handler) http.Handler {
	return lodge.PrivateHandler(mux(path, h))
}

// Lodge returns the handler of an app's lodge socket, for mounting at
// LodgePrefix. It serves h at path, and it refuses a request with 403 unless
// the CallerHeader names PrincipalCaller and the path is under LodgePrefix.
// Behind that, lodge.Handler reads the principal and the grant of lodge's 2
// headers, and refuses a connection that lodge's listener did not accept.
//
// The app mounts its own routes beside this adapter, outside it: /health and
// every other path that is no Connect call is the app's, and lodge's headers
// bound no part of it.
func Lodge(path string, h http.Handler) http.Handler {
	next := lodge.Handler(http.StripPrefix(mount, mux(path, h)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(CallerHeader) != PrincipalCaller {
			refuse(w, "the "+CallerHeader+" header is missing or is not "+PrincipalCaller)
			return
		}
		if !strings.HasPrefix(r.URL.Path, LodgePrefix) {
			refuse(w, "the path is not under "+LodgePrefix)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Serve serves h on l until ctx ends, and it then shuts the server down. It
// returns nil after a clean stop, and the server's error otherwise: an error of
// the server closes the connections it accepted, so no new call starts after
// Serve returns (a call in flight sees its connection close).
//
// The caller passes the listener of either socket: lodge.Listener(l, uid)
// serves the socket of 1 user only, and Serve marks the connections of that
// listener with lodge.ConnContext, so lodge.Handler can tell a call that came
// from lodge's socket from any other.
func Serve(ctx context.Context, l net.Listener, h http.Handler) error {
	server := &http.Server{
		Handler:           h,
		ConnContext:       lodge.ConnContext,
		ReadHeaderTimeout: readHeaderTimeout,
	}
	served := make(chan error, 1)
	go func() {
		served <- server.Serve(l)
	}()
	select {
	case err := <-served:
		// The server stopped by itself, and its error is permanent: the
		// listener is closed, and every connection it accepted must close too,
		// or a keep-alive connection serves new calls after this call returns.
		_ = server.Close()
		return err
	case <-ctx.Done():
	}
	// The stop outlives the context that ended: the calls in flight get their
	// time to finish.
	stop, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(stop); err != nil {
		_ = server.Close()
		return err
	}
	if err := <-served; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// UnixClient returns a client of the HTTP server on 1 Unix socket.
//
// The host of the client's base URL is ignored, and a client dials the socket
// whatever it is given, so a caller writes "http://localhost" and not a real
// host.
func UnixClient(socket string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var dialer net.Dialer
				return dialer.DialContext(ctx, "unix", socket)
			},
		},
	}
}

// mux returns the mux that serves 1 handler at 1 path, and no other path.
func mux(path string, h http.Handler) http.Handler {
	handler := http.NewServeMux()
	handler.Handle(path, h)
	return handler
}

// refuse answers 1 request with 403 and 1 line of text.
func refuse(w http.ResponseWriter, reason string) {
	http.Error(w, "forbidden: "+oneLine(reason), http.StatusForbidden)
}

// oneLine returns 1 text on 1 line.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
