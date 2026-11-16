package events_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/hearth/authz"
	"github.com/frostyard/hearth/events"
)

// spec is the event of 1 created note, filed in a group.
var spec = events.Spec{
	App:            "demo",
	Type:           "org.frostyard.demo.note.created",
	Topic:          "demo.note.created.group",
	Selector:       "group",
	ReadPermission: "demo:read",
}

// noSelector is the event of 1 published note, which carries no selector.
var noSelector = events.Spec{
	App:            "demo",
	Type:           "org.frostyard.demo.note.published",
	Topic:          "demo.note.published",
	ReadPermission: "demo:publish",
}

// local returns a context whose caller is the app's own user.
func local() context.Context {
	return authz.WithCaller(context.Background(), authz.LocalCaller())
}

// fixed is the time of every event of a fixed test: 2023-11-14 22:13:20 UTC.
var fixed = time.Unix(1700000000, 0).UTC()

// fixedRandom is the fixed source of random bytes of a fixed test.
func fixedRandom() io.Reader {
	random := make([]byte, 16)
	for i := range random {
		random[i] = byte(i)
	}
	return bytes.NewReader(random)
}

// TestNewAuthorLocal checks that the local user writes an event as "local".
func TestNewAuthorLocal(t *testing.T) {
	event, err := events.New(local(), spec, "n-1", "/work")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if event.Author != "local" {
		t.Errorf("the author is %q, want local", event.Author)
	}
}

// TestNewAuthorPrincipal checks that the author is the caller's principal.
func TestNewAuthorPrincipal(t *testing.T) {
	grant := authz.LocalCaller().Grant
	ctx := authz.WithCaller(t.Context(), authz.Caller{Principal: "chani", Grant: grant})
	event, err := events.New(ctx, spec, "n-1", "/work")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if event.Author != "chani" {
		t.Errorf("the author is %q, want chani", event.Author)
	}
}

// TestNewNoCaller checks that an event of a context with no caller fails, and
// that a caller with no principal fails too.
func TestNewNoCaller(t *testing.T) {
	if _, err := events.New(t.Context(), spec, "n-1", "/work"); !errors.Is(err, events.ErrNoCaller) {
		t.Errorf("New with no caller = %v, want an error that wraps ErrNoCaller", err)
	}
	ctx := authz.WithCaller(t.Context(), authz.Caller{Grant: authz.LocalCaller().Grant})
	if _, err := events.New(ctx, spec, "n-1", "/work"); !errors.Is(err, events.ErrNoCaller) {
		t.Errorf("New with no principal = %v, want an error that wraps ErrNoCaller", err)
	}
}

// TestNewID checks that an event's id is its own, never the object's, and that
// 2 events of 1 object get 2 ids.
func TestNewID(t *testing.T) {
	ctx := local()
	first, err := events.New(ctx, spec, "n-1", "/work")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	second, err := events.New(ctx, spec, "n-1", "/work")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if first.ID == "n-1" || first.ID == first.Subject {
		t.Errorf("the id %q is the object's id", first.ID)
	}
	if first.ID == second.ID {
		t.Errorf("2 events of n-1 hold the id %q", first.ID)
	}
	if !uuidV7.MatchString(first.ID) {
		t.Errorf("the id %q is not a lower-case UUID", first.ID)
	}
}

// uuidV7 is the shape of a lower-case version 7 UUID.
var uuidV7 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// TestNewAtFixed checks the golden id of a fixed time and a fixed source of
// random bytes: the version is 7 and the variant is 10, as RFC 9562 says.
func TestNewAtFixed(t *testing.T) {
	event, err := events.NewAt(local(), spec, "n-1", "/work", fixed, fixedRandom())
	if err != nil {
		t.Fatalf("NewAt: %v", err)
	}
	if want := "018bcfe5-6800-7607-8809-0a0b0c0d0e0f"; event.ID != want {
		t.Errorf("the id = %q, want %q", event.ID, want)
	}
	if !event.Time.Equal(fixed) || event.Time.Location() != time.UTC {
		t.Errorf("the time = %v, want %v in UTC", event.Time, fixed)
	}
	if !uuidV7.MatchString(event.ID) {
		t.Errorf("the id %q is no version 7 UUID", event.ID)
	}
}

