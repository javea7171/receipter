package audit

import (
	"context"
	"encoding/json"

	"github.com/uptrace/bun"

	"receipter/models"
)

// Service writes audit records inside the caller transaction.
type Service struct{}

func NewService() *Service {
	return &Service{}
}

func (s *Service) Write(ctx context.Context, tx bun.Tx, userID int64, action, entityType, entityID string, before, after any) error {
	beforeJSON, err := marshal(before)
	if err != nil {
		return err
	}
	afterJSON, err := marshal(after)
	if err != nil {
		return err
	}
	log := &models.AuditLog{
		UserID:     userID,
		Action:     action,
		EntityType: entityType,
		EntityID:   entityID,
		BeforeJSON: beforeJSON,
		AfterJSON:  afterJSON,
	}
	_, err = tx.NewInsert().Model(log).Exec(ctx)
	return err
}

func marshal(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
