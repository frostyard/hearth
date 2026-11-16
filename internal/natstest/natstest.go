// Package natstest runs an embedded NATS server, with JetStream, for the tests
// of hearth's event transports. It is not a test file, so that 2 test packages
// share 1 server type.
//
// The server has the shape that lodge ADR-0022 records: the global account is
// the hub, the app's account imports the app's subjects from it, and the app's
// messages land in 1 JetStream stream named app_<id>. Every connection that is
// not the hub's own client is the app's user: it may publish the app's subjects
// and subscribe its inboxes, and nothing else.
package natstest

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// The server's limits, and the app's stream's, as lodge ADR-0022 records them.
const (
	// maxPayload is the largest message the server takes.
	maxPayload = 64 << 10
	// maxConn is the server's connection cap.
	maxConn = 1024
	// streamMaxAge is how long the app's stream keeps a message.
	streamMaxAge = 7 * 24 * time.Hour
	// streamMaxBytes is how large the app's stream grows before it drops the
	// oldest message.
	streamMaxBytes = 1 << 30
	// duplicateWindow is how long the stream remembers a message's id.
	duplicateWindow = 2 * time.Minute
	// readyTimeout bounds the wait for the server's readiness.
	readyTimeout = 10 * time.Second
)

// Message is 1 message that the app's stream holds.
type Message struct {
	// Subject is the message's subject.
	Subject string
	// Header is the message's headers.
	Header nats.Header
	// Data is the message's data.
	Data []byte
}

// Server is an embedded broker for 1 app.
type Server struct {
	// Socket is the path of the Unix socket that the app's connections use.
	Socket string
	// app is the app's id, which names its account and its stream.
	app string
	// srv is the server.
	srv *server.Server
	// hub is the hub's own client, which makes the stream and reads it.
	hub *nats.Conn
	// js is the hub's JetStream client.
	js jetstream.JetStream
	// mu guards the hub's secret, the app's account, which Check reads on the
	// server's goroutines, and the socket's connections.
	mu sync.Mutex
	// secret is the token that names the hub's user.
	secret string
	// acc is the app's account.
	acc *server.Account
	// links are the live connections of the socket, which Drop closes.
	links map[*link]bool
	// held is set while the socket holds each new connection unspliced.
	held bool
	// closed is set by the cleanup: no connection is taken after it.
	closed bool
	// beforeSplice, when set, runs after start publishes a link's server
	// connection and before the splice starts: a test of this package makes a
	// cleanup come in between.
	beforeSplice func(*link)
	// waiting are the connections that Hold kept waiting, until Release.
	waiting []*link
}

// link is 1 connection of the socket, and the server's own connection that it is
// spliced to.
type link struct {
	// client is the connection of the socket.
	client net.Conn
	// server is the server's connection. start sets it 1 time, and it never
	// changes after that.
	server net.Conn
	// done is set by forget: a link that is done never gets a server
	// connection, and its splice copies nothing.
	done bool
}

// Check admits 1 connection: the hub's token is the hub's user, and every other
// connection is the app's user, in the app's account.
func (s *Server) Check(c server.ClientAuthentication) bool {
	if c.Kind() != server.CLIENT {
		return false
	}
	var token string
	if opts := c.GetOpts(); opts != nil {
		token = opts.Token
	}
	s.mu.Lock()
	secret, acc := s.secret, s.acc
	s.mu.Unlock()
	if secret != "" && token == secret {
		c.RegisterUser(&server.User{Username: "hub"})
		return true
	}
	if acc == nil {
		return false
	}
	c.RegisterUser(&server.User{
		Username: "app:" + s.app,
		Account:  acc,
		Permissions: &server.Permissions{
			Publish:   &server.SubjectPermission{Allow: []string{s.app + ".>"}},
			Subscribe: &server.SubjectPermission{Allow: []string{"_INBOX.>"}},
		},
	})
	return true
}