// TestNewAtRandomError checks that a source of random bytes that fails is an
// error.
func TestNewAtRandomError(t *testing.T) {
	failing := errors.New("no entropy")
	_, err := events.NewAt(local(), spec, "n-1", "/work", fixed, errorReader{failing})
	if !errors.Is(err, failing) {
		t.Errorf("NewAt with a failing source of random bytes = %v, want an error that wraps %v", err, failing)
	}
}

// errorReader is a source of random bytes that fails.
type errorReader struct {
	// err is the error that the reader returns.
	err error
}

// Read implements io.Reader.
func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

// TestNewSubject checks that the object's id is required.
func TestNewSubject(t *testing.T) {
	_, err := events.New(local(), spec, "", "/work")
	want := "events: org.frostyard.demo.note.created: the subject (the object's id) is required"
	if err == nil || err.Error() != want {
		t.Errorf("New with no subject = %v, want %q", err, want)
	}
}

// TestTopic checks the topic of an event of each path: 1 segment for each
// segment of the path, percent-encoded, and the root path adds none.
func TestTopic(t *testing.T) {
	for _, tc := range []struct {
		name  string
		path  string
		topic string
		canon string
	}{
		{"root", "/", "demo.note.created.group", "/"},
		{"absent", "", "demo.note.created.group", "/"},
		{"1 segment", "/work", "demo.note.created.group.work", "/work"},
		{"2 segments", "/work/2026", "demo.note.created.group.work.2026", "/work/2026"},
		{"a space", "/Work Notes", "demo.note.created.group.Work%20Notes", "/Work Notes"},
		{"an underscore", "/2026_Q1", "demo.note.created.group.2026%5FQ1", "/2026_Q1"},
		{"a dot", "/a.b", "demo.note.created.group.a%2Eb", "/a.b"},
		{"a percent", "/100%", "demo.note.created.group.100%25", "/100%"},
		{"a hyphen and a digit", "/q-1", "demo.note.created.group.q-1", "/q-1"},
		{"NFC", "/Cafe\u0301", "demo.note.created.group.Caf%C3%A9", "/Caf\u00e9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event, err := events.New(local(), spec, "n-1", tc.path)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if event.Topic != tc.topic {
				t.Errorf("the topic of %q = %q, want %q", tc.path, event.Topic, tc.topic)
			}
			if event.Selector != tc.canon {
				t.Errorf("the path of %q = %q, want %q", tc.path, event.Selector, tc.canon)
			}
		})
	}
}

