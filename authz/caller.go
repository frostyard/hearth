package authz

import (
	"context"
	"errors"
)

// ErrNoCaller is the error of a check that finds no caller: the permission
// runtime fails closed.
var ErrNoCaller = errors.New("permission denied: no caller")

// Caller is who calls an app's API: the principal's name, and the grant that
// bounds it.
type Caller struct {
	// Principal is the caller's name: lodge's principal, or "local" for the
	// app's own user. An empty Principal is not set; the adapters refuse it.
	Principal string
	// Grant is what the caller may do.
	Grant Grant
}

// LocalCaller returns the caller of the local user: the principal "local" with
// the local grant.
func LocalCaller() Caller {
	return Caller{Principal: "local", Grant: Local()}
}

// callerKey is the context key of a Caller.
type callerKey struct{}

// WithCaller returns a context that carries the caller.
func WithCaller(ctx context.Context, caller Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, caller)
}

// CallerFrom returns the caller of the context, and whether the context
// carries 1.
func CallerFrom(ctx context.Context) (Caller, bool) {
	caller, ok := ctx.Value(callerKey{}).(Caller)
	return caller, ok
}

// GrantFrom returns the grant of the context's caller. A context with no
// caller gives ErrNoCaller, so a missing caller refuses every check.
func GrantFrom(ctx context.Context) (Grant, error) {
	caller, ok := CallerFrom(ctx)
	if !ok {
		return Grant{}, ErrNoCaller
	}
	return caller.Grant, nil
}
