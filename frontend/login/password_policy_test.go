package login

import "testing"

func TestValidatePasswordPolicy(t *testing.T) {
	cases := []struct {
		name string
		pwd  string
		ok   bool
	}{
		{name: "valid", pwd: "StrongPass1!", ok: true},
		{name: "short", pwd: "Aa1!short", ok: false},
		{name: "missing-symbol", pwd: "StrongPass12", ok: false},
		{name: "missing-upper", pwd: "strongpass1!", ok: false},
		{name: "missing-lower", pwd: "STRONGPASS1!", ok: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePasswordPolicy(tc.pwd)
			if tc.ok && err != nil {
				t.Fatalf("expected valid password, got error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("expected policy error")
			}
		})
	}
}
