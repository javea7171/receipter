package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/uptrace/bun"
)

// WithWriteTx runs fn in an explicit write transaction.
func (db *DB) WithWriteTx(ctx context.Context, fn func(ctx context.Context, tx bun.Tx) error) error {
	if db == nil || db.W == nil {
		return fmt.Errorf("write db is not initialized")
	}
	return db.W.RunInTx(ctx, &sql.TxOptions{}, func(ctx context.Context, tx bun.Tx) error {
		return fn(ctx, tx)
	})
}

// WithReadTx runs fn in an explicit read transaction.
func (db *DB) WithReadTx(ctx context.Context, fn func(ctx context.Context, tx bun.Tx) error) error {
	if db == nil || db.R == nil {
		return fmt.Errorf("read db is not initialized")
	}
	return db.R.RunInTx(ctx, &sql.TxOptions{ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		return fn(ctx, tx)
	})
}
