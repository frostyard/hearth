# notes-lite: 1 small app on hearth

hearth generates an app's machinery from 1 Protobuf service: its manifest, its
permission checks, its MCP tools and its event publishers. This tutorial builds
such an app from an empty directory. The app keeps its notes in SQLite, it
publishes 2 events, and it serves 4 MCP tools.

This file is the app's source as well as its tutorial: each hand-written file
below is 1 code block marked with its path, and hearth's gate fails when a block
and its file differ. Take the blocks in the order they come, run the commands,
and the app is there.

## What you need

- Go 1.27 or later.
- buf.
- protoc-gen-go: `go install google.golang.org/protobuf/cmd/protoc-gen-go@latest`.
- golangci-lint, which `make verify` runs.

## Make the module

```sh
mkdir notes-lite
cd notes-lite
go mod init example.com/notes-lite
go get github.com/frostyard/hearth@latest modernc.org/sqlite@latest
```

The 4 plugins and the `hearth` command run with `go run` of the hearth version
that this module requires. Record them as tools of the module, so that `go.sum`
holds the packages they import:

```sh
go get -tool github.com/frostyard/hearth/cmd/protoc-gen-hearth-authz
go get -tool github.com/frostyard/hearth/cmd/protoc-gen-hearth-mcp
go get -tool github.com/frostyard/hearth/cmd/protoc-gen-hearth-events
go get -tool github.com/frostyard/hearth/cmd/protoc-gen-hearth-manifest
go get -tool github.com/frostyard/hearth/cmd/hearth
```

## Vendor hearth's options file

hearth publishes no Buf Schema Registry module yet, and its options file is 1
file of its Go module. Copy it, and keep the copy: buf reads it from
`third_party/`.

```sh
mkdir -p third_party/frostyard/hearth/v1
cp "$(go list -m -f '{{.Dir}}' github.com/frostyard/hearth)/proto/frostyard/hearth/v1/options.proto" third_party/frostyard/hearth/v1/
chmod u+w third_party/frostyard/hearth/v1/options.proto
```

## The app's Protobuf file

1 service and 2 events, with hearth's options on them. The `hearth_selector`
option names the field that carries the folder a note is filed in: the generated
permission check reads that field, and the generated event carries it.

<!-- file: proto/example/notes/v1/notes.proto -->