// Start starts an embedded server for 1 app, and closes it, its socket and its
// store when the test ends.
func Start(t testing.TB, app string) *Server {
	t.Helper()
	s := &Server{app: app, links: map[*link]bool{}}
	srv, err := server.NewServer(&server.Options{
		ServerName: "natstest",
		// The app reaches the server on a Unix socket whose connections are
		// spliced to connections of the server itself.
		DontListen:                 true,
		JetStream:                  true,
		StoreDir:                   t.TempDir(),
		NoSigs:                     true,
		MaxPayload:                 maxPayload,
		MaxConn:                    maxConn,
		CustomClientAuthentication: s,
	})
	if err != nil {
		t.Fatalf("natstest: make the server: %v", err)
	}
	srv.SetLoggerV2(discardLogger{}, false, false, false)
	srv.Start()
	t.Cleanup(srv.Shutdown)
	if !srv.ReadyForConnections(readyTimeout) {
		t.Fatalf("natstest: the server is not ready")
	}
	s.srv = srv

	// The hub is the global account, and the app's account imports the app's
	// subjects from it.
	hub := srv.GlobalAccount()
	acc, err := srv.RegisterAccount("app_" + app)
	if err != nil {
		t.Fatalf("natstest: make the app's account: %v", err)
	}
	if err := hub.AddServiceExportWithResponse(app+".>", server.Singleton, []*server.Account{acc}); err != nil {
		t.Fatalf("natstest: export the app's subjects: %v", err)
	}
	if err := acc.AddServiceImport(hub, app+".>", app+".>"); err != nil {
		t.Fatalf("natstest: import the app's subjects: %v", err)
	}
	s.mu.Lock()
	s.acc = acc
	s.secret = secret(t)
	s.mu.Unlock()

	// The hub's own client makes the app's stream: 1 stream per app, which
	// remembers a message's id for the duplicate window.
	conn, err := nats.Connect("", nats.InProcessServer(srv), nats.Token(s.secret),
		nats.Name("natstest-hub"), nats.Timeout(readyTimeout))
	if err != nil {
		t.Fatalf("natstest: connect the hub: %v", err)
	}
	t.Cleanup(conn.Close)
	s.hub = conn
	s.js, err = jetstream.New(conn)
	if err != nil {
		t.Fatalf("natstest: connect the hub to JetStream: %v", err)
	}
	if _, err := s.js.CreateStream(t.Context(), jetstream.StreamConfig{
		Name:       "app_" + app,
		Subjects:   []string{app + ".>"},
		MaxAge:     streamMaxAge,
		MaxBytes:   streamMaxBytes,
		Discard:    jetstream.DiscardOld,
		Storage:    jetstream.FileStorage,
		Duplicates: duplicateWindow,
	}); err != nil {
		t.Fatalf("natstest: make the app's stream: %v", err)
	}

	// The app's door: a Unix socket under a short directory, because a socket
	// path is 108 bytes at most.
	dir, err := os.MkdirTemp("", "hearth-natstest-")
	if err != nil {
		t.Fatalf("natstest: make the socket's directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("natstest: remove the socket's directory: %v", err)
		}
	})
	s.Socket = filepath.Join(dir, "nats.sock")
	listener, err := net.Listen("unix", s.Socket)
	if err != nil {
		t.Fatalf("natstest: listen on the socket: %v", err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("natstest: close the socket: %v", err)
		}
	})
	go s.accept(listener)
	// The connections of the socket close first, so that a test that ends after
	// Hold, or with a live connection, leaks nothing.
	t.Cleanup(s.closeLinks)
	return s
}

// closeLinks closes both ends of every live connection of the socket, and of
// every connection that Hold kept waiting. Release after it is harmless.
func (s *Server) closeLinks() {
	s.mu.Lock()
	links := make([]*link, 0, len(s.links)+len(s.waiting))
	for l := range s.links {
		links = append(links, l)
	}
	links = append(links, s.waiting...)
	s.links = map[*link]bool{}
	s.waiting = nil
	s.closed = true
	s.mu.Unlock()
	for _, l := range links {
		s.forget(l)
	}
}

// OpenConns returns how many connections of the socket the server holds: the
// ones it spliced, and the ones that Hold keeps waiting. It is 1 test's view of
// the server's state, and it is 0 once the test's cleanup has run.
func (s *Server) OpenConns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.links) + len(s.waiting)
}

// Count returns how many messages the app's stream holds.
func (s *Server) Count(t testing.TB) uint64 {
	t.Helper()
	info, err := s.stream(t).Info(t.Context())
	if err != nil {
		t.Fatalf("natstest: read the app's stream: %v", err)
	}
	return info.State.Msgs
}

// Messages returns the messages the app's stream holds, in the stream's order.
func (s *Server) Messages(t testing.TB) []Message {
	t.Helper()
	stream := s.stream(t)
	info, err := stream.Info(t.Context())
	if err != nil {
		t.Fatalf("natstest: read the app's stream: %v", err)
	}
	out := make([]Message, 0, info.State.Msgs)
	for seq := info.State.FirstSeq; seq <= info.State.LastSeq; seq++ {
		stored, err := stream.GetMsg(t.Context(), seq)
		if err != nil {
			t.Fatalf("natstest: read the message %d of the app's stream: %v", seq, err)
		}
		out = append(out, Message{Subject: stored.Subject, Header: stored.Header, Data: stored.Data})
	}
	return out
}