// TestNoSelector checks the event that carries no selector: it has no path, so
// its topic is the spec's topic and its path is empty.
func TestNoSelector(t *testing.T) {
	event, err := events.New(local(), noSelector, "n-1", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if event.Topic != "demo.note.published" {
		t.Errorf("the topic = %q, want demo.note.published", event.Topic)
	}
	if event.Selector != "" {
		t.Errorf("the path = %q, want no path", event.Selector)
	}
	if _, err := events.New(local(), noSelector, "n-1", "/work"); err == nil {
		t.Error("an event that carries no selector took the path /work")
	}
}

// TestSpec checks that a bad spec is an error that names the event's type.
func TestSpec(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spec  events.Spec
		error string
	}{
		{
			"a capital app",
			events.Spec{App: "Demo", Type: "org.frostyard.demo.note.created", Topic: "Demo.note.created.group", Selector: "group"},
			"the app \"Demo\" must match",
		},
		{
			"an empty app",
			events.Spec{Type: "org.frostyard.demo.note.created", Topic: "demo.note.created.group", Selector: "group"},
			"the app \"\" must match",
		},
		{
			"another app's type",
			events.Spec{App: "demo", Type: "org.frostyard.notes.note.created", Topic: "demo.note.created.group", Selector: "group"},
			"the type \"org.frostyard.notes.note.created\" must be \"org.frostyard.demo.\"",
		},
		{
			"a type with no name",
			events.Spec{App: "demo", Type: "org.frostyard.demo.", Topic: "demo.note.created.group", Selector: "group"},
			"must be \"org.frostyard.demo.\" followed by the event's name",
		},
		{
			"a topic of another app",
			events.Spec{App: "demo", Type: "org.frostyard.demo.note.created", Topic: "notes.note.created.group", Selector: "group"},
			"the topic \"notes.note.created.group\" must start with \"demo.\"",
		},
		{
			"a topic without the selector",
			events.Spec{App: "demo", Type: "org.frostyard.demo.note.created", Topic: "demo.note.created", Selector: "group"},
			"the topic \"demo.note.created\" must end with \".group\"",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := events.New(local(), tc.spec, "n-1", "/work")
			if err == nil {
				t.Fatalf("New succeeded with the spec %+v", tc.spec)
			}
			if !strings.Contains(err.Error(), tc.error) {
				t.Errorf("the error = %q, want it to hold %q", err, tc.error)
			}
			typeName := tc.spec.Type
			if typeName == "" {
				typeName = "the event"
			}
			if !strings.HasPrefix(err.Error(), "events: "+typeName+": ") {
				t.Errorf("the error %q does not name %q", err, typeName)
			}
		})
	}
}

// TestBadPath checks that a path that is no path is a *authz.PathError.
func TestBadPath(t *testing.T) {
	_, err := events.New(local(), spec, "n-1", "work/2026")
	if err == nil {
		t.Fatal("New took the path work/2026")
	}
	var pathErr *authz.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("the error %v is %T, want it to wrap *authz.PathError", err, err)
	}
	if pathErr.Path != "work/2026" {
		t.Errorf("the path error names %q, want work/2026", pathErr.Path)
	}
}

// TestMaxTopicLen checks that a topic of MaxTopicLen bytes passes, and that a
// longer 1 is an error.
func TestMaxTopicLen(t *testing.T) {
	// The spec's topic is 25 bytes, so a path of 998 bytes makes the dot and
	// the segment fit exactly.
	base := len("demo.note.created.group")
	fits := "/" + strings.Repeat("a", events.MaxTopicLen-base-1)
	event, err := events.New(local(), spec, "n-1", fits)
	if err != nil {
		t.Fatalf("New with a topic of %d bytes: %v", events.MaxTopicLen, err)
	}
	if len(event.Topic) != events.MaxTopicLen {
		t.Errorf("the topic is %d bytes, want %d", len(event.Topic), events.MaxTopicLen)
	}
	_, err = events.New(local(), spec, "n-1", fits+"/b")
	if err == nil {
		t.Fatal("New took a topic of 1026 bytes")
	}
	if !strings.Contains(err.Error(), "the limit is 1024") {
		t.Errorf("the error = %q, want it to name the limit", err)
	}
}

// TestValidate checks the checks of 1 event before its change commits, and
// before a transport publishes it.
func TestValidate(t *testing.T) {
	valid := func(t *testing.T) events.Event {
		t.Helper()
		event, err := events.NewAt(local(), spec, "n-1", "/work", fixed, fixedRandom())
		if err != nil {
			t.Fatalf("NewAt: %v", err)
		}
		return event
	}
	if err := valid(t).Validate(); err != nil {
		t.Errorf("Validate of a valid event: %v", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*events.Event)
		want   string
	}{
		{"no id", func(e *events.Event) { e.ID = "" }, "the id is required"},
		{"an id that is no UUID", func(e *events.Event) { e.ID = strings.ToUpper(e.ID) }, "is not a lower-case UUID"},
		{"no source", func(e *events.Event) { e.Source = "" }, "the source is required"},
		{"no type", func(e *events.Event) { e.Type = "" }, "the type is required"},
		{"no subject", func(e *events.Event) { e.Subject = "" }, "the subject (the object's id) is required"},
		{"no author", func(e *events.Event) { e.Author = "" }, "the author is required"},
		{"no time", func(e *events.Event) { e.Time = time.Time{} }, "the time is required"},
		{"no topic", func(e *events.Event) { e.Topic = "" }, "the topic is required"},
		{"a topic that is too long", func(e *events.Event) { e.Topic = "demo." + strings.Repeat("a", events.MaxTopicLen) }, "the topic is"},
		{"an envelope that is too long", func(e *events.Event) { e.Subject = strings.Repeat("a", events.MaxEnvelopeLen) }, "the envelope is"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := valid(t)
			tc.change(&event)
			err := event.Validate()
			if err == nil {
				t.Fatalf("Validate took the event with %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error = %q, want it to hold %q", err, tc.want)
			}
		})
	}
}

