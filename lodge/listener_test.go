package lodge_test

import (
	"errors"
	"net"
	"testing"

	"github.com/frostyard/hearth/lodge"
)

// temporaryError is 1 temporary error of a listener, as a net.Error reports it.
type temporaryError struct{}

// Error implements error.
func (temporaryError) Error() string { return "the listener is not ready" }

// Timeout implements net.Error.
func (temporaryError) Timeout() bool { return true }

// Temporary implements net.Error.
func (temporaryError) Temporary() bool { return true }

// failingListener is a listener whose Accept returns 1 error.
type failingListener struct {
	// err is the error that Accept returns.
	err error
}

// Accept returns the listener's error.
func (l failingListener) Accept() (net.Conn, error) {
	return nil, l.err
}

// Close closes the listener.
func (failingListener) Close() error { return nil }

// Addr returns the listener's address.
func (failingListener) Addr() net.Addr {
	return &net.UnixAddr{Name: "hearth-lodge-test", Net: "unix"}
}

// TestAcceptReturnsTheListenersError checks that the lodge listener hands the
// inner listener's error on unchanged: http.Server.Serve retries 1 temporary
// error of a net.Error, and a wrapped error would stop the server.
func TestAcceptReturnsTheListenersError(t *testing.T) {
	var want net.Error = temporaryError{}
	listener := lodge.Listener(failingListener{err: want}, 0)
	_, err := listener.Accept()
	if err != want {
		t.Fatalf("Accept = %v (%T), want the listener's own error %v", err, err, want)
	}
	// The error is the listener's own value, so its temporary flag is the
	// listener's too: http.Server.Serve retries it.
	var netErr net.Error
	if !errors.As(err, &netErr) {
		t.Fatalf("Accept = %T, and the error must still be a net.Error", err)
	}
}
