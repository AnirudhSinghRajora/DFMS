// Package auth provides authentication and authorization for the DFMS.
package auth

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

const (
	// bcryptCost is the adaptive cost factor. 12 is a good balance
	// between security and performance (~250ms per hash on modern hardware).
	bcryptCost = 12
)

// HashPassword generates a bcrypt hash of the plaintext password.
func HashPassword(password string) (string, error) {
	if len(password) < 8 {
		return "", fmt.Errorf("password must be at least 8 characters")
	}
	if len(password) > 72 {
		// bcrypt silently truncates after 72 bytes
		return "", fmt.Errorf("password must be at most 72 characters")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("failed to hash password: %w", err)
	}

	return string(hash), nil
}

// VerifyPassword compares a plaintext password against a bcrypt hash.
// Returns nil on success, an error if the password does not match.
func VerifyPassword(hashedPassword, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
}