// TestNewAtEnvelopeLimit checks that an event whose envelope is longer than
// MaxEnvelopeLen fails NewAt, so that the outbox never holds a row that no
// transport takes.
func TestNewAtEnvelopeLimit(t *testing.T) {
	_, err := events.NewAt(local(), spec, strings.Repeat("a", 40<<10), "/work", fixed, fixedRandom())
	if err == nil {
		t.Fatal("NewAt took a subject of 40 KiB")
	}
	if !strings.Contains(err.Error(), "the envelope is") {
		t.Errorf("the error = %q, want it to name the envelope", err)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(events.MaxEnvelopeLen)) {
		t.Errorf("the error = %q, want it to name the limit %d", err, events.MaxEnvelopeLen)
	}
}

// TestEncodeSegment checks the encoding of each kind of byte.
func TestEncodeSegment(t *testing.T) {
	for _, tc := range []struct{ segment, token string }{
		{"work", "work"},
		{"q-1", "q-1"},
		{"2026_Q1", "2026%5FQ1"},
		{"a.b", "a%2Eb"},
		{"a b", "a%20b"},
		{"100%", "100%25"},
		{"Caf\u00e9", "Caf%C3%A9"},
		{"Cafe\u0301", "Cafe%CC%81"},
		{"+", "%2B"},
		{"", ""},
	} {
		if got := events.EncodeSegment(tc.segment); got != tc.token {
			t.Errorf("EncodeSegment(%q) = %q, want %q", tc.segment, got, tc.token)
		}
	}
}

// TestReadFilters checks the filters of 1 grant item over the app's event specs:
// 1 filter for each event of the item's capability, and no filter for another
// capability's event.
func TestReadFilters(t *testing.T) {
	specs := []events.Spec{spec, noSelector}
	for _, tc := range []struct {
		name string
		item authz.Item
		want []string
	}{
		{
			"the read capability, no selector value",
			authz.Item{Cap: "demo:read"},
			[]string{"demo.note.created.group", "demo.note.created.group.>"},
		},
		{
			"the read capability, the root path",
			authz.Item{Cap: "demo:read", Sel: map[string]string{"group": "/"}},
			[]string{"demo.note.created.group", "demo.note.created.group.>"},
		},
		{
			"the read capability, a path",
			authz.Item{Cap: "demo:read", Sel: map[string]string{"group": "/work"}},
			[]string{"demo.note.created.group.work", "demo.note.created.group.work.>"},
		},
		{
			"the read capability, 2 segments",
			authz.Item{Cap: "demo:read", Sel: map[string]string{"group": "/Work Notes/2026_Q1"}},
			[]string{"demo.note.created.group.Work%20Notes.2026%5FQ1", "demo.note.created.group.Work%20Notes.2026%5FQ1.>"},
		},
		{
			"another selector at the root",
			authz.Item{Cap: "demo:read", Sel: map[string]string{"folder": "/"}},
			[]string{"demo.note.created.group", "demo.note.created.group.>"},
		},
		{
			"the publish capability",
			authz.Item{Cap: "demo:publish"},
			[]string{"demo.note.published"},
		},
		{
			"another selector at a path",
			authz.Item{Cap: "demo:read", Sel: map[string]string{"folder": "/work"}},
			[]string{},
		},
		{
			"the publish capability with a selector of the app",
			authz.Item{Cap: "demo:publish", Sel: map[string]string{"group": "/work"}},
			[]string{},
		},
		{
			"a capability that no event has",
			authz.Item{Cap: "demo:write"},
			[]string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := events.ReadFilters(specs, tc.item)
			if err != nil {
				t.Fatalf("ReadFilters: %v", err)
			}
			if got == nil {
				t.Fatal("the filters are nil, want an empty list when no event matches")
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("the filters = %v, want %v", got, tc.want)
			}
			for _, filter := range got {
				if err := events.ValidFilter(filter); err != nil {
					t.Errorf("the filter %q is invalid: %v", filter, err)
				}
			}
		})
	}
}

