package natstest_test

import (
	"net"
	"runtime"
	"testing"

	"github.com/frostyard/hearth/internal/natstest"
)

// TestCleanupClosesHeldConnection checks the cleanup of 1 test's server: it
// closes the connection that Hold kept waiting, so that a test that ends after
// Hold leaks nothing, and the client reads the end of the stream.
func TestCleanupClosesHeldConnection(t *testing.T) {
	var (
		srv  *natstest.Server
		conn net.Conn
	)
	// The sub-test's server is cleaned up when the sub-test ends, and the
	// connection stays open for the outer test.
	t.Run("server", func(t *testing.T) {
		srv = natstest.Start(t, "notes")
		// The socket keeps every new connection unspliced: the client reads
		// nothing, and it waits.
		srv.Hold()
		var err error
		conn, err = net.Dial("unix", srv.Socket)
		if err != nil {
			t.Fatalf("dial the socket: %v", err)
		}
		// The server takes the connection on its own goroutine.
		for srv.OpenConns() == 0 {
			runtime.Gosched()
		}
	})
	if conn == nil {
		t.Fatal("the sub-test opened no connection")
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close the connection: %v", err)
		}
	}()

	// The cleanup closed the connection, and the server holds none.
	if got := srv.OpenConns(); got != 0 {
		t.Errorf("the server holds %d connection(s) after its cleanup, want none", got)
	}

	// The client reads the end of the stream, and it does not block.
	if n, err := conn.Read(make([]byte, 1)); err == nil {
		t.Errorf("the connection of a closed server read %d byte(s), want the end of the stream", n)
	}

	// Release after the cleanup is harmless: the connection is closed, and there
	// is nothing left to splice.
	srv.Release()
}
