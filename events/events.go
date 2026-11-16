// Package events is hearth's event runtime: 1 event is a CloudEvents 1.0
// envelope with no data, its author is the caller of the change's context, and
// its topic names the app, the object, the verb and the segments of the
// object's path.
//
// An app writes 1 event into the outbox of the change's transaction (Writer),
// and a relay publishes it to a Transport: in process (package inproc), or to
// the broker on lodge's socket (package broker). A generated publisher
// (protoc-gen-hearth-events) gives the app 1 Spec and 2 functions for each
// event message of its Protobuf files.
package events

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/frostyard/hearth/authz"
)

// SpecVersion is the CloudEvents version of every event of hearth.
const SpecVersion = "1.0"

// MaxTopicLen is the longest topic, in bytes. A longer topic is an error, so
// that 1 event can never block the relay.
const MaxTopicLen = 1024

// upperHex is the upper-case hex digit of each 4-bit value, which
// EncodeSegment writes.
const upperHex = "0123456789ABCDEF"

// ErrNoCaller is the error of an event that has no author: the caller of the
// context is the event's only author.
var ErrNoCaller = errors.New("events: no caller in the context, so the event has no author")

var (
	// appPattern is the pattern of an app's id.
	appPattern = regexp.MustCompile(`^[a-z][a-z0-9]{0,15}$`)
	// selectorPattern is the pattern of a selector's name.
	selectorPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	// typePrefix is the start of every event type, before the app's id.
	typePrefix = "org.frostyard."
)

// Spec is 1 event type of an app; the plugin generates 1 for each event
// message.
type Spec struct {
	// App is the app's id, such as "demo".
	App string
	// Type is the event's CloudEvents type, such as
	// "org.frostyard.demo.note.created".
	Type string
	// Topic is the event's topic without the path: "<app>.<object>.<verb>",
	// and ".<selector>" when the event carries a selector.
	Topic string
	// Selector is the selector's name, such as "group", or "" when the event
	// carries none.
	Selector string
	// ReadPermission is the capability that a consumer of the event needs.
	ReadPermission string
}

// Event is 1 CloudEvents 1.0 event with no data.
type Event struct {
	// ID is a UUIDv7: the event's own id, never the object's.
	ID string
	// Source is the app's name: "/" and the app's id.
	Source string
	// Type is the event's CloudEvents type.
	Type string
	// Subject is the object's id.
	Subject string
	// Time is when the event was made, in UTC.
	Time time.Time
	// Author is the caller's principal, or "local" for the app's own user.
	Author string
	// Selector is the object's canonical path, or "" for an event that
	// carries no selector.
	Selector string
	// Topic is the broker's topic. It is not in the envelope.
	Topic string
}

// Writer writes 1 event in the change's transaction: the outbox.
type Writer interface {
	// Write writes 1 event in the transaction of the context.
	Write(ctx context.Context, e Event) error
}

// Transport publishes 1 event. Publish returns nil only once the transport
// holds the event: for the broker, that is JetStream's acknowledgement.
type Transport interface {
	// Publish publishes 1 event.
	Publish(ctx context.Context, e Event) error
}

// New returns the event of 1 change, made now.
//
// It is NewAt with the wall clock and the system's source of random bytes, so
// that a test makes an event of a fixed time and a fixed id with NewAt.
func New(ctx context.Context, spec Spec, subject, path string) (Event, error) {
	return NewAt(ctx, spec, subject, path, time.Now(), rand.Reader)
}

// NewAt returns the event of 1 change at 1 time, with the id that random
// gives the extra bytes of.
//
// The author is the caller of the context, and nothing else: a context with
// no caller, or a caller with no principal, is an error that wraps ErrNoCaller.
// The event's selector is the canonical form of path, or "" when the event
// carries no selector.
func NewAt(ctx context.Context, spec Spec, subject, path string, now time.Time, random io.Reader) (Event, error) {
	caller, ok := authz.CallerFrom(ctx)
	if !ok || caller.Principal == "" {
		return Event{}, fmt.Errorf("%w: %s", ErrNoCaller, typeName(spec))
	}
	if subject == "" {
		return Event{}, specErrorf(spec, "the subject (the object's id) is required")
	}
	topic, canonical, err := TopicOf(spec, path)
	if err != nil {
		return Event{}, err
	}
	id, err := newID(now, random)
	if err != nil {
		return Event{}, fmt.Errorf("events: %s: %w", typeName(spec), err)
	}
	return Event{
		ID:       id,
		Source:   "/" + spec.App,
		Type:     spec.Type,
		Subject:  subject,
		Time:     now.UTC(),
		Author:   caller.Principal,
		Selector: canonical,
		Topic:    topic,
	}, nil
}

