// Command notelite is the example app of hearth: 1 small note service that
// serves the API, the MCP tools and the events that hearth generates.
//
// It serves the app's API over Connect on its private socket, and on lodge's
// socket when -lodge is set. It serves the MCP tools over stdio. It publishes
// the app's events to the bus of its own process, and logs them where a
// developer sees them; with -broker it publishes them to lodge's broker
// instead.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/frostyard/hearth/authz"
	"github.com/frostyard/hearth/connectserver"
	"github.com/frostyard/hearth/events"
	"github.com/frostyard/hearth/events/broker"
	"github.com/frostyard/hearth/events/inproc"
	"github.com/frostyard/hearth/events/outbox"
	"github.com/frostyard/hearth/lodge"
	"github.com/frostyard/hearth/mcpserver"

	"example.com/notes-lite"
	notesv1 "example.com/notes-lite/gen/example/notes/v1"
	"example.com/notes-lite/gen/example/notes/v1/notesv1connect"
)

// The command's exit codes: 0 when the app served its tools or its API, 1 when
// it failed, and 2 for 1 usage problem.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// version is the app's version, in its manifest and in its health check.
const version = "0.1.0"

// usageText is the command's help.
const usageText = `notelite serves the example app's API over Connect, and its MCP tools over
stdio.

Usage:
  notelite [-db FILE] [-broker SOCKET] mcp
  notelite [-db FILE] [-broker SOCKET] [-socket PATH] [-lodge PATH] [-lodge-uid UID] serve

Commands:
  mcp    serve the app's MCP tools over stdio, as the app's own user
  serve  serve the app's API over Connect on its private socket, and on lodge's
         socket too when -lodge is set

Flags:
  -db FILE        the SQLite file (default notelite.db)
  -broker SOCKET  publish the events to lodge's broker on SOCKET, and not to the
                  bus of this process
  -socket PATH    the app's private socket, of the command serve (default
                  notelite.sock)
  -lodge PATH     serve the app's API on lodge's socket at PATH too
  -lodge-uid UID  the user id that the lodge socket admits (default: the user id
                  of this process, which is what a developer needs)
`

// options are the command's flags.
type options struct {
	// db is the app's SQLite file.
	db string
	// broker is lodge's broker socket, or "" for the bus of this process.
	broker string
	// socket is the app's private socket.
	socket string
	// lodge is lodge's socket, or "" for no lodge socket.
	lodge string
	// lodgeUID is the user id that lodge's socket admits, or -1 for this
	// process's own.
	lodgeUID int
}

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run runs the command, and it returns its exit code.
func run(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("notelite", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", "notelite.db", "the SQLite file")
	brokerSocket := flags.String("broker", "", "publish the events to lodge's broker on this socket")
	socketPath := flags.String("socket", "notelite.sock", "the app's private socket")
	lodgePath := flags.String("lodge", "", "serve the app's API on lodge's socket at this path")
	lodgeUID := flags.Int("lodge-uid", -1, "the user id that the lodge socket admits")
	flags.Usage = func() {
		_, _ = fmt.Fprint(stderr, usageText)
	}
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() != 1 {
		_, _ = fmt.Fprintf(stderr, "notelite: the command is mcp or serve\n\n%s", usageText)
		return exitUsage
	}
	app := options{db: *dbPath, broker: *brokerSocket, socket: *socketPath, lodge: *lodgePath, lodgeUID: *lodgeUID}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch command := flags.Arg(0); command {
	case "mcp":
		err = runMCP(ctx, app, stderr)
	case "serve":
		err = runServe(ctx, app, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "notelite: the command %q is no command\n\n%s", command, usageText)
		return exitUsage
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "notelite: %v\n", err)
		return exitError
	}
	return exitOK
}

