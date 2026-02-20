package cache

import (
	"strings"
	"sync"

	"receipter/models"
)

// UserCache caches users by username.
type UserCache struct {
	mu    sync.RWMutex
	users map[string]models.User
}

func NewUserCache() *UserCache {
	return &UserCache{users: make(map[string]models.User)}
}

func (c *UserCache) Add(username string, user models.User) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.users[strings.ToLower(username)] = user
}

func (c *UserCache) Get(username string) (models.User, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	u, ok := c.users[strings.ToLower(username)]
	return u, ok
}
