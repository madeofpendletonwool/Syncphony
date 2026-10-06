// SPDX-License-Identifier: AGPL-3.0-only

package provider

import (
	"context"
	"time"
)

// Linker connects a user's account to a provider. Every linking method goes
// through the same two steps: the UI collects input (a form, an OAuth2
// redirect, or a device pairing), then Complete turns it into credentials.
//
// Linkers are stateless: anything that must survive between BeginOAuth and
// Complete (a PKCE verifier) is returned to the core in OAuthStart.Secret and
// handed back in LinkInput. The same goes for device pairing (DevicePairer).
type Linker interface {
	Method() LinkMethod
	// Fields lists the form fields for LinkCredentials. Nil otherwise.
	Fields() []LinkField
	// BeginOAuth returns where to send the user for LinkOAuth2. Linkers
	// using another method return ErrUnsupported.
	BeginOAuth(ctx context.Context, req OAuthRequest) (OAuthStart, error)
	// Complete validates the input against the service and returns the
	// credentials to store. Rejected input is ErrInvalidCredentials.
	Complete(ctx context.Context, in LinkInput) (Credentials, AccountInfo, error)
}

// LinkMethod is how a user links an account.
type LinkMethod string

const (
	// LinkCredentials collects Fields in a form (Navidrome: URL, username, password).
	LinkCredentials LinkMethod = "credentials"
	// LinkOAuth2 redirects to the service to authorize (Spotify).
	LinkOAuth2 LinkMethod = "oauth2"
	// LinkDevice pairs by showing a URL and a code that the user approves
	// on any device (OAuth2's device authorization grant). The linker
	// implements DevicePairer.
	LinkDevice LinkMethod = "device"
)

// DevicePairer is implemented by linkers that pair a device. A LinkDevice
// linker pairs as its only step. A LinkOAuth2 linker may implement it to pair
// before the OAuth2 redirect (Spotify does: pairing gets the streaming login,
// OAuth2 the Web API's). Either way, the pairing's result reaches Complete in
// LinkInput.Paired.
type DevicePairer interface {
	// BeginPairing asks the service for a code for the user to approve.
	BeginPairing(ctx context.Context) (Pairing, error)
	// PollPairing checks once whether the user has approved the pairing
	// with secret. It returns ErrPending until they have, and
	// ErrInvalidCredentials if they declined or the code expired. The
	// result is opaque provider state for Complete.
	PollPairing(ctx context.Context, secret string) (string, error)
}

// Pairing is a device pairing waiting for the user's approval.
type Pairing struct {
	// VerifyURL is where the user approves, with the code filled in if the
	// service supports that.
	VerifyURL string
	// UserCode is the code the user checks (or types) at VerifyURL.
	UserCode string
	// Interval is the least time between polls.
	Interval time.Duration
	// ExpiresIn is how long the code stays valid.
	ExpiresIn time.Duration
	// Secret is opaque provider state (a device code). The core keeps it
	// server-side and passes it to PollPairing.
	Secret string
}

// LinkField is one input of a credentials form.
type LinkField struct {
	// Name is the key in LinkInput.Fields.
	Name        string
	Label       string
	Kind        FieldKind
	Required    bool
	Placeholder string
	Help        string
}

// FieldKind controls how the UI renders and treats a field.
type FieldKind string

// Field kinds.
const (
	FieldText FieldKind = "text"
	FieldURL  FieldKind = "url"
	// FieldSecret is masked in the UI and never echoed back or logged.
	FieldSecret FieldKind = "secret"
)

// OAuthRequest starts an OAuth2 link.
type OAuthRequest struct {
	// State is the core's CSRF token; it must be passed through to the service.
	State string
	// RedirectURL is the server's callback URL. The core owns the route, so
	// providers don't need to know the server's base URL.
	RedirectURL string
}

// OAuthStart is where to send the user, plus provider state to keep until Complete.
type OAuthStart struct {
	AuthURL string
	// Secret is opaque provider state (e.g. a PKCE verifier). The core stores
	// it server-side with State and returns it in LinkInput.OAuthSecret.
	Secret string
}

// LinkInput is what the UI collected.
type LinkInput struct {
	// Fields holds form values for LinkCredentials, keyed by LinkField.Name.
	Fields map[string]string
	// Code, RedirectURL and OAuthSecret are set for LinkOAuth2. The core
	// has already checked the state parameter.
	Code        string
	RedirectURL string
	OAuthSecret string
	// Paired is PollPairing's result, for linkers that implement DevicePairer.
	Paired string
}

// Credentials are opaque to everything but the provider that made them.
// The vault encrypts and stores them as-is.
type Credentials []byte

// AccountInfo identifies the account on the service side.
type AccountInfo struct {
	// ID is stable for the account on that service (for a self-hosted
	// service, include the server URL). The core uses it to stop the same
	// account being linked twice.
	ID string
	// Name is a display name, e.g. "alice on music.example.com".
	Name string
}

// Link is a linked account, as passed to Provider.Open.
type Link struct {
	// ID is the Syncphony link ID. Sessions put it in every TrackRef.
	ID          string
	Account     AccountInfo
	Credentials Credentials
	// Sink saves rotated credentials (e.g. after an OAuth2 refresh). It may
	// be nil in tests.
	Sink CredentialSink
}

// CredentialSink persists new credentials for a link. The session calls it
// whenever its credentials change; the vault re-encrypts and saves them.
type CredentialSink func(ctx context.Context, c Credentials) error
