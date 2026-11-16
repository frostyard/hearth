// Package broker is hearth's transport to the broker that lodge embeds: it
// publishes each event to the NATS server, through lodge's Unix socket, and waits
// for JetStream's acknowledgement before it returns (lodge ADR-0022).
//
// The event's id is the message's id, so a publisher that sends 1 event twice
// stores 1 message: JetStream drops a duplicate id within its window. The
// event's envelope is the message's data, and it is the CloudEvents structured
// JSON form, so a consumer parses it with events.Parse.
package broker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/frostyard/hearth/events"
)

// The client's constants.
const (
	// defaultTimeout is how long 1 publish waits for JetStream's
	// acknowledgement when the context has no deadline.
	defaultTimeout = 10 * time.Second
	// contentType is the media type of 1 event's data.
	contentType = "application/cloudevents+json"
)

// Options configure the transport.
type Options struct {
	// Socket is the broker's Unix socket, such as /run/lodge/nats.sock.
	Socket string
	// User is the connection's user field, or "". lodge's development root
	// reads the app's id from it.
	User string
	// Name is the connection's name, or "".
	Name string
	// Timeout is how long Publish waits for JetStream's acknowledgement when
	// the context has no deadline. 0 means 10 s.
	Timeout time.Duration
	// ReconnectWait is how long the connection waits between 2 attempts to
	// reconnect after the broker ends it. 0 means the client's own default.
	ReconnectWait time.Duration
	// Logger gets the connection's asynchronous errors, such as a permissions
	// violation, and its disconnects and reconnects. nil means
	// slog.Default().
	Logger *slog.Logger
}

// Transport publishes an app's events to the broker.
type Transport struct {
	// conn is the client's connection, which reconnects forever.
	conn *nats.Conn
	// js is the client's JetStream client, which waits for the
	// acknowledgement.
	js jetstream.JetStream
	// timeout is how long Publish waits for the acknowledgement when the
	// context has no deadline.
	timeout time.Duration
}

// socketDialer dials 1 Unix socket, and it reads no address of a URL: the
// broker is lodge's socket, and lodge has no port.
type socketDialer struct {
	// path is the socket's path.
	path string
}

// Dial implements nats.CustomDialer.
func (d socketDialer) Dial(_, _ string) (net.Conn, error) {
	conn, err := net.Dial("unix", d.path)
	if err != nil {
		return nil, fmt.Errorf("events/broker: dial the socket %s: %w", d.path, err)
	}
	return conn, nil
}

// Dial connects to the broker on its socket. The connection reconnects forever,
// because the broker may restart under the app.
func Dial(opts Options) (*Transport, error) {
	if opts.Socket == "" {
		return nil, errors.New("events/broker: the broker's socket is required")
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	natsOpts := []nats.Option{
		nats.SetCustomDialer(socketDialer{path: opts.Socket}),
		nats.Timeout(timeout),
		nats.MaxReconnects(-1),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			log.Error("the broker's connection reported an error", "err", err)
		}),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err == nil {
				log.Debug("the broker's connection closed")
				return
			}
			log.Warn("the broker's connection ended", "err", err)
		}),
		nats.ReconnectHandler(func(conn *nats.Conn) {
			log.Info("the broker's connection is back", "url", conn.ConnectedUrl())
		}),
	}
	if opts.ReconnectWait > 0 {
		// A given wait is exact: the jitter of the client would make it a
		// range, and a test could not tell when the next attempt comes.
		natsOpts = append(natsOpts, nats.ReconnectWait(opts.ReconnectWait), nats.ReconnectJitter(0, 0))
	}
	if opts.Name != "" {
		natsOpts = append(natsOpts, nats.Name(opts.Name))
	}
	if opts.User != "" {
		natsOpts = append(natsOpts, nats.UserInfo(opts.User, ""))
	}
	// The URL names no address: the dialer reads the socket.
	conn, err := nats.Connect("", natsOpts...)
	if err != nil {
		return nil, fmt.Errorf("events/broker: connect to the socket %s: %w", opts.Socket, err)
	}
	js, err := jetstream.New(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("events/broker: make the JetStream client: %w", err)
	}
	return &Transport{conn: conn, js: js, timeout: timeout}, nil
}

// Publish implements events.Transport: it publishes the event on its topic, with
// the event's id as the message's id, and it returns nil only once JetStream has
// acknowledged the message. A duplicate acknowledgement is a success, because the
// stream holds the event 1 time.
func (t *Transport) Publish(ctx context.Context, e events.Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	data, err := e.MarshalJSON()
	if err != nil {
		return err
	}
	message := &nats.Msg{Subject: e.Topic, Data: data, Header: nats.Header{}}
	message.Header.Set(jetstream.MsgIDHeader, e.ID)
	message.Header.Set("Content-Type", contentType)
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}
	if _, err := t.js.PublishMsg(ctx, message); err != nil {
		return fmt.Errorf("events/broker: publish the event %s on %s: %w", e.ID, e.Topic, err)
	}
	return nil
}

// Close closes the connection. The broker holds every event that Publish
// acknowledged, so a close loses no event.
func (t *Transport) Close() {
	t.conn.Close()
}