```proto
// The note service of the example app: 1 Protobuf service, and hearth's
// options on it, so that 1 buf run writes the app's manifest, its permission
// checks, its MCP tools and its event publishers.
syntax = "proto3";

package example.notes.v1;

import "frostyard/hearth/v1/options.proto";

option go_package = "example.com/notes-lite/gen/example/notes/v1;notesv1";

// Note is 1 note, filed in a folder.
message Note {
  // id identifies the note.
  string id = 1;
  // folder is the selector the note is filed under.
  string folder = 2 [(frostyard.hearth.v1.hearth_selector) = "folder"];
  // title is the note's title.
  string title = 3;
}

// CreateNoteRequest carries the note to write.
message CreateNoteRequest {
  // folder is the selector the note is filed under.
  string folder = 1 [(frostyard.hearth.v1.hearth_selector) = "folder"];
  // title is the note's title.
  string title = 2;
}

// CreateNoteResponse carries the note that was written.
message CreateNoteResponse {
  // note is the note that was written.
  Note note = 1;
}

// GetNoteRequest names the note to read.
message GetNoteRequest {
  // id identifies the note to read.
  string id = 1;
}

// GetNoteResponse carries the note that was read.
message GetNoteResponse {
  // note is the note that was read.
  Note note = 1;
}

// ListNotesRequest names the folder to list.
message ListNotesRequest {
  // folder is the selector the notes are filed under.
  string folder = 1 [(frostyard.hearth.v1.hearth_selector) = "folder"];
}

// ListNotesResponse carries the notes of 1 folder.
message ListNotesResponse {
  // notes are the notes of the folder.
  repeated Note notes = 1;
}

// DeleteNoteRequest names the note to delete.
message DeleteNoteRequest {
  // id identifies the note to delete.
  string id = 1;
}

// DeleteNoteResponse reports the deletion.
message DeleteNoteResponse {}

// NoteCreated is published when a note is created, in the transaction that
// writes it.
message NoteCreated {
  option (frostyard.hearth.v1.hearth_event) = {
    object: "note"
    verb: "created"
    selector: "folder"
    read_permission: "notelite:read"
  };
}

// NoteDeleted is published when a note is deleted, in the transaction that
// deletes it.
message NoteDeleted {
  option (frostyard.hearth.v1.hearth_event) = {
    object: "note"
    verb: "deleted"
    selector: "folder"
    read_permission: "notelite:read"
  };
}

// NoteService serves notes.
service NoteService {
  // CreateNote writes 1 note.
  rpc CreateNote(CreateNoteRequest) returns (CreateNoteResponse) {
    option (frostyard.hearth.v1.hearth_rpc) = {
      permission: "notelite:write"
      effect: EFFECT_WRITE
      expose: EXPOSE_UI
      expose: EXPOSE_AGENT
      agent_description: "Create 1 note with a title, in a folder."
      examples: "{\"folder\": \"/work\", \"title\": \"buy milk\"}"
    };
  }
  // GetNote reads 1 note.
  rpc GetNote(GetNoteRequest) returns (GetNoteResponse) {
    option (frostyard.hearth.v1.hearth_rpc) = {
      permission: "notelite:read"
      effect: EFFECT_READ
      expose: EXPOSE_UI
      expose: EXPOSE_AGENT
      agent_description: "Read 1 note by its id."
      examples: "{\"id\": \"n-1\"}"
    };
  }
  // ListNotes reads the notes of 1 folder.
  rpc ListNotes(ListNotesRequest) returns (ListNotesResponse) {
    option (frostyard.hearth.v1.hearth_rpc) = {
      permission: "notelite:read"
      effect: EFFECT_READ
      expose: EXPOSE_UI
      expose: EXPOSE_AGENT
      agent_description: "List the notes of 1 folder."
      examples: "{\"folder\": \"/work\"}"
    };
  }
  // DeleteNote deletes 1 note.
  rpc DeleteNote(DeleteNoteRequest) returns (DeleteNoteResponse) {
    option (frostyard.hearth.v1.hearth_rpc) = {
      permission: "notelite:write"
      effect: EFFECT_WRITE
      expose: EXPOSE_UI
      expose: EXPOSE_AGENT
      agent_description: "Delete 1 note by its id."
      examples: "{\"id\": \"n-1\"}"
    };
  }
}
```

## The buf workspace

The workspace has 2 modules: the app's own Protobuf files, and the copy of
hearth's options file. Both lint at STANDARD.

<!-- file: buf.yaml -->

```yaml
# The example app's buf workspace: its own Protobuf files, and the copy of
# hearth's options file that the README vendors. Each module lints at STANDARD.
version: v2
modules:
  - path: proto
    lint:
      use:
        - STANDARD
  - path: third_party
    lint:
      use:
        - STANDARD
```

## The code generation

`protoc-gen-go` writes the Go types, and hearth's 4 plugins write the manifest,
the permission checks, the MCP tools and the event publishers. Each plugin runs
with `go run`, so it is the hearth version of this module, and no plugin comes
from a registry.

<!-- file: buf.gen.yaml -->

