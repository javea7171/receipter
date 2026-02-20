package cache

import (
	"sort"
	"sync"
)

// Resource maps role-based permissions to route/method metadata.
type Resource struct {
	UserResourceCode string
	Path             string
	Method           string
	Role             string
}

// RbacRolesCache stores role to resources map.
type RbacRolesCache struct {
	mu        sync.RWMutex
	resources map[string][]Resource
	allRoutes map[string]struct{}
}

func NewRbacRolesCache() *RbacRolesCache {
	return &RbacRolesCache{
		resources: make(map[string][]Resource),
		allRoutes: make(map[string]struct{}),
	}
}

func (c *RbacRolesCache) Add(role string, r Resource) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resources[role] = append(c.resources[role], r)
	c.allRoutes[r.UserResourceCode] = struct{}{}
}

func (c *RbacRolesCache) GetRolesAndResources(roles []string) []Resource {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Resource, 0)
	for _, role := range roles {
		out = append(out, c.resources[role]...)
	}
	return out
}

func (c *RbacRolesCache) GetAllRouteNames() map[string]int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]int, len(c.allRoutes))
	for route := range c.allRoutes {
		out[route] = 1
	}
	return out
}

func (c *RbacRolesCache) RouteNamesSorted() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	names := make([]string, 0, len(c.allRoutes))
	for name := range c.allRoutes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
