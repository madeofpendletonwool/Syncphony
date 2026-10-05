// SPDX-License-Identifier: AGPL-3.0-only

// Package httpapi implements the HTTP API described by api/openapi.yaml.
package httpapi

import (
	"context"
	"net/http"
)

// Server implements StrictServerInterface.
type Server struct {
	Version string
}

var _ StrictServerInterface = (*Server)(nil)

// Handler returns the API mounted under /api.
func (s *Server) Handler() http.Handler {
	return HandlerWithOptions(NewStrictHandler(s, nil), StdHTTPServerOptions{
		BaseURL:    "/api",
		BaseRouter: http.NewServeMux(),
	})
}

// GetHealth reports liveness and the build version.
func (s *Server) GetHealth(context.Context, GetHealthRequestObject) (GetHealthResponseObject, error) {
	return GetHealth200JSONResponse{Status: Ok, Version: s.Version}, nil
}
