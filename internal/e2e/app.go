// Package e2e is hearth's end-to-end demo app: an in-memory note service on the
// generated permission checks and MCP tools of
// testdata/e2e/proto/frostyard/demo/v1/demo.proto.
//
// The package is documentation as much as test: it shows what an app writes
// itself, and what the plugins write for it. The API comes from the generated
// interface, the permission check of each RPC from protoc-gen-hearth-authz, the
// hooks that resolve a stored note's group from the app, the tools from
// protoc-gen-hearth-mcp, and the list filter from the app.
package e2e

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"google.golang.org/protobuf/proto"

	"github.com/frostyard/hearth/authz"
	demov1 "github.com/frostyard/hearth/internal/e2e/gen/frostyard/demo/v1"
)

// noteRead is the capability that reads a note.
const noteRead = "demo:read"

// store is the app's transaction: every note, by id, and the last id the app
// gave out.
type store struct {
	// notes holds each note, by its id.
	notes map[string]*demov1.Note
	// next is the last number of a note that the app named.
	next int
}

// NoteService is the demo app: an in-memory note service.
//
// The app holds its own lock, and its transaction is the lock: 1 RPC that runs
// on an object id holds the lock from its hook to its end, so the note the hook
// checked is the note the RPC changes.
type NoteService struct {
	mutex sync.Mutex
	store *store
}

// New returns a demo app with no note.
func New() *NoteService {
	return &NoteService{store: &store{notes: map[string]*demov1.Note{}}}
}

// Wired returns the API with the generated permission check of every RPC.
//
// An app serves this API on its own sockets: the stdio MCP server, its private
// socket and its lodge socket all reach it, each with its own caller.
func (s *NoteService) Wired() demov1.NoteServiceAPI {
	return demov1.NewNoteServiceAuthz[*store](s, s.tx)
}

// SeedNote stores 1 note, as the app's startup does, with no permission check.
func (s *NoteService) SeedNote(id, group string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.store.notes[id] = &demov1.Note{Id: id, Group: groupOf(group)}
}

// tx runs fn inside the app's transaction.
func (s *NoteService) tx(ctx context.Context, fn func(ctx context.Context, tx *store) error) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return fn(ctx, s.store)
}

// GetNote reads 1 note.
func (s *NoteService) GetNote(ctx context.Context, req *demov1.GetNoteRequest) (*demov1.GetNoteResponse, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	note, ok := tx.notes[req.GetId()]
	if !ok {
		return nil, fmt.Errorf("no note %q", req.GetId())
	}
	return &demov1.GetNoteResponse{Note: clone(note)}, nil
}

