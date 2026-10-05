// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/madeofpendletonwool/syncphony/server/internal/vault"
)

const vaultUsage = `usage: syncphony vault <command>

commands:
  genkey   print a new master key for SYNCPHONY_VAULT_KEY
  rotate   re-wrap every stored credential with the current master key

To rotate the master key:
  1. syncphony vault genkey
  2. Set SYNCPHONY_VAULT_KEY to the new key and SYNCPHONY_VAULT_OLD_KEYS to
     the old one, and restart the server (it accepts both meanwhile).
  3. syncphony vault rotate
  4. Remove SYNCPHONY_VAULT_OLD_KEYS and restart.`

func vaultCommand(args []string) error {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, vaultUsage)
		return errors.New("vault: expected one command")
	}
	switch args[0] {
	case "genkey":
		fmt.Println(vault.GenerateKey())
		return nil
	case "rotate":
		ctx := context.Background()
		a, err := setup(ctx)
		if err != nil {
			return err
		}
		defer a.db.Close()
		n, failed, err := a.links.Rotate(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("re-wrapped %d linked account(s) with key %s\n", n, a.links.KeyID())
		if len(failed) > 0 {
			return fmt.Errorf("%d link(s) couldn't be re-wrapped (is their old key in SYNCPHONY_VAULT_OLD_KEYS?):\n  %s", len(failed), strings.Join(failed, "\n  "))
		}
		return nil
	default:
		fmt.Fprintln(os.Stderr, vaultUsage)
		return fmt.Errorf("vault: unknown command %q", args[0])
	}
}
