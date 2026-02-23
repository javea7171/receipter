package context

import (
	"context"

	"receipter/models"
)

type sessionKey struct{}

func NewContextWithSession(ctx context.Context, session models.Session) context.Context {
	return context.WithValue(ctx, sessionKey{}, session)
}

func GetSessionFromContext(ctx context.Context) (models.Session, bool) {
	s, ok := ctx.Value(sessionKey{}).(models.Session)
	return s, ok
}

func ActiveProjectIDFromContext(ctx context.Context) (int64, bool) {
	s, ok := GetSessionFromContext(ctx)
	if !ok || s.ActiveProjectID == nil || *s.ActiveProjectID <= 0 {
		return 0, false
	}
	return *s.ActiveProjectID, true
}