// ListNotes reads the notes of 1 group, and filters each of them with the
// caller's grant: the generated check reads the request's group, and a note of
// another group is left out.
func (s *NoteService) ListNotes(ctx context.Context, req *demov1.ListNotesRequest) (*demov1.ListNotesResponse, error) {
	grant, err := authz.GrantFrom(ctx)
	if err != nil {
		return nil, err
	}
	group := groupOf(req.GetGroup())
	s.mutex.Lock()
	defer s.mutex.Unlock()
	ids := make([]string, 0, len(s.store.notes))
	for id := range s.store.notes {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	response := &demov1.ListNotesResponse{}
	for _, id := range ids {
		note := s.store.notes[id]
		if !under(note.GetGroup(), group) || !grant.Allows(noteRead, "group", note.GetGroup()) {
			continue
		}
		response.Notes = append(response.Notes, clone(note))
	}
	return response, nil
}

// CreateNote writes 1 note.
func (s *NoteService) CreateNote(ctx context.Context, req *demov1.CreateNoteRequest) (*demov1.CreateNoteResponse, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	note := clone(req.GetNote())
	if note.GetId() == "" {
		// The app names a note that carries no id, with a free id.
		for {
			tx.next++
			note.Id = fmt.Sprintf("n-%d", tx.next)
			if _, taken := tx.notes[note.GetId()]; !taken {
				break
			}
		}
	}
	note.Group = groupOf(note.GetGroup())
	if _, ok := tx.notes[note.GetId()]; ok {
		return nil, fmt.Errorf("the note %q is there", note.GetId())
	}
	tx.notes[note.GetId()] = note
	return &demov1.CreateNoteResponse{Note: clone(note)}, nil
}

// PublishNote reports where 1 note went.
func (s *NoteService) PublishNote(ctx context.Context, req *demov1.PublishNoteRequest) (*demov1.PublishNoteResponse, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	note, ok := tx.notes[req.GetId()]
	if !ok {
		return nil, fmt.Errorf("no note %q", req.GetId())
	}
	return &demov1.PublishNoteResponse{Url: "https://example.com/notes/" + note.GetId()}, nil
}

// MoveNote moves 1 note to another group.
func (s *NoteService) MoveNote(ctx context.Context, req *demov1.MoveNoteRequest) (*demov1.MoveNoteResponse, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	note, ok := tx.notes[req.GetId()]
	if !ok {
		return nil, fmt.Errorf("no note %q", req.GetId())
	}
	note.Group = groupOf(req.GetToGroup())
	return &demov1.MoveNoteResponse{Note: clone(note)}, nil
}

// AuthorizeGetNote checks the group the note is stored in. The generated check
// has already read the capability from the grant: a hook resolves the object.
func (s *NoteService) AuthorizeGetNote(_ context.Context, tx *store, grant authz.Grant, req *demov1.GetNoteRequest) error {
	return storedGroup(tx, grant, noteRead, req.GetId())
}

// AuthorizeCreateNote checks the group the new note goes to. A new note has no
// stored group, so the request carries the object's value.
func (s *NoteService) AuthorizeCreateNote(_ context.Context, _ *store, grant authz.Grant, req *demov1.CreateNoteRequest) error {
	return grant.Check("demo:write", authz.Sel{Selector: "group", Path: req.GetNote().GetGroup()})
}

// AuthorizePublishNote checks the group the note is stored in.
func (s *NoteService) AuthorizePublishNote(_ context.Context, tx *store, grant authz.Grant, req *demov1.PublishNoteRequest) error {
	return storedGroup(tx, grant, "demo:publish", req.GetId())
}

// AuthorizeMoveNote checks the group the note leaves. The destination is the
// request's selector value, and the generated check reads it.
func (s *NoteService) AuthorizeMoveNote(_ context.Context, tx *store, grant authz.Grant, req *demov1.MoveNoteRequest) error {
	note, ok := tx.notes[req.GetId()]
	if !ok {
		return fmt.Errorf("no note %q", req.GetId())
	}
	return grant.Check("demo:write", authz.Sel{Selector: "group", Path: note.GetGroup()})
}

// storedGroup checks 1 capability on the group that 1 stored note is filed in.
func storedGroup(tx *store, grant authz.Grant, cap, id string) error {
	note, ok := tx.notes[id]
	if !ok {
		return fmt.Errorf("no note %q", id)
	}
	return grant.Check(cap, authz.Sel{Selector: "group", Path: note.GetGroup()})
}

// transaction returns the app's transaction from the context of 1 object-id
// RPC.
func transaction(ctx context.Context) (*store, error) {
	tx, ok := authz.TxFrom[*store](ctx)
	if !ok {
		return nil, errors.New("the call runs outside the app's transaction")
	}
	return tx, nil
}

// groupOf returns the canonical form of 1 group: the Unicode NFC form of the
// path that the permission check compares a grant with, so that 2 spellings of 1
// group are 1 group. An absent value is the root, and a value that is no path is
// returned as it came, so that it matches no other group.
func groupOf(group string) string {
	if group == "" {
		return authz.Root
	}
	cleaned, err := authz.CleanPath(group)
	if err != nil {
		return group
	}
	return cleaned
}

// under reports whether 1 group is the path, or below it.
func under(group, path string) bool {
	return group == path || strings.HasPrefix(group, path+"/") || path == authz.Root
}

// clone returns a copy of 1 note, so that a caller cannot change the store.
func clone(note *demov1.Note) *demov1.Note {
	if note == nil {
		return &demov1.Note{}
	}
	copied, ok := proto.Clone(note).(*demov1.Note)
	if !ok {
		return &demov1.Note{}
	}
	return copied
}
