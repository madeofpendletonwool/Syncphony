// SPDX-License-Identifier: AGPL-3.0-only

// Package store is the server's database: SQLite in WAL mode, with schema
// migrations in migrations/ (goose) and typed queries generated from
// queries/ (sqlc, into *.gen.go).
//
// Conventions, chosen so a move to Postgres stays cheap:
//   - IDs are UUIDv7 strings from NewID, generated in Go.
//   - Times are TIMESTAMP columns written from Go in UTC; use Now. Nothing
//     relies on SQL-side defaults or date functions.
//   - Enums are TEXT with CHECK constraints, mirrored by constants here.
//   - JSON is stored as TEXT.
//
// After editing migrations or queries, run `make gen`.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"time"
	"uuid"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store is an open database. Its embedded Queries run outside a transaction;
// use Tx to run several in one.
type Store struct {
	*Queries
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path and applies
// pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	q := url.Values{}
	for _, p := range []string{
		"foreign_keys(1)",
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
		// Wait for a writer instead of failing with SQLITE_BUSY.
		"busy_timeout(5000)",
	} {
		q.Add("_pragma", p)
	}
	// Write times in a format that sorts and compares correctly as text.
	q.Set("_time_format", "sqlite")
	// Take the write lock at BEGIN, so concurrent read-then-write
	// transactions wait rather than fail when upgrading their lock.
	q.Set("_txlock", "immediate")

	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{Queries: New(db), db: db}, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, db, fsys)
	if err != nil {
		return fmt.Errorf("store: loading migrations: %w", err)
	}
	results, err := p.Up(ctx)
	if err != nil {
		return fmt.Errorf("store: migrating: %w", err)
	}
	for _, r := range results {
		slog.Info("applied migration", "version", r.Source.Version, "file", r.Source.Path, "took", r.Duration)
	}
	return nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Tx runs fn in a transaction, committing if it returns nil.
func (s *Store) Tx(ctx context.Context, fn func(q *Queries) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(s.WithTx(tx)); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	return tx.Commit()
}

// RedeemInvite marks an unused, unexpired invite as used by userID. It
// returns an IsNotFound error if the invite is missing, used, or expired.
func (q *Queries) RedeemInvite(ctx context.Context, code, userID string, now time.Time) (Invite, error) {
	return q.MarkInviteUsed(ctx, MarkInviteUsedParams{
		UserID: sql.NullString{String: userID, Valid: true},
		UsedAt: sql.NullTime{Time: now, Valid: true},
		Code:   code,
		Now:    now,
	})
}

// NewID returns a new time-ordered ID.
func NewID() string { return uuid.NewV7().String() }

// Now returns the current time in the form the store expects: UTC, to the
// microsecond.
func Now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

// IsAutopilot reports whether autopilot queued the item, rather than a
// member. AddedBy is then whose taste seeded it, not who chose it.
func (it QueueItem) IsAutopilot() bool { return it.Autopilot.Valid }

// IsNotFound reports whether err means a query matched no rows.
func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// Values for enum columns.
const (
	RoleAdmin  = "admin"
	RoleMember = "member"

	LinkOK      = "ok"
	LinkExpired = "expired"
	LinkError   = "error"

	FairnessRoundRobin = "round_robin"
	FairnessFIFO       = "fifo"

	ItemQueued  = "queued"
	ItemPlaying = "playing"
	ItemPlayed  = "played"
	ItemSkipped = "skipped"
	ItemRemoved = "removed"

	EndFinished = "finished"
	EndSkipped  = "skipped"
	EndRemoved  = "removed"
	EndError    = "error"
)
