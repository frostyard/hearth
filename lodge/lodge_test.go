//go:build linux

package lodge_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frostyard/hearth/authz"
	"github.com/frostyard/hearth/lodge"
)

// grantHeader returns 1 grant as lodge sends it.
func grantHeader(t *testing.T, grant string) string {
	t.Helper()
	return base64.RawURLEncoding.EncodeToString([]byte(grant))
}

// shortSocketPath returns a path for a Unix socket in a directory of its own,
// because a socket's path is 108 bytes at most and a test's own directory can be
// longer.
func shortSocketPath(t *testing.T) string {
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

// listen returns a Unix listener, and its path.
func listen(t *testing.T) (net.Listener, string) {
	t.Helper()
	path := shortSocketPath(t)
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Logf("close the listener: %v", err)
		}
	})
	return listener, path
}

// serveLodge serves 1 handler on a lodge socket of 1 user, and returns the
// socket's path.
func serveLodge(t *testing.T, lodgeUID int, handler http.Handler) string {
	t.Helper()
	listener, path := listen(t)
	server := &http.Server{Handler: lodge.Handler(handler), ConnContext: lodge.ConnContext}
	go func() {
		_ = server.Serve(lodge.Listener(listener, lodgeUID))
	}()
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Logf("close the server: %v", err)
		}
	})
	return path
}

// servePrivate serves 1 handler on an app's private socket, and returns the
// socket's path.
func servePrivate(t *testing.T, handler http.Handler) string {
	t.Helper()
	listener, path := listen(t)
	server := &http.Server{Handler: lodge.PrivateHandler(handler)}
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Logf("close the server: %v", err)
		}
	})
	return path
}

// unixClient returns a client that dials 1 Unix socket.
func unixClient(t *testing.T, path string) *http.Client {
	t.Helper()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", path)
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport}
}

// callerBody writes 1 handler's view of the caller.
func callerBody(w http.ResponseWriter, r *http.Request) {
	caller, ok := authz.CallerFrom(r.Context())
	if !ok {
		http.Error(w, "no caller", http.StatusInternalServerError)
		return
	}
	allowed := caller.Grant.Allows("demo:write", "group", "/private")
	fmt.Fprintf(w, "principal=%s allowed=%t", caller.Principal, allowed)
}

// TestLodgeSocketReadsHeaders checks that the lodge socket reads the principal
// and the grant, after the peer check.
func TestLodgeSocketReadsHeaders(t *testing.T) {
	path := serveLodge(t, os.Getuid(), http.HandlerFunc(callerBody))
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://lodge/notes", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(lodge.PrincipalHeader, "brian@example.com")
	request.Header.Set(lodge.GrantHeader, grantHeader(t, `[{"cap":"demo:write","sel":{"group":"/private"}}]`))
	response, err := unixClient(t, path).Do(request)
	if err != nil {
		t.Fatalf("the request: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("the status is %d, want 200: %s", response.StatusCode, body)
	}
	if want := "principal=brian@example.com allowed=true"; string(body) != want {
		t.Errorf("the handler saw %q, want %q", body, want)
	}
}

// TestWrongPeerUIDRefused checks that the lodge socket closes a connection of
// another user before any handler runs.
func TestWrongPeerUIDRefused(t *testing.T) {
	var handled atomic.Int32
	path := serveLodge(t, os.Getuid()+1, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handled.Add(1)
		callerBody(w, r)
	}))

	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	// Connection: close makes a server that wrongly answers close the stream
	// after its response, so the test fails on its assertion and never waits.
	if _, err := io.WriteString(conn, "GET /notes HTTP/1.1\r\nHost: lodge\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatalf("write the request: %v", err)
	}
	// The server closes the connection without a response: the read ends at the
	// end of the stream, or with a reset.
	response, err := io.ReadAll(conn)
	if err == nil && len(response) > 0 {
		t.Errorf("the refused connection got %d byte(s) of response: %q", len(response), response)
	}
	if got := handled.Load(); got != 0 {
		t.Errorf("the handler ran %d time(s), want 0", got)
	}
}