```yaml
# The example app's code: its Go types from protoc-gen-go, and its manifest,
# permission checks, MCP tools and event publishers from hearth's 4 plugins.
# Each plugin runs with `go run` of the hearth version that go.mod requires, so
# no plugin comes from a registry.
version: v2
inputs:
  - directory: proto
plugins:
  - local: protoc-gen-go
    out: gen
    opt:
      - paths=source_relative
  - local:
      - go
      - run
      - github.com/frostyard/hearth/cmd/protoc-gen-hearth-authz
    out: gen
    opt:
      - paths=source_relative
      - app=notelite
      - version=0.1.0
  - local:
      - go
      - run
      - github.com/frostyard/hearth/cmd/protoc-gen-hearth-mcp
    out: gen
    opt:
      - paths=source_relative
      - app=notelite
      - version=0.1.0
  - local:
      - go
      - run
      - github.com/frostyard/hearth/cmd/protoc-gen-hearth-events
    out: gen
    opt:
      - paths=source_relative
      - app=notelite
      - version=0.1.0
  - local:
      - go
      - run
      - github.com/frostyard/hearth/cmd/protoc-gen-hearth-manifest
    out: gen
    opt:
      - app=notelite
      - version=0.1.0
```

## The gate

`make ci` runs the app's own gate: tidy, vet, format, lint, buf lint and format,
the tests, the generated-code check, the race detector, and the manifest diff
against `BASE`. `manifest-check` fails closed: it skips the comparison only when
`BASE` is empty, or when that commit is there and holds no manifest.

<!-- file: Makefile -->

```makefile
.PHONY: help generate gen-check verify race manifest-check ci

# Tracked or new Go files of the example, skipping files deleted but not yet
# staged.
GO_FILES = $(shell git ls-files --cached --others --exclude-standard '*.go' | while read -r f; do [ -e "$$f" ] && echo "$$f"; done)

help: ## List targets
	@grep -E '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | sed 's/:.*## /\t/' | expand -t 16

# The plugins of buf.gen.yaml run with go run of the hearth version that go.mod
# requires: go.sum pins them, and no plugin comes from a registry.
generate: ## Regenerate gen/ with the local plugins
	buf generate

gen-check: ## Regenerate into a temp dir, and fail when gen/ differs from a fresh run
	@tmp=$$(mktemp -d) || exit 1; \
	trap 'rm -rf "$$tmp"' EXIT; \
	buf generate -o "$$tmp" || exit 1; \
	if ! diff -r "$$tmp/gen" gen; then \
		echo "gen-check: gen/ differs from a fresh buf generate; run make generate and commit the result"; \
		exit 1; \
	fi; \
	echo "gen-check: gen/ matches a fresh run"

verify: ## Tidy, vet, format, lint, buf and the tests
	go mod tidy -diff
	go vet ./...
	test -z "$$(gofmt -l $(GO_FILES))"
	golangci-lint run
	buf lint
	buf format --diff --exit-code
	go test ./...

race: ## Run the tests under the race detector
	CGO_ENABLED=1 go test -race ./...

# The manifest is the app's contract: 1 change of it decides whether a consumer
# needs a new consent (ADR-0004). BASE is the commit to compare with, and the
# gate fails closed when it cannot look the manifest up (hearth's AGENTS.md,
# "A gate recipe fails closed"): only a base commit that is there, and a path
# that git ls-tree finds absent, skip the comparison.
manifest-check: ## Compare gen/manifest.json with the 1 at BASE (skipped when BASE is empty)
	@if [ -z "$(BASE)" ]; then \
		echo "manifest-check: BASE is not set, so the manifest is not compared"; \
	elif ! git rev-parse --verify -q "$(BASE)^{commit}" >/dev/null; then \
		echo "manifest-check: BASE $(BASE) is not a commit of this repository"; \
		exit 2; \
	elif ! listing=$$(git ls-tree "$(BASE)" -- gen/manifest.json); then \
		echo "manifest-check: git ls-tree $(BASE) -- gen/manifest.json failed"; \
		exit 2; \
	elif [ -z "$$listing" ]; then \
		echo "manifest-check: no manifest at $(BASE)"; \
	else \
		tmp=$$(mktemp) || exit 2; \
		trap 'rm -f "$$tmp"' EXIT; \
		if ! git show "$(BASE):./gen/manifest.json" > "$$tmp"; then \
			echo "manifest-check: git show $(BASE):./gen/manifest.json failed"; \
			exit 2; \
		fi; \
		go run github.com/frostyard/hearth/cmd/hearth manifest diff "$$tmp" gen/manifest.json; \
	fi

ci: verify gen-check race manifest-check ## verify plus the generated code, race and the manifest diff
```

