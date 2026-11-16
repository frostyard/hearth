package notes_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/frostyard/hearth/authz"
	"github.com/frostyard/hearth/events"
	"github.com/frostyard/hearth/events/inproc"
	"github.com/frostyard/hearth/events/outbox"

	"example.com/notes-lite"
	notesv1 "example.com/notes-lite/gen/example/notes/v1"
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
	filters, err := events.ReadFilters("notelite", "folder", "/work")
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
