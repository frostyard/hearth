package notes_test

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

	"github.com/frostyard/hearth/authz"
	"github.com/frostyard/hearth/connectserver"
	"github.com/frostyard/hearth/events"
	"github.com/frostyard/hearth/events/inproc"
	"github.com/frostyard/hearth/events/outbox"
	"github.com/frostyard/hearth/lodge"

	"example.com/notes-lite"
	notesv1 "example.com/notes-lite/gen/example/notes/v1"
	"example.com/notes-lite/gen/example/notes/v1/notesv1connect"
)

// app returns the example app on a database in a temporary directory.
func app(t *testing.T) *notes.Service {
	t.Helper()
	db, err := notes.Open(t.Context(), filepath.Join(t.TempDir(), "notelite.db"))
	if err != nil {
		t.Fatalf("notes.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close the database: %v", err)
		}
	})
	return &notes.Service{DB: db}
}

// local returns the context of the app's own user, which every check passes.
func local() context.Context {
	return authz.WithCaller(context.Background(), authz.LocalCaller())
}

// caller returns the context of 1 principal with the grant of items.
func caller(t *testing.T, items ...authz.Item) context.Context {
	t.Helper()
	grant, err := authz.NewGrant(items...)
	if err != nil {
		t.Fatalf("authz.NewGrant: %v", err)
	}
	return authz.WithCaller(context.Background(), authz.Caller{Principal: "chani", Grant: grant})
}

// item returns 1 grant item on 1 folder.
func item(cap, folder string) authz.Item {
	return authz.Item{Cap: cap, Sel: map[string]string{"folder": folder}}
}

// subscribe subscribes 1 channel to the filters.
func subscribe(t *testing.T, bus *inproc.Bus, filters ...string) <-chan events.Event {
	t.Helper()
	got := make(chan events.Event, 8)
	for _, filter := range filters {
		cancel, err := bus.Subscribe(filter, func(e events.Event) { got <- e })
		if err != nil {
			t.Fatalf("Subscribe(%q): %v", filter, err)
		}
		t.Cleanup(cancel)
	}
	return got
}

// drain publishes the outbox's rows to the bus.
func drain(t *testing.T, app *notes.Service, bus *inproc.Bus) {
	t.Helper()
	if _, err := outbox.NewRelay(app.DB, bus, outbox.Options{}).Drain(t.Context()); err != nil {
		t.Fatalf("the relay's Drain: %v", err)
	}
}

// count returns how many rows 1 table of the app's database holds.
func count(t *testing.T, app *notes.Service, table string) int {
	t.Helper()
	var rows int
	if err := app.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&rows); err != nil {
		t.Fatalf("count the rows of %s: %v", table, err)
	}
	return rows
}

// TestCreateNote checks that 1 created note publishes its event with the note's
// id, its folder and the app's own user as the author.
func TestCreateNote(t *testing.T) {
	example := app(t)
	bus := inproc.New()
	got := subscribe(t, bus, "notelite.>")

	response, err := example.API().CreateNote(local(), &notesv1.CreateNoteRequest{Folder: "/work", Title: "buy milk"})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	drain(t, example, bus)

	event := <-got
	if event.Type != "org.frostyard.notelite.note.created" {
		t.Errorf("the event's type = %q, want org.frostyard.notelite.note.created", event.Type)
	}
	if event.Subject != response.GetNote().GetId() {
		t.Errorf("the event's subject = %q, want the note's id %q", event.Subject, response.GetNote().GetId())
	}
	if event.Selector != "/work" {
		t.Errorf("the event's selector = %q, want /work", event.Selector)
	}
	if event.Author != "local" {
		t.Errorf("the event's author = %q, want local", event.Author)
	}
	if want := "notelite.note.created.folder.work"; event.Topic != want {
		t.Errorf("the event's topic = %q, want %q", event.Topic, want)
	}
	if got := count(t, example, "hearth_outbox"); got != 0 {
		t.Errorf("the outbox holds %d rows after the relay, want none", got)
	}
}

// TestRefusedCreateNote checks that a grant on /work refuses 1 note in /home,
// and that the refusal writes neither the note nor its event.
func TestRefusedCreateNote(t *testing.T) {
	example := app(t)
	ctx := caller(t, item("notelite:read", "/work"), item("notelite:write", "/work"))
	_, err := example.API().CreateNote(ctx, &notesv1.CreateNoteRequest{Folder: "/home", Title: "buy milk"})
	if err == nil {
		t.Fatal("CreateNote in /home passed, and the grant holds /work")
	}
	if want := "permission denied: needs notelite:write on folder=/home"; err.Error() != want {
		t.Errorf("the refusal = %q, want %q", err, want)
	}
	if got := count(t, example, "notes"); got != 0 {
		t.Errorf("the notes table holds %d rows, want none", got)
	}
	if got := count(t, example, "hearth_outbox"); got != 0 {
		t.Errorf("the outbox holds %d rows, want none", got)
	}
}