// TopicOf returns the topic of 1 event and the canonical form of its path: the
// spec's topic, and 1 segment for each segment of the path. The root path adds
// no segment.
//
// It checks the spec, the path and the topic's length. An event that carries
// no selector has no path, so a path is then an error.
func TopicOf(spec Spec, path string) (string, string, error) {
	if err := checkSpec(spec); err != nil {
		return "", "", err
	}
	canonical, err := canonicalPath(spec, path)
	if err != nil {
		return "", "", err
	}
	topic := spec.Topic
	if canonical != "" && canonical != authz.Root {
		var out strings.Builder
		out.WriteString(spec.Topic)
		for segment := range strings.SplitSeq(canonical[1:], "/") {
			out.WriteByte('.')
			out.WriteString(EncodeSegment(segment))
		}
		topic = out.String()
	}
	if len(topic) > MaxTopicLen {
		return "", "", specErrorf(spec, "the topic is %d bytes, and the limit is %d", len(topic), MaxTopicLen)
	}
	return topic, canonical, nil
}

// EncodeSegment returns 1 path segment as 1 topic token: every byte outside
// `[A-Za-z0-9-]` is percent-encoded with upper-case hex.
func EncodeSegment(segment string) string {
	var out strings.Builder
	out.Grow(len(segment))
	for i := range len(segment) {
		b := segment[i]
		if isTokenByte(b) {
			out.WriteByte(b)
			continue
		}
		out.WriteByte('%')
		out.WriteByte(upperHex[b>>4])
		out.WriteByte(upperHex[b&0x0f])
	}
	return out.String()
}

// ReadFilters returns the broker filters that a consumer subscribes to, to read
// the events of app at path of the selector: every event of the app at the root
// path, and 2 filters otherwise, for the path and for what is below it.
//
// It checks the app's id, the selector's name and the path.
func ReadFilters(app, selector, path string) ([]string, error) {
	switch {
	case !appPattern.MatchString(app):
		return nil, fmt.Errorf("events: the app %q must match %s", app, appPattern)
	case !selectorPattern.MatchString(selector):
		return nil, fmt.Errorf("events: the selector %q must match %s", selector, selectorPattern)
	}
	if path == "" || path == authz.Root {
		return []string{app + ".>"}, nil
	}
	cleaned, err := authz.CleanPath(path)
	if err != nil {
		return nil, fmt.Errorf("events: the read filters of %s: %w", app, err)
	}
	var out strings.Builder
	out.WriteString(app)
	out.WriteString(".*.*.")
	out.WriteString(selector)
	for segment := range strings.SplitSeq(cleaned[1:], "/") {
		out.WriteByte('.')
		out.WriteString(EncodeSegment(segment))
	}
	filter := out.String()
	return []string{filter, filter + ".>"}, nil
}

// Match reports whether 1 broker filter takes 1 topic: the 2 split on ".", "*"
// matches exactly 1 token, and ">" as the filter's last token matches 1 or more
// tokens.
func Match(filter, topic string) bool {
	filters := strings.Split(filter, ".")
	tokens := strings.Split(topic, ".")
	for i, token := range filters {
		if token == ">" {
			return i < len(tokens)
		}
		if i >= len(tokens) {
			return false
		}
		if token != "*" && token != tokens[i] {
			return false
		}
	}
	return len(filters) == len(tokens)
}

// ValidFilter checks 1 broker filter: it is not empty, no token is empty, ">"
// is the last token only, and it holds no space.
func ValidFilter(filter string) error {
	if filter == "" {
		return errors.New("events: the filter is empty")
	}
	if strings.ContainsAny(filter, " \t") {
		return fmt.Errorf("events: the filter %q holds a space", filter)
	}
	tokens := strings.Split(filter, ".")
	for i, token := range tokens {
		switch {
		case token == "":
			return fmt.Errorf("events: the filter %q holds an empty token", filter)
		case token == ">" && i != len(tokens)-1:
			return fmt.Errorf("events: the filter %q holds \">\" before its last token", filter)
		}
	}
	return nil
}

// envelope is the CloudEvents structured JSON form of 1 event, in the order of
// that form's members.
type envelope struct {
	// SpecVersion is the CloudEvents version.
	SpecVersion string `json:"specversion"`
	// ID is the event's id.
	ID string `json:"id"`
	// Source is the app's name.
	Source string `json:"source"`
	// Type is the event's type.
	Type string `json:"type"`
	// Subject is the object's id.
	Subject string `json:"subject"`
	// Time is the event's time in RFC 3339 with nanoseconds.
	Time string `json:"time"`
	// Author is the caller's principal.
	Author string `json:"author"`
	// Selector is the object's canonical path, which an event without a
	// selector does not carry.
	Selector string `json:"selector,omitempty"`
}

// MarshalJSON implements json.Marshaler: the event's CloudEvents structured
// JSON form, with no data.
func (e Event) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(envelope{
		SpecVersion: SpecVersion,
		ID:          e.ID,
		Source:      e.Source,
		Type:        e.Type,
		Subject:     e.Subject,
		Time:        e.Time.UTC().Format(time.RFC3339Nano),
		Author:      e.Author,
		Selector:    e.Selector,
	})
	if err != nil {
		return nil, fmt.Errorf("events: encode the event %s: %w", e.ID, err)
	}
	return data, nil
}