## Generate the code

buf keeps a lock file for the modules a workspace depends on. This app declares
no such dependency, so write the empty lock once, and then generate:

```sh
printf '# Generated by buf. DO NOT EDIT.\nversion: v2\n' > buf.lock
buf dep update
make generate
```

`make generate` writes `gen/`: the Go types, the permission checks, the MCP
tools, the event publishers, and `gen/manifest.json`. That manifest is the app's
contract with lodge: its capabilities and its events are what lodge shows for its
consent.

## The app

`Open` opens the database, and it makes the notes table and the outbox's. The
generated API runs each RPC on an object id in 1 transaction, and the app's
`inTx` is that transaction: it commits, and then it wakes the relay.

`CreateNote` and `DeleteNote` write the note's event in the same transaction, so
the note and its event are there together, or neither is. `ListNotes` runs
outside a transaction, and it keeps only the notes that the caller's grant
covers. The 2 hooks read the stored note's folder inside the transaction, before
the RPC reads or deletes it.

<!-- file: notes.go -->

```go
// Package notes is the example app of hearth: 1 small note service whose API,
// permission checks, MCP tools, events and manifest hearth's plugins generate
// from proto/example/notes/v1/notes.proto.
//
// The app writes each note and its event in 1 SQLite transaction, so that the
// outbox holds the event exactly when the note is there. A relay publishes the
// outbox's events to a transport: the bus of this process, or lodge's broker.
package notes

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/frostyard/hearth/authz"
	"github.com/frostyard/hearth/events/outbox"

	notesv1 "example.com/notes-lite/gen/example/notes/v1"
)

// readCap and writeCap are the app's 2 capabilities.
const (
	readCap  = "notelite:read"
	writeCap = "notelite:write"
)

// schema is the app's table: 1 note, its folder and its title.
const schema = `CREATE TABLE IF NOT EXISTS notes (
	id     TEXT PRIMARY KEY,
	folder TEXT NOT NULL,
	title  TEXT NOT NULL
)`

// The app's statements.
const (
	insertSQL = `INSERT INTO notes (id, folder, title) VALUES (?, ?, ?)`
	selectSQL = `SELECT id, folder, title FROM notes ORDER BY id`
	readSQL   = `SELECT id, folder, title FROM notes WHERE id = ?`
	deleteSQL = `DELETE FROM notes WHERE id = ?`
)

// Open opens the app's database at path, and it makes the app's table and the
// outbox's.
//
// The database holds 1 connection: SQLite writes 1 at a time, so the app's
// transactions and the relay's deletes wait for each other, and never fail with
// a lock.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open the database %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("make the notes table: %w", err)
	}
	if err := outbox.Init(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// Service is the example app: its database, and the relay to wake after a
// change that wrote an event.
type Service struct {
	// DB is the app's database.
	DB *sql.DB
	// Notify wakes the relay. nil is no wake.
	Notify func()
}

// API returns the app's API with the generated permission check of every RPC.
func (s *Service) API() notesv1.NoteServiceAPI {
	return notesv1.NewNoteServiceAuthz[*sql.Tx](s, s.inTx)
}

// inTx runs fn in 1 transaction, commits it, and then wakes the relay, which
// publishes the events that the transaction wrote.
func (s *Service) inTx(ctx context.Context, fn func(ctx context.Context, tx *sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin the transaction: %w", err)
	}
	if err := fn(ctx, tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit the transaction: %w", err)
	}
	if s.Notify != nil {
		s.Notify()
	}
	return nil
}

// CreateNote writes 1 note, and its event, in 1 transaction. The app names the
// note: its id is a random string, not the request's.
func (s *Service) CreateNote(ctx context.Context, req *notesv1.CreateNoteRequest) (*notesv1.CreateNoteResponse, error) {
	folder := folderOf(req.GetFolder())
	var note *notesv1.Note
	err := s.inTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		id, err := newID()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, insertSQL, id, folder, req.GetTitle()); err != nil {
			return fmt.Errorf("write the note: %w", err)
		}
		// The event is in the change's transaction: it is published only if
		// the note commits.
		if err := notesv1.EmitNoteCreated(ctx, outbox.Tx(tx), id, folder); err != nil {
			return err
		}
		note = &notesv1.Note{Id: id, Folder: folder, Title: req.GetTitle()}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &notesv1.CreateNoteResponse{Note: note}, nil
}

// GetNote reads 1 note, in the transaction that the generated hook opened.
func (s *Service) GetNote(ctx context.Context, req *notesv1.GetNoteRequest) (*notesv1.GetNoteResponse, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	note, err := readNote(ctx, tx, req.GetId())
	if err != nil {
		return nil, err
	}
	return &notesv1.GetNoteResponse{Note: note}, nil
}

// ListNotes reads the notes of 1 folder, and it keeps only the notes that the
// caller's grant covers: the generated check reads the request's folder, and a
// note of another folder is left out.
//
// The RPC runs outside a transaction, because no part of it reads 1 stored note
// by its id.
func (s *Service) ListNotes(ctx context.Context, req *notesv1.ListNotesRequest) (*notesv1.ListNotesResponse, error) {
	grant, err := authz.GrantFrom(ctx)
	if err != nil {
		return nil, err
	}
	folder := folderOf(req.GetFolder())
	rows, err := s.DB.QueryContext(ctx, selectSQL)
	if err != nil {
		return nil, fmt.Errorf("read the notes: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	response := &notesv1.ListNotesResponse{}
	for rows.Next() {
		note := &notesv1.Note{}
		if err := rows.Scan(&note.Id, &note.Folder, &note.Title); err != nil {
			return nil, fmt.Errorf("read a note: %w", err)
		}
		if !under(note.GetFolder(), folder) || !grant.Allows(readCap, "folder", note.GetFolder()) {
			continue
		}
		response.Notes = append(response.Notes, note)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the notes: %w", err)
	}
	return response, nil
}

// DeleteNote deletes 1 note, and it writes the note's event, in the
// transaction that the generated hook opened.
func (s *Service) DeleteNote(ctx context.Context, req *notesv1.DeleteNoteRequest) (*notesv1.DeleteNoteResponse, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	note, err := readNote(ctx, tx, req.GetId())
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, deleteSQL, note.GetId()); err != nil {
		return nil, fmt.Errorf("delete the note %q: %w", note.GetId(), err)
	}
	if err := notesv1.EmitNoteDeleted(ctx, outbox.Tx(tx), note.GetId(), note.GetFolder()); err != nil {
		return nil, err
	}
	return &notesv1.DeleteNoteResponse{}, nil
}

// AuthorizeGetNote checks the folder that 1 note is stored in, before 1 call
// reads it.
func (s *Service) AuthorizeGetNote(ctx context.Context, tx *sql.Tx, grant authz.Grant, req *notesv1.GetNoteRequest) error {
	return storedFolder(ctx, tx, grant, readCap, req.GetId())
}

// AuthorizeDeleteNote checks the folder that 1 note is stored in, before 1 call
// deletes it.
func (s *Service) AuthorizeDeleteNote(ctx context.Context, tx *sql.Tx, grant authz.Grant, req *notesv1.DeleteNoteRequest) error {
	return storedFolder(ctx, tx, grant, writeCap, req.GetId())
}

// storedFolder checks 1 capability on the folder that 1 stored note is filed
// in.
func storedFolder(ctx context.Context, tx *sql.Tx, grant authz.Grant, cap, id string) error {
	note, err := readNote(ctx, tx, id)
	if err != nil {
		return err
	}
	return grant.Check(cap, authz.Sel{Selector: "folder", Path: note.GetFolder()})
}

// readNote reads 1 note of the transaction.
func readNote(ctx context.Context, tx *sql.Tx, id string) (*notesv1.Note, error) {
	note := &notesv1.Note{}
	err := tx.QueryRowContext(ctx, readSQL, id).Scan(&note.Id, &note.Folder, &note.Title)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("no note %q", id)
	}
	if err != nil {
		return nil, fmt.Errorf("read the note %q: %w", id, err)
	}
	return note, nil
}

// transaction returns the transaction of 1 RPC that runs on an object id.
func transaction(ctx context.Context) (*sql.Tx, error) {
	tx, ok := authz.TxFrom[*sql.Tx](ctx)
	if !ok {
		return nil, fmt.Errorf("the call runs outside the app's transaction")
	}
	return tx, nil
}

// folderOf returns the canonical form of 1 folder: the Unicode NFC form of the
// path that the permission check compares a grant with. An absent folder is the
// root, and a value that is no path is returned as it came, so that no grant
// covers it.
func folderOf(folder string) string {
	if folder == "" {
		return authz.Root
	}
	cleaned, err := authz.CleanPath(folder)
	if err != nil {
		return folder
	}
	return cleaned
}

// under reports whether 1 folder is the path, or below it.
func under(folder, path string) bool {
	return folder == path || strings.HasPrefix(folder, path+"/") || path == authz.Root
}

// newID returns a random id of 16 bytes, in hex.
func newID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("make the note's id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}
```

