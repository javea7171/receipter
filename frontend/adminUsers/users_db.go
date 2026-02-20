package adminusers

import (
	"context"

	"github.com/uptrace/bun"

	"receipter/infrastructure/sqlite"
)

func LoadUsersPageData(ctx context.Context, db *sqlite.DB) ([]UserView, error) {
	users := make([]UserView, 0)
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw("SELECT id, username, role FROM users ORDER BY id ASC").Scan(ctx, &users)
	})
	return users, err
}