// TestReadFiltersMatch checks the topics that 1 item's filters take: the events of
// the item's capability at its path and below it, and no other event.
func TestReadFiltersMatch(t *testing.T) {
	specs := []events.Spec{spec, noSelector}
	takes := func(t *testing.T, filters []string, topic string) bool {
		t.Helper()
		for _, filter := range filters {
			if events.Match(filter, topic) {
				return true
			}
		}
		return false
	}

	work, err := events.ReadFilters(specs, authz.Item{Cap: "demo:read", Sel: map[string]string{"group": "/work"}})
	if err != nil {
		t.Fatalf("ReadFilters: %v", err)
	}
	for _, tc := range []struct {
		topic string
		want  bool
	}{
		{"demo.note.created.group.work", true},
		{"demo.note.created.group.work.a", true},
		{"demo.note.created.group.workshop", false},
		{"demo.note.created.group", false},
		{"demo.note.published", false},
		{"notes.note.created.group.work", false},
	} {
		if got := takes(t, work, tc.topic); got != tc.want {
			t.Errorf("the filters of /work take %q = %v, want %v", tc.topic, got, tc.want)
		}
	}

	// The root item of the read capability never takes the publish event, which
	// another capability reads.
	root, err := events.ReadFilters(specs, authz.Item{Cap: "demo:read"})
	if err != nil {
		t.Fatalf("ReadFilters: %v", err)
	}
	if takes(t, root, noSelector.Topic) {
		t.Errorf("the filters of the read capability take %q, and demo:publish reads it", noSelector.Topic)
	}
	if !takes(t, root, spec.Topic) {
		t.Errorf("the filters of the read capability do not take %q", spec.Topic)
	}

	// The publish item takes exactly its own event, and nothing below it.
	publish, err := events.ReadFilters(specs, authz.Item{Cap: "demo:publish"})
	if err != nil {
		t.Fatalf("ReadFilters: %v", err)
	}
	if want := []string{"demo.note.published"}; !slices.Equal(publish, want) {
		t.Errorf("the filters of demo:publish = %v, want %v", publish, want)
	}
	if takes(t, publish, "demo.note.published.work") {
		t.Error("the filter of an event that carries no selector takes a path below its topic")
	}
}

// TestReadFiltersChecks checks that a spec of the item's capability and a path
// that is no path are errors, and that a spec of another capability is not read.
func TestReadFiltersChecks(t *testing.T) {
	badSpec := spec
	badSpec.Topic = "demo.note.created"
	if _, err := events.ReadFilters([]events.Spec{badSpec}, authz.Item{Cap: "demo:read"}); err == nil {
		t.Error("ReadFilters took a spec whose topic lacks the event's selector")
	}

	_, err := events.ReadFilters([]events.Spec{spec}, authz.Item{Cap: "demo:read", Sel: map[string]string{"group": "work"}})
	if err == nil {
		t.Fatal("ReadFilters took the path work")
	}
	var pathErr *authz.PathError
	if !errors.As(err, &pathErr) {
		t.Errorf("the error %v does not wrap *authz.PathError", err)
	}

	// A spec of another capability is not 1 that the item reads, so it is not
	// checked.
	badOther := badSpec
	badOther.ReadPermission = "demo:publish"
	got, err := events.ReadFilters([]events.Spec{badOther}, authz.Item{Cap: "demo:read"})
	if err != nil || len(got) != 0 {
		t.Errorf("ReadFilters with a bad spec of another capability = %v, %v, want no filter and no error", got, err)
	}
}

