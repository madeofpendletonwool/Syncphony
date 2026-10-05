// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/url"

	"github.com/madeofpendletonwool/syncphony/server/internal/links"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// linksPage is where the OAuth2 callback sends the browser.
const linksPage = "/settings/services"

func toProviderInfo(p provider.Provider) ProviderInfo {
	info := p.Info()
	l := p.Linker()
	out := ProviderInfo{
		Id: info.ID, Name: info.Name, Icon: info.Icon,
		Playback:   ProviderInfoPlayback(info.Capabilities.Playback),
		LinkMethod: ProviderInfoLinkMethod(l.Method()),
		Fields:     []LinkField{},
	}
	c := info.Capabilities
	out.Capabilities.Artwork, out.Capabilities.Isrc, out.Capabilities.Lyrics, out.Capabilities.Playlists = c.Artwork, c.ISRC, c.Lyrics, c.Playlists
	out.Capabilities.Shareable = c.Shareable
	out.Capabilities.Search = []ProviderInfoCapabilitiesSearch{}
	for _, k := range c.Search {
		out.Capabilities.Search = append(out.Capabilities.Search, ProviderInfoCapabilitiesSearch(k))
	}
	for _, f := range l.Fields() {
		lf := LinkField{Name: f.Name, Label: f.Label, Kind: LinkFieldKind(f.Kind), Required: f.Required}
		if f.Placeholder != "" {
			lf.Placeholder = &f.Placeholder
		}
		if f.Help != "" {
			lf.Help = &f.Help
		}
		out.Fields = append(out.Fields, lf)
	}
	return out
}

// linkStatus maps stored statuses to the API's. The database says
// "expired" for what the API calls needs_relink.
func linkStatus(s string) ServiceLinkStatus {
	switch s {
	case store.LinkOK:
		return ServiceLinkStatusOk
	case store.LinkExpired:
		return ServiceLinkStatusNeedsRelink
	default:
		return ServiceLinkStatusError
	}
}

// toServiceLink deliberately has no access to the credentials column's
// meaning: it copies only display fields.
func toServiceLink(l store.ServiceLink) ServiceLink {
	out := ServiceLink{
		Id: l.ID, OwnerId: l.UserID, Provider: l.Provider, AccountLabel: l.AccountLabel, Status: linkStatus(l.Status),
		Shared: l.Shared, CreatedAt: l.CreatedAt, LastOkAt: timePtr(l.LastOkAt.Time, l.LastOkAt.Valid),
	}
	if l.StatusDetail != "" {
		out.StatusDetail = &l.StatusDetail
	}
	return out
}

// ListProviders lists linkable services.
func (s *Server) ListProviders(context.Context, ListProvidersRequestObject) (ListProvidersResponseObject, error) {
	ps := s.Links.Providers()
	out := make(ListProviders200JSONResponse, len(ps))
	for i, p := range ps {
		out[i] = toProviderInfo(p)
	}
	return out, nil
}

// ListLinks lists the user's links, and with include=shared everyone
// else's shared ones.
func (s *Server) ListLinks(ctx context.Context, req ListLinksRequestObject) (ListLinksResponseObject, error) {
	userID := sessionFrom(ctx).User.ID
	list := s.Links.List
	if req.Params.Include != nil && *req.Params.Include == Shared {
		list = s.Links.Usable
	}
	rows, err := list(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make(ListLinks200JSONResponse, len(rows))
	for i, r := range rows {
		out[i] = toServiceLink(r)
	}
	return out, nil
}

// CreateLink links an account with a form.
func (s *Server) CreateLink(ctx context.Context, req CreateLinkRequestObject) (CreateLinkResponseObject, error) {
	l, err := s.Links.LinkWithCredentials(ctx, sessionFrom(ctx).User.ID, req.Body.Provider, req.Body.Fields)
	if err != nil {
		return nil, err
	}
	return CreateLink201JSONResponse(toServiceLink(l)), nil
}

// Relink replaces a link's credentials.
func (s *Server) Relink(ctx context.Context, req RelinkRequestObject) (RelinkResponseObject, error) {
	l, err := s.Links.RelinkWithCredentials(ctx, sessionFrom(ctx).User.ID, req.Id, req.Body.Fields)
	if err != nil {
		return nil, err
	}
	return Relink200JSONResponse(toServiceLink(l)), nil
}

// UpdateLink shares or unshares one of the user's links.
func (s *Server) UpdateLink(ctx context.Context, req UpdateLinkRequestObject) (UpdateLinkResponseObject, error) {
	l, err := s.Links.SetShared(ctx, sessionFrom(ctx).User.ID, req.Id, req.Body.Shared)
	if err != nil {
		return nil, err
	}
	return UpdateLink200JSONResponse(toServiceLink(l)), nil
}

// Unlink deletes a link.
func (s *Server) Unlink(ctx context.Context, req UnlinkRequestObject) (UnlinkResponseObject, error) {
	if err := s.Links.Unlink(ctx, sessionFrom(ctx).User.ID, req.Id); err != nil {
		return nil, err
	}
	return Unlink204Response{}, nil
}

// BeginOAuthLink starts an OAuth2 link.
func (s *Server) BeginOAuthLink(ctx context.Context, req BeginOAuthLinkRequestObject) (BeginOAuthLinkResponseObject, error) {
	var providerID, linkID string
	if req.Body.Provider != nil {
		providerID = *req.Body.Provider
	}
	if req.Body.LinkId != nil {
		linkID = *req.Body.LinkId
	}
	if (providerID == "") == (linkID == "") {
		return nil, &links.InvalidInputError{Field: "provider", Message: "set exactly one of provider or linkId"}
	}
	authURL, err := s.Links.BeginOAuth(ctx, sessionFrom(ctx).User.ID, providerID, linkID)
	if err != nil {
		return nil, err
	}
	return BeginOAuthLink200JSONResponse{AuthUrl: authURL}, nil
}

// CompleteOAuthLink is the OAuth2 redirect target. It's public in the spec
// so that every outcome, including "not signed in", redirects to the app.
func (s *Server) CompleteOAuthLink(ctx context.Context, req CompleteOAuthLinkRequestObject) (CompleteOAuthLinkResponseObject, error) {
	back := func(key, value string) (CompleteOAuthLinkResponseObject, error) {
		loc := s.BaseURL + linksPage + "?" + url.Values{key: {value}}.Encode()
		return CompleteOAuthLink303Response{Headers: CompleteOAuthLink303ResponseHeaders{Location: &loc}}, nil
	}
	sess, err := s.Auth.Authenticate(ctx, requestFrom(ctx).token)
	if err != nil {
		return back("link_error", "unauthenticated")
	}
	p := req.Params
	if p.Error != nil {
		return back("link_error", "denied")
	}
	if p.State == nil || p.Code == nil {
		return back("link_error", "bad_request")
	}
	l, err := s.Links.CompleteOAuth(ctx, sess.User.ID, *p.State, *p.Code)
	if err != nil {
		code := errorCode(err)
		if code == "internal" {
			slog.Error("completing OAuth link", "err", err)
		}
		return back("link_error", code)
	}
	return back("linked", l.ID)
}

// errorCode is the API error code writeError would use for err.
func errorCode(err error) string {
	for _, e := range errorCodes {
		if errors.Is(err, e.err) {
			return e.code
		}
	}
	return "internal"
}