// runMCP serves the app's MCP tools over stdio. It returns nil when the client
// closes the stream, and when a signal ends the app.
func runMCP(ctx context.Context, app options, stderr io.Writer) error {
	log := slog.New(slog.NewTextHandler(stderr, nil))
	service, stop, err := open(ctx, app, log, stderr)
	if err != nil {
		return err
	}
	defer stop()

	server := mcpserver.NewServer(&mcp.Implementation{Name: "notelite", Version: version},
		authz.LocalCaller(), notesv1.NoteServiceTools(service.API()))
	if err := mcpserver.ServeStdio(ctx, server, os.Stdin, os.Stdout); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

// runServe serves the app's API over Connect on its private socket, as the
// app's own user, and on lodge's socket when 1 is given. It returns nil when a
// signal ends the app.
func runServe(ctx context.Context, app options, stderr io.Writer) error {
	log := slog.New(slog.NewTextHandler(stderr, nil))
	service, stop, err := open(ctx, app, log, stderr)
	if err != nil {
		return err
	}
	defer stop()

	// The route of the app's service: the generated Connect handler takes the
	// API that the generated permission check gives, so 1 route serves the
	// app's own user and lodge's callers, each with its own caller.
	path, handler := notesv1connect.NewNoteServiceHandler(service.API(), connectserver.HandlerOptions()...)
	// The app's own routes sit outside the adapter: /health is no Connect call
	// and it reads no caller.
	private := http.NewServeMux()
	private.Handle(path, connectserver.Private(path, handler))
	private.HandleFunc("/health", health)
	onLodge := http.NewServeMux()
	onLodge.Handle(connectserver.LodgePrefix, connectserver.Lodge(path, handler))
	onLodge.HandleFunc("/health", health)

	privateSocket, err := listenSocket(app.socket)
	if err != nil {
		return err
	}
	sockets := []socket{{listener: lodge.Listener(privateSocket, os.Getuid()), handler: private}}
	if app.lodge != "" {
		uid := app.lodgeUID
		if uid < 0 {
			uid = os.Getuid()
		}
		lodgeSocket, err := listenSocket(app.lodge)
		if err != nil {
			_ = privateSocket.Close()
			return err
		}
		sockets = append(sockets, socket{listener: lodge.Listener(lodgeSocket, uid), handler: onLodge})
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	served := make(chan error, len(sockets))
	for _, s := range sockets {
		go func() {
			served <- connectserver.Serve(ctx, s.listener, s.handler)
		}()
	}
	// 1 socket that stops ends the app, so that a failure is never silent.
	var first error
	for range sockets {
		if err := <-served; err != nil && first == nil {
			first = err
			cancel()
		}
	}
	return first
}

// socket is 1 socket of the app, and the handler that it serves.
type socket struct {
	// listener admits the connections of 1 user only.
	listener net.Listener
	// handler serves the app's API, and the app's own routes beside it.
	handler http.Handler
}

// listenSocket returns the listener of 1 Unix socket. The listener of
// lodge.Listener admits 1 user only: it closes every other connection before it
// reads a byte of it.
func listenSocket(path string) (net.Listener, error) {
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}
	return listener, nil
}

// health answers the app's health check.
func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"ok":true,"version":"`+version+`","protocol":1}`)
}

// open opens the app's database, its event transport, and the relay that
// publishes each event that 1 committed change wrote. It returns the app, and
// the function that stops the relay and closes the transport and the database.
func open(ctx context.Context, app options, log *slog.Logger, stderr io.Writer) (*notes.Service, func(), error) {
	db, err := notes.Open(ctx, app.db)
	if err != nil {
		return nil, nil, err
	}
	transport, closeTransport, err := eventTransport(app.broker, log, stderr)
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	relayCtx, cancel := context.WithCancel(ctx)
	relay := outbox.NewRelay(db, transport, outbox.Options{Logger: log})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		if err := relay.Run(relayCtx); err != nil {
			log.Error("the relay stopped", "err", err)
		}
	}()
	service := &notes.Service{DB: db, Notify: relay.Notify}
	stop := func() {
		cancel()
		<-stopped
		closeTransport()
		if err := db.Close(); err != nil {
			log.Error("the database did not close", "err", err)
		}
	}
	return service, stop, nil
}

// eventTransport returns the app's event transport, and the function that
// closes it: lodge's broker when socket is set, and the bus of this process
// otherwise, which logs each event to stderr.
func eventTransport(socket string, log *slog.Logger, stderr io.Writer) (events.Transport, func(), error) {
	if socket != "" {
		transport, err := broker.Dial(broker.Options{Socket: socket, Name: "notelite", Logger: log})
		if err != nil {
			return nil, nil, err
		}
		return transport, transport.Close, nil
	}
	bus := inproc.New()
	cancel, err := bus.Subscribe("notelite.>", func(e events.Event) {
		_, _ = fmt.Fprintf(stderr, "event %s of %s at %s\n", e.Type, e.Subject, e.Selector)
	})
	if err != nil {
		return nil, nil, err
	}
	return bus, cancel, nil
}
