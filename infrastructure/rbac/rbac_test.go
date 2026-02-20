package rbac

import "testing"

func TestMatchPathWildcardSegments(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		ok      bool
	}{
		{pattern: "/tasker/api/pallets/*/receipts", path: "/tasker/api/pallets/1/receipts", ok: true},
		{pattern: "/tasker/pallets/*/label", path: "/tasker/pallets/10/label", ok: true},
		{pattern: "/tasker/exports/pallet/*", path: "/tasker/exports/pallet/1.csv", ok: true},
		{pattern: "/tasker/admin/users", path: "/tasker/admin/users", ok: true},
		{pattern: "/tasker/admin/users", path: "/tasker/admin/users/1", ok: false},
		{pattern: "/tasker/api/pallets/*/receipts", path: "/tasker/api/pallets/1/close", ok: false},
	}

	for _, tc := range cases {
		if got := matchPath(tc.pattern, tc.path); got != tc.ok {
			t.Fatalf("pattern=%s path=%s expected=%v got=%v", tc.pattern, tc.path, tc.ok, got)
		}
	}
}