// TestListNotes checks that the list holds only the notes that the caller's
// grant covers, and that the app's own user sees each folder.
func TestListNotes(t *testing.T) {
	example := app(t)
	api := example.API()
	for _, folder := range []string{"/work", "/work/2026", "/home"} {
		if _, err := api.CreateNote(local(), &notesv1.CreateNoteRequest{Folder: folder, Title: "a note of " + folder}); err != nil {
			t.Fatalf("CreateNote in %s: %v", folder, err)
		}
	}

	ctx := caller(t, item("notelite:read", "/work"))
	response, err := api.ListNotes(ctx, &notesv1.ListNotesRequest{Folder: "/work"})
	if err != nil {
		t.Fatalf("ListNotes of /work: %v", err)
	}
	if len(response.GetNotes()) != 2 {
		t.Fatalf("ListNotes of /work = %d notes, want the 2 of /work and /work/2026", len(response.GetNotes()))
	}
	for _, note := range response.GetNotes() {
		if note.GetFolder() != "/work" && note.GetFolder() != "/work/2026" {
			t.Errorf("ListNotes of /work holds the note of %s", note.GetFolder())
		}
	}

	all, err := api.ListNotes(local(), &notesv1.ListNotesRequest{Folder: "/"})
	if err != nil {
		t.Fatalf("ListNotes of /: %v", err)
	}
	if len(all.GetNotes()) != 3 {
		t.Errorf("ListNotes of / = %d notes, want 3", len(all.GetNotes()))
	}

	if _, err := api.ListNotes(ctx, &notesv1.ListNotesRequest{Folder: "/home"}); err == nil {
		t.Error("ListNotes of /home passed, and the grant holds /work")
	}
}

// TestGetNoteHook checks that the hook of an object-id RPC refuses 1 note that
// the caller's grant does not cover.
func TestGetNoteHook(t *testing.T) {
	example := app(t)
	api := example.API()
	created, err := api.CreateNote(local(), &notesv1.CreateNoteRequest{Folder: "/home", Title: "buy milk"})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	ctx := caller(t, item("notelite:read", "/work"))
	_, err = api.GetNote(ctx, &notesv1.GetNoteRequest{Id: created.GetNote().GetId()})
	if err == nil {
		t.Fatal("GetNote of a note in /home passed, and the grant holds /work")
	}
	if want := "permission denied: needs notelite:read on folder=/home"; err.Error() != want {
		t.Errorf("the refusal = %q, want %q", err, want)
	}
}

// TestDeleteNote checks that a deleted note publishes its event, with the id
// and the folder that the note had.
func TestDeleteNote(t *testing.T) {
	example := app(t)
	bus := inproc.New()
	got := subscribe(t, bus, "notelite.>")
	api := example.API()
	created, err := api.CreateNote(local(), &notesv1.CreateNoteRequest{Folder: "/work", Title: "buy milk"})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if _, err := api.DeleteNote(local(), &notesv1.DeleteNoteRequest{Id: created.GetNote().GetId()}); err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}
	drain(t, example, bus)

	createdEvent := <-got
	deletedEvent := <-got
	if createdEvent.Type != "org.frostyard.notelite.note.created" {
		t.Fatalf("the first event's type = %q, want org.frostyard.notelite.note.created", createdEvent.Type)
	}
	if deletedEvent.Type != "org.frostyard.notelite.note.deleted" {
		t.Errorf("the second event's type = %q, want org.frostyard.notelite.note.deleted", deletedEvent.Type)
	}
	if deletedEvent.Subject != created.GetNote().GetId() {
		t.Errorf("the deleted note's event has the subject %q, want %q", deletedEvent.Subject, created.GetNote().GetId())
	}
	if deletedEvent.Selector != "/work" {
		t.Errorf("the deleted note's event has the selector %q, want /work", deletedEvent.Selector)
	}
	if got := count(t, example, "notes"); got != 0 {
		t.Errorf("the notes table holds %d rows after the deletion, want none", got)
	}
}

