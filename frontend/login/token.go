package login

import (
	"crypto/rand"
	"encoding/hex"
)

func newSessionToken() string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