// TestMatch checks the 2 tokens of a filter: 1 exact token, "*" for exactly 1,
// and ">" for 1 or more at the end.
func TestMatch(t *testing.T) {
	for _, tc := range []struct {
		filter, topic string
		want          bool
	}{
		{"demo.>", "demo.note.created.group.work", true},
		{"demo.>", "demo", false},
		{"demo.>", "notes.note.created.group.work", false},
		{"demo.*.*.group.work", "demo.note.created.group.work", true},
		{"demo.*.*.group.work", "demo.note.created.group.work.2026", false},
		{"demo.*.*.group.work", "demo.note.group.work", false},
		{"demo.*.*.group.work.>", "demo.note.created.group.work.2026", true},
		{"demo.*.*.group.work.>", "demo.note.created.group.work", false},
		{"demo.note.created.group.work", "demo.note.created.group.work", true},
		{"demo.note.created.group.work", "demo.note.created.group.workshop", false},
		{"*", "demo", true},
		{"*", "demo.note", false},
		{">", "demo", true},
	} {
		if got := events.Match(tc.filter, tc.topic); got != tc.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tc.filter, tc.topic, got, tc.want)
		}
	}
}

// TestValidFilter checks the filters that are valid and the ones that are not.
func TestValidFilter(t *testing.T) {
	for _, filter := range []string{"demo.>", ">", "demo.*.*.group.work", "demo.*.*.group.work.>", "_INBOX.abc"} {
		if err := events.ValidFilter(filter); err != nil {
			t.Errorf("ValidFilter(%q) = %v, want nil", filter, err)
		}
	}
	for _, filter := range []string{"", "demo..work", ">.demo", "demo.>.", "demo note", "demo.", ".demo"} {
		if err := events.ValidFilter(filter); err == nil {
			t.Errorf("ValidFilter(%q) = nil, want an error", filter)
		}
	}
}

