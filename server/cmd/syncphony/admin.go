// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/auth"
	"github.com/madeofpendletonwool/syncphony/server/internal/config"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

const adminUsage = `usage: syncphony admin <command>

commands:
  reset-link [-hours N] <username>
           print a one-time link that lets <username> set a new password or
           passkey, signing them out everywhere else. For when no admin can
           sign in to make one. Lasts 24 hours unless -hours says otherwise.
  backup   the same as "syncphony backup now".

Run it where the server's SYNCPHONY_* settings are set, e.g.
  docker compose exec syncphony syncphony admin reset-link alice`

func adminCommand(args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, adminUsage)
		return errors.New("admin: expected a command")
	}
	switch args[0] {
	case "reset-link":
		return resetLinkCommand(context.Background(), args[1:], os.Stdout)
	case "backup":
		return backupCommand(append([]string{"now"}, args[1:]...))
	default:
		fmt.Fprintln(os.Stderr, adminUsage)
		return fmt.Errorf("admin: unknown command %q", args[0])
	}
}

func resetLinkCommand(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("reset-link", flag.ContinueOnError)
	hours := fs.Int("hours", int(auth.DefaultResetTTL/time.Hour), "how long the link lasts, 1 to 168")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, adminUsage)
		return errors.New("admin reset-link: expected one username")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, filepath.Join(cfg.DataDir, "syncphony.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	accounts, err := auth.New(db, auth.Config{BaseURL: cfg.BaseURL})
	if err != nil {
		return err
	}
	u, link, err := accounts.CreateResetLinkFor(ctx, fs.Arg(0), time.Duration(*hours)*time.Hour)
	if errors.Is(err, auth.ErrNotFound) {
		return fmt.Errorf("admin reset-link: no user %q", fs.Arg(0))
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Reset link for %s (@%s), good once until %s:\n\n  %s\n\n", u.DisplayName, u.Username, link.ExpiresAt.Local().Format(time.RFC1123), link.URL)
	fmt.Fprintln(out, "Using it signs them out everywhere else.")
	return nil
}
