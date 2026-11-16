package authz

import "context"

// TxFunc runs 1 app transaction: it calls fn with the context and the
// transaction's value, and returns fn's error, committed or rolled back.
//
// An app's object-id hooks and their RPC run inside 1 transaction, so that the
// object a hook checked is the object the RPC changes.
type TxFunc[Tx any] func(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error

// txKey is the context key of a transaction value. It is 1 type for each
// transaction type, so that 2 transaction types cannot collide.
type txKey[Tx any] struct{}

// WithTx returns a context that carries the transaction value. The generated
// permission check calls it before it runs an object-id RPC.
func WithTx[Tx any](ctx context.Context, tx Tx) context.Context {
	return context.WithValue(ctx, txKey[Tx]{}, tx)
}

// TxFrom returns the transaction value of the context, and whether the context
// carries 1.
func TxFrom[Tx any](ctx context.Context) (Tx, bool) {
	tx, ok := ctx.Value(txKey[Tx]{}).(Tx)
	return tx, ok
}