// TestForgedHeadersOnPrivateSocket checks that a Lodge-* header of the private
// socket reaches no handler, and that the caller is the local user.
func TestForgedHeadersOnPrivateSocket(t *testing.T) {
	var seen []string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Values(lodge.PrincipalHeader)...)
		caller, ok := authz.CallerFrom(r.Context())
		if !ok {
			http.Error(w, "no caller", http.StatusInternalServerError)
			return
		}
		allowed := caller.Grant.Allows("demo:write", "group", "/private")
		fmt.Fprintf(w, "principal=%s allowed=%t", caller.Principal, allowed)
	})
	path := servePrivate(t, handler)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://app/notes", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(lodge.PrincipalHeader, "chani")
	request.Header.Set(lodge.GrantHeader, grantHeader(t, `[{"cap":"demo:read","sel":{"group":"/work"}}]`))
	response, err := unixClient(t, path).Do(request)
	if err != nil {
		t.Fatalf("the request: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("the status is %d, want 200: %s", response.StatusCode, body)
	}
	if want := "principal=local allowed=true"; string(body) != want {
		t.Errorf("the handler saw %q, want %q", body, want)
	}
	if len(seen) != 0 {
		t.Errorf("the handler read the forged header(s) %q, want none", seen)
	}
}

// TestMissingHeaders403 checks that the lodge socket refuses a request without
// lodge's headers.
func TestMissingHeaders403(t *testing.T) {
	var handled atomic.Int32
	path := serveLodge(t, os.Getuid(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handled.Add(1)
		callerBody(w, r)
	}))
	for _, tc := range []struct {
		name      string
		principal string
		grant     string
	}{
		{"no header", "", ""},
		{"a principal alone", "brian", ""},
		{"a grant alone", "", grantHeader(t, `[{"cap":"demo:read"}]`)},
		{"a malformed principal", "brian!", grantHeader(t, `[{"cap":"demo:read"}]`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://lodge/notes", nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.principal != "" {
				request.Header.Set(lodge.PrincipalHeader, tc.principal)
			}
			if tc.grant != "" {
				request.Header.Set(lodge.GrantHeader, tc.grant)
			}
			response, err := unixClient(t, path).Do(request)
			if err != nil {
				t.Fatalf("the request: %v", err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusForbidden {
				t.Fatalf("the status is %d, want 403: %s", response.StatusCode, body)
			}
			if got := string(body); !strings.HasPrefix(got, "forbidden: ") || strings.Count(strings.TrimSuffix(got, "\n"), "\n") != 0 {
				t.Errorf("the refusal is %q, want 1 line that starts with %q", got, "forbidden: ")
			}
		})
	}
	if got := handled.Load(); got != 0 {
		t.Errorf("the handler ran %d time(s), want 0", got)
	}
}

// TestBadGrant403 checks that the lodge socket refuses a grant it cannot read.
func TestBadGrant403(t *testing.T) {
	var handled atomic.Int32
	path := serveLodge(t, os.Getuid(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handled.Add(1)
		callerBody(w, r)
	}))
	for _, tc := range []struct {
		name  string
		grant string
	}{
		{"not base64url", "%%%"},
		{"padded base64", base64.URLEncoding.EncodeToString([]byte(`[{"cap":"demo:read","sel":{"group":"/work/2026"}}]`))},
		{"not a grant", grantHeader(t, `{"cap":"demo:read"}`)},
		{"a bad capability", grantHeader(t, `[{"cap":"Demo:read"}]`)},
		{"an unknown member", grantHeader(t, `[{"cap":"demo:read","extra":"x"}]`)},
		{"an invalid path", grantHeader(t, `[{"cap":"demo:read","sel":{"group":"/a/../b"}}]`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://lodge/notes", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set(lodge.PrincipalHeader, "brian")
			request.Header.Set(lodge.GrantHeader, tc.grant)
			response, err := unixClient(t, path).Do(request)
			if err != nil {
				t.Fatalf("the request: %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusForbidden {
				t.Fatalf("the status is %d, want 403", response.StatusCode)
			}
		})
	}
	if got := handled.Load(); got != 0 {
		t.Errorf("the handler ran %d time(s), want 0", got)
	}
}

// TestHandlerWithoutPeerMark403 checks that the handler refuses a request whose
// connection the lodge listener did not accept.
func TestHandlerWithoutPeerMark403(t *testing.T) {
	var handled atomic.Int32
	handler := lodge.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handled.Add(1)
		callerBody(w, r)
	}))
	request := httptest.NewRequest(http.MethodGet, "http://lodge/notes", nil)
	request.Header.Set(lodge.PrincipalHeader, "brian")
	request.Header.Set(lodge.GrantHeader, grantHeader(t, `[{"cap":"demo:read"}]`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Errorf("the status is %d, want 403", recorder.Code)
	}
	if got, want := recorder.Header().Get("Content-Type"), "text/plain; charset=utf-8"; got != want {
		t.Errorf("the content type is %q, want %q", got, want)
	}
	if got := handled.Load(); got != 0 {
		t.Errorf("the handler ran %d time(s), want 0", got)
	}
}
