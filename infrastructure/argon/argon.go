package argon

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Params controls argon2id hashing behavior.
type Params struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

var DefaultParams = &Params{
	Memory:      64 * 1024,
	Iterations:  2,
	Parallelism: 1,
	SaltLength:  16,
	KeyLength:   32,
}

func CreateHash(password string, p *Params) (string, error) {
	if strings.TrimSpace(password) == "" {
		return "", errors.New("password is required")
	}
	if p == nil {
		p = DefaultParams
	}

	salt, err := generateRandomBytes(p.SaltLength)
	if err != nil {
		return "", err
	}

	hash := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)
	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)

	encoded := fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", p.Memory, p.Iterations, p.Parallelism, b64Salt, b64Hash)
	return encoded, nil
}

func ComparePasswordAndHash(password, encodedHash string) (bool, error) {
	p, salt, hash, err := decodeHash(encodedHash)
	if err != nil {
		return false, err
	}

	otherHash := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, uint32(len(hash)))
	if subtle.ConstantTimeCompare(hash, otherHash) == 1 {
		return true, nil
	}
	return false, nil
}

func decodeHash(encodedHash string) (*Params, []byte, []byte, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 {
		return nil, nil, nil, errors.New("invalid hash format")
	}
	if parts[1] != "argon2id" {
		return nil, nil, nil, errors.New("invalid hash variant")
	}

	p := &Params{}
	_, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Parallelism)
	if err != nil {
		return nil, nil, nil, errors.New("invalid hash parameters")
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, nil, nil, errors.New("invalid salt")
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return nil, nil, nil, errors.New("invalid hash")
	}
	p.SaltLength = uint32(len(salt))
	p.KeyLength = uint32(len(hash))
	return p, salt, hash, nil
}

func generateRandomBytes(n uint32) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return nil, err
	}
	return b, nil
}
