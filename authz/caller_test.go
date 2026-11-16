package authz_test

import (
	"context"
	"errors"
	"testing"

	"github.com/frostyard/hearth/authz"
)

// TestCaller checks the caller in the context, and that a missing caller fails
// closed.
func TestCaller(t *testing.T) {
	ctx := t.Context()
	if _, ok := authz.CallerFrom(ctx); ok {
		t.Error("an empty context carries a caller")
	}
	if _, err := authz.GrantFrom(ctx); !errors.Is(err, authz.ErrNoCaller) {
		t.Errorf("GrantFrom on an empty context = %v, want authz.ErrNoCaller", err)
	}

	grant := mustGrant(t, authz.Item{Cap: "demo:read", Sel: map[string]string{"group": "/work"}})
	caller := authz.Caller{Principal: "chani", Grant: grant}
	ctx = authz.WithCaller(ctx, caller)
	got, ok := authz.CallerFrom(ctx)
	if !ok {
		t.Fatal("CallerFrom found no caller")
	}
	if got.Principal != "chani" {
		t.Errorf("the principal is %q, want chani", got.Principal)
	}
	fromContext, err := authz.GrantFrom(ctx)
	if err != nil {
		t.Fatalf("GrantFrom: %v", err)
	}
	if err := fromContext.Check("demo:read", authz.Sel{Selector: "group", Path: "/work"}); err != nil {
		t.Errorf("the caller's grant: %v", err)
	}

	local := authz.LocalCaller()
	if local.Principal != "local" {
		t.Errorf("the local principal is %q, want local", local.Principal)
	}
	if err := local.Grant.Check("demo:write", authz.Sel{Selector: "group", Path: "/private"}); err != nil {
		t.Errorf("the local caller's grant: %v", err)
	}
}

// TestTx checks the transaction value in the context.
func TestTx(t *testing.T) {
	type store struct{ notes []string }

	ctx := t.Context()
	if _, ok := authz.TxFrom[store](ctx); ok {
		t.Error("an empty context carries a transaction")
	}
	ctx = authz.WithTx(ctx, store{notes: []string{"n-1"}})
	tx, ok := authz.TxFrom[store](ctx)
	if !ok {
		t.Fatal("TxFrom found no transaction")
	}
	if len(tx.notes) != 1 || tx.notes[0] != "n-1" {
		t.Errorf("the transaction is %+v", tx)
	}
	// A transaction of another type is a different value.
	if _, ok := authz.TxFrom[int](ctx); ok {
		t.Error("TxFrom[int] found the store's transaction")
	}

	var ran bool
	inTx := authz.TxFunc[store](func(ctx context.Context, fn func(ctx context.Context, tx store) error) error {
		ran = true
		return fn(authz.WithTx(ctx, store{notes: []string{"n-2"}}), store{notes: []string{"n-2"}})
	})
	if err := inTx(ctx, func(ctx context.Context, tx store) error {
		if tx.notes[0] != "n-2" {
			t.Errorf("the transaction is %+v", tx)
		}
		inner, ok := authz.TxFrom[store](ctx)
		if !ok || inner.notes[0] != "n-2" {
			t.Errorf("the context's transaction is %+v, %v", inner, ok)
		}
		return nil
	}); err != nil {
		t.Fatalf("the transaction function: %v", err)
	}
	if !ran {
		t.Error("the transaction function did not run")
	}
}