// Parse reads 1 event from its CloudEvents structured JSON form.
//
// It is strict: an unknown member, a member that the form requires and the
// data lacks, a specversion other than SpecVersion and a bad time are errors.
// The event's topic is not in the form, so it is "".
func Parse(data []byte) (Event, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var raw envelope
	if err := decoder.Decode(&raw); err != nil {
		return Event{}, fmt.Errorf("events: read the envelope: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Event{}, errors.New("events: read the envelope: the data after the JSON object is not allowed")
	}
	if raw.SpecVersion != SpecVersion {
		return Event{}, fmt.Errorf("events: read the envelope: specversion is %q, and it must be %q", raw.SpecVersion, SpecVersion)
	}
	for _, member := range []struct {
		name  string
		value string
	}{
		{"id", raw.ID},
		{"source", raw.Source},
		{"type", raw.Type},
		{"subject", raw.Subject},
		{"time", raw.Time},
		{"author", raw.Author},
	} {
		if member.value == "" {
			return Event{}, fmt.Errorf("events: read the envelope: the member %q is required", member.name)
		}
	}
	when, err := time.Parse(time.RFC3339Nano, raw.Time)
	if err != nil {
		return Event{}, fmt.Errorf("events: read the envelope: the member \"time\" is %q, which is no RFC 3339 time: %w", raw.Time, err)
	}
	return Event{
		ID:       raw.ID,
		Source:   raw.Source,
		Type:     raw.Type,
		Subject:  raw.Subject,
		Time:     when.UTC(),
		Author:   raw.Author,
		Selector: raw.Selector,
	}, nil
}

// checkSpec checks 1 event's spec: the app's id, the type, the topic and the
// topic's selector.
func checkSpec(spec Spec) error {
	prefix := typePrefix + spec.App + "."
	switch {
	case !appPattern.MatchString(spec.App):
		return specErrorf(spec, "the app %q must match %s", spec.App, appPattern)
	case !strings.HasPrefix(spec.Type, prefix) || len(spec.Type) == len(prefix):
		return specErrorf(spec, "the type %q must be %q followed by the event's name", spec.Type, prefix)
	case !strings.HasPrefix(spec.Topic, spec.App+"."):
		return specErrorf(spec, "the topic %q must start with %q", spec.Topic, spec.App+".")
	case spec.Selector != "" && !strings.HasSuffix(spec.Topic, "."+spec.Selector):
		return specErrorf(spec, "the topic %q must end with %q, the event's selector", spec.Topic, "."+spec.Selector)
	}
	return nil
}

// canonicalPath returns the canonical form of 1 event's path: "" for an event
// that carries no selector, and the root path for an absent path.
func canonicalPath(spec Spec, path string) (string, error) {
	if spec.Selector == "" {
		if path != "" {
			return "", specErrorf(spec, "the event carries no selector, so it has no path, and the path is %q", path)
		}
		return "", nil
	}
	if path == "" {
		return authz.Root, nil
	}
	cleaned, err := authz.CleanPath(path)
	if err != nil {
		return "", fmt.Errorf("events: %s: %w", typeName(spec), err)
	}
	return cleaned, nil
}

// newID returns a UUIDv7 (RFC 9562) of 1 time: its 48-bit Unix millisecond
// count, version 7, variant 10, and the rest from random.
func newID(now time.Time, random io.Reader) (string, error) {
	var raw [16]byte
	if _, err := io.ReadFull(random, raw[:]); err != nil {
		return "", fmt.Errorf("read the random bytes of the id: %w", err)
	}
	milliseconds := uint64(now.UnixMilli()) & 0xffffffffffff
	raw[0] = byte(milliseconds >> 40)
	raw[1] = byte(milliseconds >> 32)
	raw[2] = byte(milliseconds >> 24)
	raw[3] = byte(milliseconds >> 16)
	raw[4] = byte(milliseconds >> 8)
	raw[5] = byte(milliseconds)
	raw[6] = raw[6]&0x0f | 0x70
	raw[8] = raw[8]&0x3f | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

// typeName returns 1 event's type for an error: the type, or "the event" when
// the spec names none.
func typeName(spec Spec) string {
	if spec.Type == "" {
		return "the event"
	}
	return spec.Type
}

// specErrorf returns an error about 1 event: "events: <type>: <text>".
func specErrorf(spec Spec, format string, args ...any) error {
	return fmt.Errorf("events: %s: %s", typeName(spec), fmt.Sprintf(format, args...))
}

// isTokenByte reports whether 1 byte is a topic token's own byte, which needs no
// encoding.
func isTokenByte(b byte) bool {
	switch {
	case b >= 'A' && b <= 'Z':
		return true
	case b >= 'a' && b <= 'z':
		return true
	case b >= '0' && b <= '9':
		return true
	}
	return b == '-'
}
