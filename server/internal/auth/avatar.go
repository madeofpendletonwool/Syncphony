// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"

	"github.com/madeofpendletonwool/syncphony/server/internal/avatar"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// AvatarURL is where an uploaded avatar is served. The version busts
// caches when it changes.
func AvatarURL(userID string, version int64) string {
	return fmt.Sprintf("/api/users/%s/avatar?v=%d", url.PathEscape(userID), version)
}

// UploadAvatar makes data, an uploaded picture, u's avatar.
func (s *Service) UploadAvatar(ctx context.Context, u store.User, data []byte) (store.User, error) {
	img, ct, err := avatar.Process(data)
	switch {
	case errors.Is(err, avatar.ErrNotImage):
		return u, invalid("avatar", "upload a JPEG, PNG, GIF or WebP image")
	case errors.Is(err, avatar.ErrTooLarge):
		return u, invalid("avatar", "that image is too large")
	case err != nil:
		return u, err
	}
	now := s.now()
	var out store.User
	err = s.db.Tx(ctx, func(q *store.Queries) error {
		if err := q.PutAvatar(ctx, store.PutAvatarParams{UserID: u.ID, Data: img, ContentType: ct, UpdatedAt: now}); err != nil {
			return err
		}
		out, err = q.UpdateUserProfile(ctx, store.UpdateUserProfileParams{
			ID: u.ID, DisplayName: u.DisplayName, Color: u.Color,
			Avatar: sql.NullString{String: AvatarURL(u.ID, now.UnixMilli()), Valid: true},
		})
		return err
	})
	return out, err
}

// Avatar returns a user's uploaded avatar.
func (s *Service) Avatar(ctx context.Context, userID string) (store.Avatar, error) {
	a, err := s.db.GetAvatar(ctx, userID)
	if store.IsNotFound(err) {
		return a, ErrNotFound
	}
	return a, err
}
