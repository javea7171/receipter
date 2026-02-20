package settings

import (
	"context"

	"github.com/uptrace/bun"

	"receipter/infrastructure/sqlite"
)

func SaveNotificationSettings(ctx context.Context, db *sqlite.DB, userID int64, emailEnabled bool) error {
	return db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, `
INSERT INTO user_settings (user_id, email_enabled, updated_at)
VALUES (?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(user_id) DO UPDATE SET
  email_enabled = excluded.email_enabled,
  updated_at = CURRENT_TIMESTAMP`, userID, emailEnabled)
		return err
	})
}
