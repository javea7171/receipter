package login

import (
	"errors"
	"unicode"
)

func ValidatePasswordPolicy(password string) error {
	if len(password) < 12 {
		return errors.New("password must be at least 12 characters")
	}

	var hasUpper, hasLower, hasDigit, hasSymbol bool
	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			hasSymbol = true
		}
	}

	if !hasUpper || !hasLower || !hasDigit || !hasSymbol {
		return errors.New("password must include upper, lower, digit and symbol")
	}

	return nil
}
