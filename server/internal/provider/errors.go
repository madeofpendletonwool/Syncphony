// SPDX-License-Identifier: AGPL-3.0-only

package provider

import (
	"errors"
	"fmt"
	"time"
)

// Providers map service errors to these, possibly wrapped, so the core can
// react the same way to every service. Check them with errors.Is.
var (
	// ErrNotFound: the track, album, etc. doesn't exist (or no longer does).
	ErrNotFound = errors.New("provider: not found")
	// ErrAuthExpired: the link's credentials no longer work. The user must
	// relink; retrying won't help.
	ErrAuthExpired = errors.New("provider: authorization expired")
	// ErrInvalidCredentials: Linker.Complete rejected the input.
	ErrInvalidCredentials = errors.New("provider: invalid credentials")
	// ErrUnavailable: the service is unreachable or failing. Retry later.
	ErrUnavailable = errors.New("provider: service unavailable")
	// ErrRateLimited: the service is throttling us. Use RetryAfter for how long.
	ErrRateLimited = errors.New("provider: rate limited")
	// ErrRange: a stream's byte range is outside the file.
	ErrRange = errors.New("provider: range not satisfiable")
	// ErrUnsupported: the provider doesn't support this operation.
	ErrUnsupported = errors.New("provider: not supported")
	// ErrPending: DevicePairer.PollPairing is still waiting for the user.
	ErrPending = errors.New("provider: waiting for approval")
	// ErrNotPlayable: the service won't play this track for this account,
	// though it exists. Retrying won't help.
	ErrNotPlayable = errors.New("provider: the service won't play this track")
)

// RateLimitError is ErrRateLimited with the service's requested delay.
type RateLimitError struct {
	// RetryAfter is 0 if the service didn't say.
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter <= 0 {
		return ErrRateLimited.Error()
	}
	return fmt.Sprintf("%s: retry after %s", ErrRateLimited, e.RetryAfter)
}

// Is makes errors.Is(err, ErrRateLimited) true.
func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

// RetryAfter returns the delay from a RateLimitError in err's chain.
func RetryAfter(err error) (time.Duration, bool) {
	var rl *RateLimitError
	if errors.As(err, &rl) {
		return rl.RetryAfter, true
	}
	return 0, false
}