## The tests

The tests run the app as its own user, and as a caller whose grant covers 1
folder only: the refusal, the folder filter and the events are what they check.
They read the events from a bus of the same process, after the relay drained the
outbox.

<!-- file: notes_test.go -->

```go
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
```

## The command

`notelite mcp` serves the app's MCP tools over stdio. It opens the database,
starts the relay, and builds the server with the local user: a stdio server needs
no token, because the process is the user's own.

<!-- file: cmd/notelite/main.go -->

```go
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
```

## Build and test it

```sh
go mod tidy
make ci
```

`make ci` without `BASE` skips the manifest diff. To compare the manifest with
the 1 that a branch or a tag holds, name it:

```sh
make ci BASE=origin/main
```

## Run it

```sh
go build -o notelite ./cmd/notelite
./notelite -db notelite.db mcp
```

1 MCP client starts the app as 1 stdio server:

```json
{
  "mcpServers": {
    "notelite": {
      "command": "./notelite",
      "args": ["-db", "notelite.db", "mcp"]
    }
  }
}
```

The client sees the tools `create_note`, `get_note`, `list_notes` and
`delete_note`, each with the description and the example that the Protobuf file
gives it.

## Standalone, and with lodge

Without `-broker`, the app publishes its events to a bus of its own process, and
logs each 1 to stderr: that is the standalone mode, and it needs nothing else.

With `-broker`, it publishes them to the NATS broker that lodge embeds, over
lodge's Unix socket:

```sh
./notelite -db notelite.db -broker /run/lodge/nats.sock mcp
```

The broker keeps the app's events in 1 stream named `app_notelite`, and a
consumer that lodge admits reads them with the capability `notelite:read` and
the folder it holds. An app's own API on lodge's socket, with lodge's caller per
request, arrives with hearth's `Connect` route, which is a later phase: for now
this app serves its MCP tools as the local user, and it publishes its events.

## Inside hearth's repository

`examples/notes-lite` is this app, and hearth's `make ci` runs its gate, so a
change of hearth that breaks this tutorial fails hearth's own gate. Its `go.mod`
has 1 line that an app outside hearth does not have, so that it builds against
the checkout:

```
replace github.com/frostyard/hearth => ../..
```

The rest is what this file gives you, and a test of hearth's gate keeps each
block equal to its file.
