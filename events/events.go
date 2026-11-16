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
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json/v2"
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

// MaxEnvelopeLen is the longest envelope, in bytes: well below the 64 KiB
// payload that lodge's broker takes, which also carries the message's headers,
// so that 1 event can never be a row that no transport accepts.
const MaxEnvelopeLen = 32 << 10

// upperHex is the upper-case hex digit of each 4-bit value, which
// EncodeSegment writes.
const upperHex = "0123456789ABCDEF"

// ErrNoCaller is the error of an event that has no author: the caller of the
// context is the event's only author.
var ErrNoCaller = errors.New("events: no caller in the context, so the event has no author")

var (
	// appPattern is the pattern of an app's id.
	appPattern = regexp.MustCompile(`^[a-z][a-z0-9]{0,15}$`)
	// uuidPattern is the text form of a UUID: 32 lower-case hex digits in 5
	// groups, 8-4-4-4-12.
	uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
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
	event := Event{
		ID:       id,
		Source:   "/" + spec.App,
		Type:     spec.Type,
		Subject:  subject,
		Time:     now.UTC(),
		Author:   caller.Principal,
		Selector: canonical,
		Topic:    topic,
	}
	if err := event.Validate(); err != nil {
		return Event{}, err
	}
	return event, nil
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
// the events that 1 grant item's capability covers: 1 filter for each event spec
// whose read permission is the item's capability, in the order of the specs.
//
// An event spec with no selector carries no path, so its filter is its topic
// alone. A spec with a selector reads the item's path for that selector: the
// root path gives the spec's topic and the events below it, and any other path
// adds its 1 segment for each segment of the path. 1 item that names another
// selector at a path that is not the root covers no event of that spec, because
// the event carries no value of that selector, which is the root.
//
// A capability that no spec has gives an empty, non-nil list. Each spec that the
// item's capability covers is checked, and a path that is no path is an error.
func ReadFilters(specs []Spec, item authz.Item) ([]string, error) {
	filters := make([]string, 0, len(specs))
	for _, spec := range specs {
		if spec.ReadPermission != item.Cap {
			continue
		}
		if err := checkSpec(spec); err != nil {
			return nil, err
		}
		if !coversAnySelector(spec, item) {
			continue
		}
		if spec.Selector == "" {
			filters = append(filters, spec.Topic)
			continue
		}
		path := item.Sel[spec.Selector]
		if path == "" || path == authz.Root {
			filters = append(filters, spec.Topic, spec.Topic+".>")
			continue
		}
		cleaned, err := authz.CleanPath(path)
		if err != nil {
			return nil, fmt.Errorf("events: %s: the read filters: %w", typeName(spec), err)
		}
		var out strings.Builder
		out.WriteString(spec.Topic)
		for segment := range strings.SplitSeq(cleaned[1:], "/") {
			out.WriteByte('.')
			out.WriteString(EncodeSegment(segment))
		}
		filter := out.String()
		filters = append(filters, filter, filter+".>")
	}
	return filters, nil
}

// coversAnySelector reports whether 1 grant item covers the events of 1 spec: an
// item that names another selector at a path that is not the root covers no
// event of that spec, because the event carries no value of that selector.
func coversAnySelector(spec Spec, item authz.Item) bool {
	for name, path := range item.Sel {
		if name == spec.Selector || path == "" || path == authz.Root {
			continue
		}
		return false
	}
	return true
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

// Validate checks 1 event before its change commits, and before a transport
// publishes it: every required member is there, the topic and the envelope are
// within their limits, and the id is a lower-case UUID.
//
// A writer of the change's transaction calls it, so an event that no transport
// could take fails inside the transaction, and no row blocks the relay.
func (e Event) Validate() error {
	name := typeName(Spec{Type: e.Type})
	switch {
	case e.ID == "":
		return fmt.Errorf("events: %s: the id is required", name)
	case !uuidPattern.MatchString(e.ID):
		return fmt.Errorf("events: %s: the id %q is not a lower-case UUID", name, e.ID)
	case e.Source == "":
		return fmt.Errorf("events: %s: the source is required", name)
	case e.Type == "":
		return fmt.Errorf("events: %s: the type is required", name)
	case e.Subject == "":
		return fmt.Errorf("events: %s: the subject (the object's id) is required", name)
	case e.Author == "":
		return fmt.Errorf("events: %s: the author is required", name)
	case e.Time.IsZero():
		return fmt.Errorf("events: %s: the time is required", name)
	case e.Topic == "":
		return fmt.Errorf("events: %s: the topic is required", name)
	case len(e.Topic) > MaxTopicLen:
		return fmt.Errorf("events: %s: the topic is %d bytes, and the limit is %d", name, len(e.Topic), MaxTopicLen)
	case !literalSubject(e.Topic):
		return fmt.Errorf("events: %s: the topic %q is no literal subject of the broker", name, e.Topic)
	}
	data, err := e.MarshalJSON()
	if err != nil {
		return err
	}
	if len(data) > MaxEnvelopeLen {
		return fmt.Errorf("events: %s: the envelope is %d bytes, and the limit is %d", name, len(data), MaxEnvelopeLen)
	}
	// The envelope must read back as the same event: 1 member that no reader
	// can parse, such as a time outside RFC 3339's range, would be a row that
	// the relay publishes and no consumer reads.
	parsed, err := Parse(data)
	if err != nil {
		return fmt.Errorf("events: %s: the envelope does not read back: %w", name, err)
	}
	if !sameMembers(e, parsed) {
		return fmt.Errorf("events: %s: the envelope does not read back as the event", name)
	}
	return nil
}

// literalSubject reports whether 1 topic is 1 literal subject of the broker: no
// token is empty, and no byte of a token is a space, a control byte, "*" or ">",
// which NATS refuses or reads as 1 pattern.
func literalSubject(topic string) bool {
	for token := range strings.SplitSeq(topic, ".") {
		if token == "" {
			return false
		}
		for i := range len(token) {
			b := token[i]
			if b < 0x21 || b == 0x7f || b == '*' || b == '>' {
				return false
			}
		}
	}
	return true
}

// sameMembers reports whether 2 events hold the same members, except the topic,
// which the envelope does not carry. 2 times are the same when they are the same
// instant, whatever their location.
func sameMembers(a, b Event) bool {
	return a.ID == b.ID &&
		a.Source == b.Source &&
		a.Type == b.Type &&
		a.Subject == b.Subject &&
		a.Author == b.Author &&
		a.Selector == b.Selector &&
		a.Time.Equal(b.Time)
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
// It is strict: an unknown member, a member that the form requires and the data
// lacks, a duplicate member, a specversion other than SpecVersion and a bad time
// are errors, and a member's name is case-sensitive. The event's topic is not in
// the form, so it is "".
func Parse(data []byte) (Event, error) {
	var raw envelope
	if err := json.Unmarshal(data, &raw, json.RejectUnknownMembers(true)); err != nil {
		return Event{}, fmt.Errorf("events: read the envelope: %w", err)
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
