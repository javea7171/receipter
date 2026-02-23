package login

import (
	"errors"
	"unicode/utf8"
)

func ValidatePasswordPolicy(password string) error {
	if utf8.RuneCountInString(password) < 5 {
		return errors.New("password must be at least 5 characters")
	}

	return nil
}
