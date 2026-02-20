package cache

import (
	"sync"

	"receipter/models"
)

// UserSessionCache stores sessions by token.
type UserSessionCache struct {
	mu       sync.RWMutex
	sessions map[string]models.Session
}

func NewUserSessionCache() *UserSessionCache {
	return &UserSessionCache{sessions: make(map[string]models.Session)}
}

func (c *UserSessionCache) AddSession(s models.Session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessions[s.ID] = s
}

func (c *UserSessionCache) FindSessionBySessionToken(token string) (models.Session, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.sessions[token]
	return s, ok
}

func (c *UserSessionCache) DeleteSessionBySessionToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.sessions, token)
}
