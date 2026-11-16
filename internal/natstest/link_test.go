package natstest

import (
	"net"
	"testing"
)

// TestForgetBeforeSplice makes a cleanup come between the moment that start
// publishes a link's server connection and the start of its splice: the splice
// must end at once, and the client must read the end of the stream, with no
// panic.
func TestForgetBeforeSplice(t *testing.T) {
	s := Start(t, "notes")
	forgot := make(chan struct{})
	s.mu.Lock()
	s.beforeSplice = func(l *link) {
		s.forget(l)
		close(forgot)
	}
	s.mu.Unlock()
	conn, err := net.Dial("unix", s.Socket)
	if err != nil {
		t.Fatalf("dial the socket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	<-forgot
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("the client read from a link that the server forgot")
	}
	if got := s.OpenConns(); got != 0 {
		t.Errorf("the server holds %d connection(s), want none", got)
	}
}

// TestForgetBeforeStart forgets a link before start makes its server
// connection, as a Drop that comes first does: start must close the server's
// connection, and must not publish it.
func TestForgetBeforeStart(t *testing.T) {
	s := Start(t, "notes")
	client, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	l := &link{client: client}
	s.mu.Lock()
	s.links[l] = true
	s.mu.Unlock()
	s.forget(l)
	s.start(l)
	s.mu.Lock()
	server := l.server
	s.mu.Unlock()
	if server != nil {
		t.Error("start published a server connection for a link that was forgotten")
	}
	if got := s.OpenConns(); got != 0 {
		t.Errorf("the server holds %d connection(s), want none", got)
	}
}
