package rbac

import (
	"strings"

	"receipter/infrastructure/cache"
)

const (
	RoleAdmin   = "admin"
	RoleScanner = "scanner"
)

// Rbac stores route resources in cache.
type Rbac struct {
	cache *cache.RbacRolesCache
}

func New(c *cache.RbacRolesCache) *Rbac {
	return &Rbac{cache: c}
}

func (r *Rbac) Add(role, code, method, path string) {
	if r == nil || r.cache == nil {
		return
	}
	r.cache.Add(role, cache.Resource{
		Role:             role,
		UserResourceCode: code,
		Method:           strings.ToUpper(method),
		Path:             path,
	})
}

func ValidateResourceAccess(resources []cache.Resource, urlPath, method string) bool {
	method = strings.ToUpper(method)
	for _, res := range resources {
		if res.Method != method {
			continue
		}
		if matchPath(res.Path, urlPath) {
			return true
		}
	}
	return false
}

func matchPath(pattern, path string) bool {
	if pattern == path {
		return true
	}

	pattern = strings.Trim(pattern, "/")
	path = strings.Trim(path, "/")

	patternSeg := strings.Split(pattern, "/")
	pathSeg := strings.Split(path, "/")

	// Segment wildcard matching: /a/*/c and /a/*/*/d.
	if len(patternSeg) == len(pathSeg) {
		for i := range patternSeg {
			if patternSeg[i] == "*" {
				continue
			}
			if patternSeg[i] != pathSeg[i] {
				return false
			}
		}
		return true
	}

	// Prefix wildcard matching: /a/b/* should match any deeper suffix.
	if len(patternSeg) > 0 && patternSeg[len(patternSeg)-1] == "*" {
		prefix := "/" + strings.Join(patternSeg[:len(patternSeg)-1], "/")
		return strings.HasPrefix("/"+path, prefix+"/") || "/"+path == prefix
	}

	return false
}
