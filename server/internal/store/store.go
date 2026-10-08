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

// Option changes how Open opens a database.
type Option func(*options)

type options struct {
	beforeMigrate func(ctx context.Context, s *Store, from, to int64) error
}

// BeforeMigrate has Open call fn when an existing database has migrations
// to apply, before applying them, with its schema version and the one it's
// going to. s works for BackupTo but not for queries, whose tables may not
// match yet. An error from fn stops Open.
func BeforeMigrate(fn func(ctx context.Context, s *Store, from, to int64) error) Option {
	return func(o *options) { o.beforeMigrate = fn }
}

// Open opens (creating if needed) the SQLite database at path and applies
// pending migrations.
func Open(ctx context.Context, path string, opts ...Option) (*Store, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, err
	}
	s := &Store{Queries: New(db), db: db}
	if err := migrate(ctx, s, o); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// dsn is how the database at path is opened.
func dsn(path string) string {
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
	return "file:" + path + "?" + q.Encode()
}

func migrator(db *sql.DB) (*goose.Provider, error) {
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, err
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, db, fsys)
	if err != nil {
		return nil, fmt.Errorf("store: loading migrations: %w", err)
	}
	return p, nil
}

// SchemaVersion is the newest migration this build has: the schema
// version of a database it has opened.
func SchemaVersion() int64 {
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		panic(err)
	}
	// goose only reads the files here; it needs a *sql.DB but doesn't use it.
	p, err := goose.NewProvider(goose.DialectSQLite3, &sql.DB{}, fsys)
	if err != nil {
		panic(err)
	}
	srcs := p.ListSources()
	return srcs[len(srcs)-1].Version
}

func migrate(ctx context.Context, s *Store, o options) error {
	p, err := migrator(s.db)
	if err != nil {
		return err
	}
	if o.beforeMigrate != nil {
		from, err := p.GetDBVersion(ctx)
		if err != nil {
			return fmt.Errorf("store: reading the schema version: %w", err)
		}
		if to := SchemaVersion(); from > 0 && from < to {
			if err := o.beforeMigrate(ctx, s, from, to); err != nil {
				return err
			}
		}
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

// BackupTo writes a consistent copy of the database to path, which must
// not exist. The server keeps running while it does.
func (s *Store) BackupTo(ctx context.Context, path string) error {
	_, err := s.db.ExecContext(ctx, "VACUUM INTO ?", path)
	return err
}

// Size is how many bytes the database takes up, not counting its
// write-ahead log.
func (s *Store) Size(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, "SELECT page_count * page_size FROM pragma_page_count(), pragma_page_size()").Scan(&n)
	return n, err
}

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

	VisibilityOpen     = "open"
	VisibilityUnlisted = "unlisted"
	VisibilityPrivate  = "private"

	MemberJoined  = "member"
	MemberPending = "pending"

	ItemQueued  = "queued"
	ItemPlaying = "playing"
	ItemPlayed  = "played"
	ItemSkipped = "skipped"
	ItemRemoved = "removed"

	EndFinished = "finished"
	EndSkipped  = "skipped"
	EndRemoved  = "removed"
	EndError    = "error"

	AuditRoleChanged = "role_changed"
	AuditDisabled    = "disabled"
	AuditEnabled     = "enabled"
	AuditRemoved     = "removed"
	AuditDeletedSelf = "deleted_self"
)
