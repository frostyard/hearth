// Command notelite is the example app of hearth: 1 small note service that
// serves the MCP tools that hearth generates, over stdio.
//
// It publishes the app's events to the bus of its own process, and logs them
// where a developer sees them. With -broker it publishes them to lodge's broker
// instead.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/frostyard/hearth/authz"
	"github.com/frostyard/hearth/events"
	"github.com/frostyard/hearth/events/broker"
	"github.com/frostyard/hearth/events/inproc"
	"github.com/frostyard/hearth/events/outbox"
	"github.com/frostyard/hearth/mcpserver"

	"example.com/notes-lite"
	notesv1 "example.com/notes-lite/gen/example/notes/v1"
)

// The command's exit codes: 0 when the app served its tools, 1 when it failed,
// and 2 for 1 usage problem.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// usageText is the command's help.
const usageText = `notelite serves the example app's MCP tools over stdio.

Usage:
  notelite [-db FILE] [-broker SOCKET] mcp

Flags:
  -db FILE        the SQLite file (default notelite.db)
  -broker SOCKET  publish the events to lodge's broker on SOCKET, and not to the
                  bus of this process
`

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run runs the command, and it returns its exit code.
func run(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("notelite", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", "notelite.db", "the SQLite file")
	socket := flags.String("broker", "", "publish the events to lodge's broker on this socket")
	flags.Usage = func() {
		_, _ = fmt.Fprint(stderr, usageText)
	}
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() != 1 || flags.Arg(0) != "mcp" {
		_, _ = fmt.Fprintf(stderr, "notelite: the command is mcp\n\n%s", usageText)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := serve(ctx, *dbPath, *socket, stderr); err != nil {
		_, _ = fmt.Fprintf(stderr, "notelite: %v\n", err)
		return exitError
	}
	return exitOK
}

// serve opens the app's database, publishes its events, and serves its MCP
// tools over stdio. It returns nil when the client closes the stream, and when
// a signal ends the app.
func serve(ctx context.Context, dbPath, socket string, stderr io.Writer) error {
	log := slog.New(slog.NewTextHandler(stderr, nil))
	db, err := notes.Open(ctx, dbPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Error("the database did not close", "err", err)
		}
	}()
	transport, closeTransport, err := eventTransport(socket, log, stderr)
	if err != nil {
		return err
	}
	defer closeTransport()

	// The relay publishes each event that 1 transaction wrote, and it wakes
	// when the app notifies it. It stops before the database closes.
	ctx, cancel := context.WithCancel(ctx)
	relay := outbox.NewRelay(db, transport, outbox.Options{Logger: log})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		if err := relay.Run(ctx); err != nil {
			log.Error("the relay stopped", "err", err)
		}
	}()
	defer func() {
		cancel()
		<-stopped
	}()

	service := &notes.Service{DB: db, Notify: relay.Notify}
	server := mcpserver.NewServer(&mcp.Implementation{Name: "notelite", Version: "0.1.0"},
		authz.LocalCaller(), notesv1.NoteServiceTools(service.API()))
	if err := mcpserver.ServeStdio(ctx, server, os.Stdin, os.Stdout); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
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
