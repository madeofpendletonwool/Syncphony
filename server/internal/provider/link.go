// SPDX-License-Identifier: AGPL-3.0-only

package provider

import "context"

// Linker connects a user's account to a provider. Both linking methods go
// through the same two steps: the UI collects input (a form, or an OAuth2
// redirect), then Complete turns it into credentials.
//
// Linkers are stateless: anything that must survive between BeginOAuth and
// Complete (a PKCE verifier) is returned to the core in OAuthStart.Secret and
// handed back in LinkInput.
type Linker interface {
	Method() LinkMethod
	// Fields lists the form fields for LinkCredentials. Nil for LinkOAuth2.
	Fields() []LinkField
	// BeginOAuth returns where to send the user for LinkOAuth2. Linkers
	// using LinkCredentials return ErrUnsupported.
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
)

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
