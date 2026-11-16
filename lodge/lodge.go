// Package lodge is an app's adapter to lodge, the hub of frostyard's
// first-party apps.
//
// An app has 2 sockets: the lodge socket, on which lodge forwards a user's
// calls, and the app's private socket, on which the app's own user works. The
// adapter trusts the Lodge-Principal and Lodge-Grant headers on the lodge
// socket only, and only after it has checked the peer's user id: every other
// connection gets the local user, and its Lodge-* headers are deleted.
//
// The permissions themselves are the app's: docs/specs/permissions.md is the
// contract, and github.com/frostyard/hearth/authz is the runtime.
package lodge

import (
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/frostyard/hearth/authz"
)

// The 2 headers that lodge sends, after it has authenticated the user and
// looked up the user's grant for this app.
const (
	// PrincipalHeader carries the caller's principal.
	PrincipalHeader = "Lodge-Principal"
	// GrantHeader carries the caller's grant, as base64url without padding
	// (RFC 4648 §5) of the grant's JSON.
	GrantHeader = "Lodge-Grant"
)

// principalPattern is the pattern of a principal's name.
var principalPattern = regexp.MustCompile(`^[A-Za-z0-9@._-]{1,64}$`)

// peerKey is the context key that ConnContext sets on a connection from lodge.
type peerKey struct{}

// Listener returns a listener that accepts only the connections of lodgeUID.
// It closes every other connection, and every connection whose peer cannot be
// read, before it reads a byte of it, and it keeps accepting.
func Listener(l net.Listener, lodgeUID int) net.Listener {
	return &lodgeListener{Listener: l, lodgeUID: lodgeUID}
}

// lodgeListener is the listener of lodge's socket.
type lodgeListener struct {
	net.Listener
	lodgeUID int
}

// Accept accepts the next connection of lodge.
//
// It returns the inner listener's error unchanged: http.Server.Serve retries a
// temporary error that a net.Error reports, and a wrapped error would end the
// server.
func (l *lodgeListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uid, err := peerUID(conn)
		if err != nil || uid != l.lodgeUID {
			_ = conn.Close()
			continue
		}
		return lodgeConn{Conn: conn}, nil
	}
}

// lodgeConn marks 1 connection that came from lodge's socket.
type lodgeConn struct {
	net.Conn
}

// ConnContext marks the context of 1 connection that lodge's listener accepted.
// It is the http.Server's ConnContext.
func ConnContext(ctx context.Context, conn net.Conn) context.Context {
	switch conn.(type) {
	case lodgeConn, *lodgeConn:
		return context.WithValue(ctx, peerKey{}, true)
	}
	return ctx
}

// fromLodge reports whether 1 request context came from lodge's socket.
func fromLodge(ctx context.Context) bool {
	marked, ok := ctx.Value(peerKey{}).(bool)
	return ok && marked
}

// Handler serves an app's lodge socket: it reads the principal and the grant
// from lodge's 2 headers, and refuses the request with 403 unless it came from
// lodge's socket, the principal is valid and the grant parses.
//
// On success the caller is in the request's context
// (authz.CallerFrom), so every generated permission check reads it.
func Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromLodge(r.Context()) {
			refuse(w, "the connection is not the socket of lodge")
			return
		}
		principal := r.Header.Get(PrincipalHeader)
		if !principalPattern.MatchString(principal) {
			refuse(w, "the "+PrincipalHeader+" header is missing or malformed")
			return
		}
		encoded := r.Header.Get(GrantHeader)
		data, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
		if err != nil {
			refuse(w, "the "+GrantHeader+" header is not base64url without padding")
			return
		}
		grant, err := authz.ParseGrant(data)
		if err != nil {
			refuse(w, "the "+GrantHeader+" header is not a grant: "+err.Error())
			return
		}
		ctx := authz.WithCaller(r.Context(), authz.Caller{Principal: principal, Grant: grant})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// PrivateHandler serves an app's private socket: it deletes every Lodge-* header
// of the request, and sets the local user as the caller. A forged header
// therefore reaches no handler.
func PrivateHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for name := range r.Header {
			if strings.HasPrefix(http.CanonicalHeaderKey(name), "Lodge-") {
				delete(r.Header, name)
			}
		}
		ctx := authz.WithCaller(r.Context(), authz.LocalCaller())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Serve serves h on lodge's socket, over the listener of
// Listener(l, lodgeUID), and returns the http.Server that runs it.
//
// Serve starts the server and returns; stop it with the server's Shutdown or
// Close. A caller that needs the server's own errors serves the listener
// itself, with Handler and ConnContext.
func Serve(l net.Listener, lodgeUID int, h http.Handler) *http.Server {
	server := &http.Server{
		Handler:     Handler(h),
		ConnContext: ConnContext,
	}
	go func() {
		// Serve returns after Shutdown or Close, which a caller asks for.
		_ = server.Serve(Listener(l, lodgeUID))
	}()
	return server
}

// refuse answers 1 request with 403 and 1 line of text.
func refuse(w http.ResponseWriter, reason string) {
	http.Error(w, "forbidden: "+oneLine(reason), http.StatusForbidden)
}

// oneLine returns 1 text on 1 line.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
