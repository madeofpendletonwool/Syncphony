// SPDX-License-Identifier: AGPL-3.0-only

package vault

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// KeySource says where the master keys come from.
type KeySource struct {
	// Key is a base64 master key (SYNCPHONY_VAULT_KEY). It wins over KeyFile.
	Key string
	// KeyFile holds a base64 master key (SYNCPHONY_VAULT_KEY_FILE).
	KeyFile string
	// OldKeys are previous base64 keys, still accepted for opening records
	// until `syncphony vault rotate` re-wraps them (SYNCPHONY_VAULT_OLD_KEYS).
	OldKeys []string
}

// Load builds a vault from src. If neither Key nor KeyFile is set, it uses
// defaultFile, generating a key there on first run; created reports that.
func Load(src KeySource, defaultFile string) (v *Vault, created bool, err error) {
	var current *Key
	switch {
	case src.Key != "":
		if current, err = ParseKey(src.Key); err != nil {
			return nil, false, fmt.Errorf("SYNCPHONY_VAULT_KEY: %w", err)
		}
	case src.KeyFile != "":
		if current, err = readKeyFile(src.KeyFile); err != nil {
			return nil, false, fmt.Errorf("SYNCPHONY_VAULT_KEY_FILE: %w", err)
		}
	default:
		current, err = readKeyFile(defaultFile)
		if errors.Is(err, fs.ErrNotExist) {
			if err := os.WriteFile(defaultFile, []byte(GenerateKey()+"\n"), 0o600); err != nil {
				return nil, false, fmt.Errorf("vault: creating key file: %w", err)
			}
			created = true
			current, err = readKeyFile(defaultFile)
		}
		if err != nil {
			return nil, false, err
		}
	}
	var old []*Key
	for i, s := range src.OldKeys {
		k, err := ParseKey(s)
		if err != nil {
			return nil, false, fmt.Errorf("SYNCPHONY_VAULT_OLD_KEYS[%d]: %w", i, err)
		}
		old = append(old, k)
	}
	return New(current, old...), created, nil
}

func readKeyFile(path string) (*Key, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the operator chooses this path
	if err != nil {
		return nil, err
	}
	k, err := ParseKey(string(b))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return k, nil
}