// TestMarshal checks the envelope's members, their order and the absent
// selector.
func TestMarshal(t *testing.T) {
	event, err := events.NewAt(local(), spec, "n-1", "/work", fixed, fixedRandom())
	if err != nil {
		t.Fatalf("NewAt: %v", err)
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	want := `{"specversion":"1.0","id":"018bcfe5-6800-7607-8809-0a0b0c0d0e0f",` +
		`"source":"/demo","type":"org.frostyard.demo.note.created","subject":"n-1",` +
		`"time":"2023-11-14T22:13:20Z","author":"local","selector":"/work"}`
	if string(data) != want {
		t.Errorf("the envelope =\n%s\nwant\n%s", data, want)
	}

	published, err := events.New(local(), noSelector, "n-1", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	data, err = json.Marshal(published)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if bytes.Contains(data, []byte("selector")) {
		t.Errorf("the envelope of an event without a selector holds 1:\n%s", data)
	}
	if bytes.Contains(data, []byte("data")) {
		t.Errorf("the envelope holds data:\n%s", data)
	}
}

// TestParseRoundTrip checks that 1 event reads back as it was written, with no
// topic, which the envelope does not carry.
func TestParseRoundTrip(t *testing.T) {
	event, err := events.NewAt(local(), spec, "n-1", "/work", fixed, fixedRandom())
	if err != nil {
		t.Fatalf("NewAt: %v", err)
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	parsed, err := events.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.ID != event.ID || parsed.Source != event.Source || parsed.Type != event.Type {
		t.Errorf("the event read back = %+v, want %+v", parsed, event)
	}
	if parsed.Subject != event.Subject || parsed.Author != event.Author || parsed.Selector != event.Selector {
		t.Errorf("the event read back = %+v, want %+v", parsed, event)
	}
	if !parsed.Time.Equal(event.Time) {
		t.Errorf("the time read back = %v, want %v", parsed.Time, event.Time)
	}
	if parsed.Topic != "" {
		t.Errorf("the topic read back = %q, want none", parsed.Topic)
	}

	// An envelope with no selector reads back with no path.
	published, err := events.New(local(), noSelector, "n-1", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	data, err = json.Marshal(published)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	parsed, err = events.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.Selector != "" {
		t.Errorf("the path read back = %q, want none", parsed.Selector)
	}
}

// TestParseFails checks that a broken envelope is an error: an unknown member,
// a member that is required and absent, another version, a bad time, and data
// after the object.
func TestParseFails(t *testing.T) {
	const good = `{"specversion":"1.0","id":"018bcfe5-6800-7607-8809-0a0b0c0d0e0f",` +
		`"source":"/demo","type":"org.frostyard.demo.note.created","subject":"n-1",` +
		`"time":"2023-11-14T22:13:20Z","author":"local"}`
	if _, err := events.Parse([]byte(good)); err != nil {
		t.Fatalf("Parse of a good envelope: %v", err)
	}
	for _, tc := range []struct {
		name string
		data string
		want string
	}{
		{"an unknown member", strings.Replace(good, `"author":"local"`, `"author":"local","extra":1`, 1), `unknown object member name "extra"`},
		{"a member in capitals", strings.Replace(good, `"author":"local"`, `"AUTHOR":"local"`, 1), `unknown object member name "AUTHOR"`},
		{"a duplicate member", strings.Replace(good, `"subject":"n-1"`, `"subject":"n-1","id":"018bcfe5-6800-7607-8809-0a0b0c0d0e0f"`, 1), `duplicate object member name "id"`},
		{"no id", strings.Replace(good, `"id":"018bcfe5-6800-7607-8809-0a0b0c0d0e0f",`, "", 1), `the member "id" is required`},
		{"no source", strings.Replace(good, `"source":"/demo",`, "", 1), `the member "source" is required`},
		{"no type", strings.Replace(good, `"type":"org.frostyard.demo.note.created",`, "", 1), `the member "type" is required`},
		{"no subject", strings.Replace(good, `"subject":"n-1",`, "", 1), `the member "subject" is required`},
		{"no time", strings.Replace(good, `"time":"2023-11-14T22:13:20Z",`, "", 1), `the member "time" is required`},
		{"no author", strings.Replace(good, `,"author":"local"`, "", 1), `the member "author" is required`},
		{"another version", strings.Replace(good, `"specversion":"1.0"`, `"specversion":"0.3"`, 1), "must be \"1.0\""},
		{"a bad time", strings.Replace(good, `"2023-11-14T22:13:20Z"`, `"yesterday"`, 1), "no RFC 3339 time"},
		{"data after the object", good + "{}", "after top-level value"},
		{"no object", "[]", "read the envelope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := events.Parse([]byte(tc.data))
			if err == nil {
				t.Fatalf("Parse took %s", tc.data)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error = %q, want it to hold %q", err, tc.want)
			}
		})
	}
}

// TestEventTime checks that a time with nanoseconds reads back to the
// nanosecond.
func TestEventTime(t *testing.T) {
	when := fixed.Add(123456789 * time.Nanosecond)
	event, err := events.NewAt(local(), spec, "n-1", "/work", when, fixedRandom())
	if err != nil {
		t.Fatalf("NewAt: %v", err)
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if !bytes.Contains(data, []byte(`"time":"2023-11-14T22:13:20.123456789Z"`)) {
		t.Errorf("the time is not in RFC 3339 with nanoseconds:\n%s", data)
	}
	parsed, err := events.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !parsed.Time.Equal(when) {
		t.Errorf("the time read back = %v, want %v", parsed.Time, when)
	}
}