// TestReadFilters checks that a consumer of 1 folder reads the events of that
// folder, and no other folder's.
func TestReadFilters(t *testing.T) {
	example := app(t)
	bus := inproc.New()
	// The filters of a grant item on /work cover the app's created and deleted
	// notes there, and no other folder's.
	filters, err := events.ReadFilters(
		[]events.Spec{notesv1.EventNoteCreated, notesv1.EventNoteDeleted},
		authz.Item{Cap: "notelite:read", Sel: map[string]string{"folder": "/work"}},
	)
	if err != nil {
		t.Fatalf("events.ReadFilters: %v", err)
	}
	got := subscribe(t, bus, filters...)
	api := example.API()
	for _, folder := range []string{"/work", "/home"} {
		if _, err := api.CreateNote(local(), &notesv1.CreateNoteRequest{Folder: folder, Title: "a note"}); err != nil {
			t.Fatalf("CreateNote in %s: %v", folder, err)
		}
	}
	drain(t, example, bus)

	event := <-got
	if event.Selector != "/work" {
		t.Errorf("the consumer got the event of %q, want /work", event.Selector)
	}
	if len(got) != 0 {
		t.Errorf("the consumer got %d events more, want none", len(got))
	}
}

// setHeaders is a client interceptor that sets the headers of each call.
type setHeaders map[string]string

// WrapUnary implements connect.Interceptor.
func (h setHeaders) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		for name, value := range h {
			req.Header().Set(name, value)
		}
		return next(ctx, req)
	}
}

// WrapStreamingClient implements connect.Interceptor.
func (h setHeaders) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler implements connect.Interceptor.
func (h setHeaders) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

// lodgeCall returns the headers of a call that lodge forwards for 1 principal
// with 1 grant.
func lodgeCall(t *testing.T, grant string) setHeaders {
	t.Helper()
	return setHeaders{
		connectserver.CallerHeader: connectserver.PrincipalCaller,
		lodge.PrincipalHeader:      "chani",
		lodge.GrantHeader:          base64.RawURLEncoding.EncodeToString([]byte(grant)),
	}
}

// socketPath returns a path for a Unix socket in a directory of its own,
// because a socket's path is 108 bytes at most and a test's directory can be
// longer.
func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "notelite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Logf("remove %s: %v", dir, err)
		}
	})
	return filepath.Join(dir, "socket")
}

// serveSocket serves 1 handler over Connect on 1 Unix socket that admits the
// connections of 1 user, and it returns the socket's path.
func serveSocket(t *testing.T, uid int, handler http.Handler) string {
	t.Helper()
	path := socketPath(t)
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() {
		served <- connectserver.Serve(ctx, lodge.Listener(listener, uid), handler)
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return path
}

// TestConnectSockets checks the app's 2 sockets: the app's own user writes and
// lists 1 note on the private socket, and a caller whose grant reads /home is
// refused 1 list of /work on lodge's socket, with the permission runtime's own
// text.
func TestConnectSockets(t *testing.T) {
	example := app(t)
	path, handler := notesv1connect.NewNoteServiceHandler(example.API(), connectserver.HandlerOptions()...)
	private := serveSocket(t, os.Getuid(), connectserver.Private(path, handler))
	onLodge := serveSocket(t, os.Getuid(), connectserver.Lodge(path, handler))

	local := notesv1connect.NewNoteServiceClient(connectserver.UnixClient(private), "http://localhost")
	created, err := local.CreateNote(t.Context(), &notesv1.CreateNoteRequest{Folder: "/work", Title: "buy milk"})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	listing, err := local.ListNotes(t.Context(), &notesv1.ListNotesRequest{Folder: "/work"})
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(listing.GetNotes()) != 1 || listing.GetNotes()[0].GetId() != created.GetNote().GetId() {
		t.Fatalf("ListNotes of /work = %v, want the 1 note that the app's own user wrote", listing.GetNotes())
	}

	grant := `[{"cap":"notelite:read","sel":{"folder":"/home"}}]`
	caller := notesv1connect.NewNoteServiceClient(connectserver.UnixClient(onLodge), "http://localhost/connect",
		connect.WithInterceptors(lodgeCall(t, grant)))
	_, err = caller.ListNotes(t.Context(), &notesv1.ListNotesRequest{Folder: "/work"})
	if err == nil {
		t.Fatal("ListNotes of /work passed, and the grant reads /home")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Errorf("the refusal's code = %v, want %v", got, connect.CodePermissionDenied)
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("the refusal = %T, want a *connect.Error", err)
	}
	if want := "permission denied: needs notelite:read on folder=/work"; connectErr.Message() != want {
		t.Errorf("the refusal = %q, want %q", connectErr.Message(), want)
	}
}
