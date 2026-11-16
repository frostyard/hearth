//go:build hearth_missinghook

// Command missinghook is the build-failure case of the generated permission
// check: its type has every RPC of the demo API, and no AuthorizeGetNote hook,
// so demov1.NewNoteServiceAuthz refuses it.
//
// internal/e2e's test builds it with the tag hearth_missinghook and requires the
// build to fail: a type that is no NoteServiceAuthorizer cannot be an
// implementation of the API, so an app that forgets 1 hook does not build.
package main

import (
	"context"

	"github.com/frostyard/hearth/authz"
	demov1 "github.com/frostyard/hearth/internal/e2e/gen/frostyard/demo/v1"
)

// missing is the demo app with every hook but GetNote's.
type missing struct{}

// GetNote reads 1 note, and it checks no permission.
func (missing) GetNote(context.Context, *demov1.GetNoteRequest) (*demov1.GetNoteResponse, error) {
	return nil, nil
}

// ListNotes reads the notes of 1 group.
func (missing) ListNotes(context.Context, *demov1.ListNotesRequest) (*demov1.ListNotesResponse, error) {
	return nil, nil
}

// CreateNote writes 1 note.
func (missing) CreateNote(context.Context, *demov1.CreateNoteRequest) (*demov1.CreateNoteResponse, error) {
	return nil, nil
}

// PublishNote publishes 1 note.
func (missing) PublishNote(context.Context, *demov1.PublishNoteRequest) (*demov1.PublishNoteResponse, error) {
	return nil, nil
}

// MoveNote moves 1 note.
func (missing) MoveNote(context.Context, *demov1.MoveNoteRequest) (*demov1.MoveNoteResponse, error) {
	return nil, nil
}

// AuthorizeCreateNote checks the group the new note goes to.
func (missing) AuthorizeCreateNote(context.Context, int, authz.Grant, *demov1.CreateNoteRequest) error {
	return nil
}

// AuthorizePublishNote checks the group the note is stored in.
func (missing) AuthorizePublishNote(context.Context, int, authz.Grant, *demov1.PublishNoteRequest) error {
	return nil
}

// AuthorizeMoveNote checks the group the note leaves.
func (missing) AuthorizeMoveNote(context.Context, int, authz.Grant, *demov1.MoveNoteRequest) error {
	return nil
}

// tx is the fixture's transaction: it runs fn at once.
func tx(ctx context.Context, fn func(ctx context.Context, tx int) error) error {
	return fn(ctx, 0)
}

func main() {
	// The hook AuthorizeGetNote is missing, so this line does not build.
	_ = demov1.NewNoteServiceAuthz[int](missing{}, tx)
}
