package argon

import "testing"

func TestCreateAndCompare(t *testing.T) {
	hash, err := CreateHash("secret-pass", DefaultParams)
	if err != nil {
		t.Fatalf("create hash: %v", err)
	}
	ok, err := ComparePasswordAndHash("secret-pass", hash)
	if err != nil {
		t.Fatalf("compare hash: %v", err)
	}
	if !ok {
		t.Fatalf("expected password to match")
	}

	ok, err = ComparePasswordAndHash("wrong", hash)
	if err != nil {
		t.Fatalf("compare hash wrong: %v", err)
	}
	if ok {
		t.Fatalf("expected password mismatch")
	}
}
