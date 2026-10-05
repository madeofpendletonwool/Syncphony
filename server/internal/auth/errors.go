// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"errors"
	"fmt"
	"time"
)

// Errors returned by Service. The HTTP layer maps them to statuses.
var (
	// ErrInvalidCredentials: wrong username, password or passkey. Deliberately
	// doesn't say which.
	ErrInvalidCredentials = errors.New("invalid username or password")
	// ErrUnauthenticated: no valid session.
	ErrUnauthenticated = errors.New("not signed in")
	// ErrForbidden: signed in, but not allowed.
	ErrForbidden = errors.New("not allowed")
	// ErrInviteInvalid: the invite doesn't exist, was used, or expired.
	ErrInviteInvalid = errors.New("this invite link is invalid, used, or expired")
	// ErrUsernameTaken: someone already has that username.
	ErrUsernameTaken = errors.New("that username is taken")
	// ErrCeremonyExpired: the WebAuthn ceremony is unknown or timed out.
	ErrCeremonyExpired = errors.New("passkey request expired; try again")
	// ErrPasskeyFailed: the browser's WebAuthn response didn't verify.
	ErrPasskeyFailed = errors.New("passkey verification failed")
	// ErrLastCredential: removing this would leave no way to sign in.
	ErrLastCredential = errors.New("you need at least one passkey or a password")
	// ErrWrongPassword: the current password for a change was wrong.
	ErrWrongPassword = errors.New("current password is wrong")
	// ErrNotFound: the thing doesn't exist (or isn't yours).
	ErrNotFound = errors.New("not found")
)

// InvalidInputError is a validation failure, with a message for the user.
type InvalidInputError struct {
	Field, Message string
}

func (e *InvalidInputError) Error() string { return e.Field + ": " + e.Message }

func invalid(field, format string, args ...any) error {
	return &InvalidInputError{Field: field, Message: fmt.Sprintf(format, args...)}
}

// RateLimitError means too many failed attempts.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("too many attempts; try again in %s", e.RetryAfter.Round(time.Second))
}