// stream returns the app's stream.
func (s *Server) stream(t testing.TB) jetstream.Stream {
	t.Helper()
	stream, err := s.js.Stream(t.Context(), "app_"+s.app)
	if err != nil {
		t.Fatalf("natstest: read the app's stream: %v", err)
	}
	return stream
}

// accept splices each connection of the socket to a connection of the server
// itself: the server has no listener of its own. A connection that Hold keeps
// waits, unspliced, until Release, and the server gives it no connection before
// then: a held connection is 1 socket of the test, and nothing of the server.
func (s *Server) accept(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		l := &link{client: conn}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = conn.Close()
			return
		}
		if s.held {
			s.waiting = append(s.waiting, l)
			s.mu.Unlock()
			continue
		}
		s.links[l] = true
		s.mu.Unlock()
		s.start(l)
	}
}

// start splices 1 connection of the socket to a connection of the server itself.
// A connection that the server cannot take is closed and forgotten.
func (s *Server) start(l *link) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		s.forget(l)
		return
	}
	serverConn, err := s.srv.InProcessConn()
	if err != nil {
		s.forget(l)
		return
	}
	s.mu.Lock()
	if l.done || s.closed {
		// A cleanup or Drop forgot the link while the server made its
		// connection: the connection is closed, and nothing is spliced.
		s.mu.Unlock()
		_ = serverConn.Close()
		return
	}
	l.server = serverConn
	hook := s.beforeSplice
	s.mu.Unlock()
	if hook != nil {
		hook(l)
	}
	go s.splice(l, serverConn)
}

// splice copies each direction of 1 link until 1 of them ends, and then closes
// both. It gets the server's connection from start, so a forget that comes
// first only makes both copies end at once.
func (s *Server) splice(l *link, serverConn net.Conn) {
	go func() {
		_, _ = io.Copy(l.client, serverConn)
		s.forget(l)
	}()
	_, _ = io.Copy(serverConn, l.client)
	s.forget(l)
}

// forget closes both connections of 1 link, and forgets the link. A link that
// was never spliced has no connection of the server, and it closes its socket
// only.
func (s *Server) forget(l *link) {
	s.mu.Lock()
	delete(s.links, l)
	l.done = true
	serverConn := l.server
	s.mu.Unlock()
	_ = l.client.Close()
	if serverConn != nil {
		_ = serverConn.Close()
	}
}

// Drop closes every live connection of the socket, as a broker that restarts
// does. The client reconnects.
func (s *Server) Drop() {
	s.mu.Lock()
	links := make([]*link, 0, len(s.links))
	for l := range s.links {
		links = append(links, l)
	}
	s.mu.Unlock()
	for _, l := range links {
		s.forget(l)
	}
}

// Hold makes each new connection of the socket wait, unspliced, until Release:
// the client then reads no greeting, and it tries again.
func (s *Server) Hold() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.held = true
}

// Release splices the connections that Hold kept waiting, and each new 1 after
// it. It is harmless after the test's cleanup, which closes those connections.
func (s *Server) Release() {
	s.mu.Lock()
	s.held = false
	waiting := s.waiting
	s.waiting = nil
	for _, l := range waiting {
		s.links[l] = true
	}
	s.mu.Unlock()
	for _, l := range waiting {
		s.start(l)
	}
}

// secret returns 1 token of 32 random bytes.
func secret(t testing.TB) string {
	t.Helper()
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatalf("natstest: make the hub's secret: %v", err)
	}
	return hex.EncodeToString(raw[:])
}

// discardLogger is the server's log, which a test does not read.
type discardLogger struct{}

// Noticef discards 1 notice.
func (discardLogger) Noticef(string, ...any) {}

// Warnf discards 1 warning.
func (discardLogger) Warnf(string, ...any) {}

// Errorf discards 1 error.
func (discardLogger) Errorf(string, ...any) {}

// Fatalf discards 1 fatal error.
func (discardLogger) Fatalf(string, ...any) {}

// Debugf discards 1 debug line.
func (discardLogger) Debugf(string, ...any) {}

// Tracef discards 1 trace line.
func (discardLogger) Tracef(string, ...any) {}
