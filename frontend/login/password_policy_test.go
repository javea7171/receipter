package login

import "testing"

func TestValidatePasswordPolicy(t *testing.T) {
	cases := []struct {
		name string
		pwd  string
		ok   bool
	}{
		{name: "valid letters only", pwd: "abcde", ok: true},
		{name: "valid mixed", pwd: "A1!bc", ok: true},
		{name: "short", pwd: "abcd", ok: false},
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
