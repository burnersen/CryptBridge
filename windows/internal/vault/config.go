// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

// Package vault describes an rclone crypt vault, registers it with the
// running rclone process, and checks keys before anything is written.
//
// Why the checks matter: rclone crypt never says "wrong password". With a
// wrong key a vault simply looks empty (measured: "rclone ls" exits 0 and
// only logs "Skipping undecryptable file name"). Anything copied into such
// an "empty" vault is encrypted with the wrong key, and the vault is split
// into two halves that never see each other again.
package vault

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/burnersen/CryptBridge/windows/internal/rcd"
)

// Values of rclone's crypt options.
const (
	EncryptionStandard  = "standard"
	EncryptionObfuscate = "obfuscate"
	EncryptionOff       = "off"

	EncodingBase32    = "base32"
	EncodingBase64    = "base64"
	EncodingBase32768 = "base32768"

	DefaultSuffix = ".bin"
)

// Config is the complete description of a vault. Every field must be
// identical on every computer that opens the vault — password and salt are
// not enough.
type Config struct {
	Path                    string `json:"path"`                    // folder in pCloud ("CryptBridge") or on this PC (`E:\Vault`)
	FilenameEncryption      string `json:"filenameEncryption"`      // standard, obfuscate or off
	DirectoryNameEncryption bool   `json:"directoryNameEncryption"` // encrypt folder names too
	FilenameEncoding        string `json:"filenameEncoding"`        // base32, base64 or base32768
	Suffix                  string `json:"suffix"`                  // file ending, used only with "off"
	Password                string `json:"password"`                // encryption password
	Password2               string `json:"password2"`               // salt; may be empty
}

// NewDefault returns the rclone defaults for a vault in the given folder.
func NewDefault(path string) Config {
	return Config{
		Path:                    path,
		FilenameEncryption:      EncryptionStandard,
		DirectoryNameEncryption: true,
		FilenameEncoding:        EncodingBase32,
		Suffix:                  DefaultSuffix,
	}
}

// NormalisedPath returns the folder without leading or trailing slashes, so
// that "/Vault/" and "Vault" mean the same folder everywhere.
func (c Config) NormalisedPath() string {
	return strings.Trim(strings.ReplaceAll(c.Path, `\`, "/"), "/ ")
}

// Validate rejects configurations rclone would misread or that cannot work.
func (c Config) Validate() error {
	if c.NormalisedPath() == "" {
		return errors.New("choose a folder for the vault (the top level of pCloud or of a drive cannot be used)")
	}
	if c.Password == "" {
		return errors.New("the encryption password must not be empty")
	}
	switch c.FilenameEncryption {
	case EncryptionStandard, EncryptionObfuscate, EncryptionOff:
	default:
		return fmt.Errorf("unknown file name encryption %q", c.FilenameEncryption)
	}
	switch c.FilenameEncoding {
	case EncodingBase32, EncodingBase64, EncodingBase32768:
	default:
		return fmt.Errorf("unknown file name encoding %q", c.FilenameEncoding)
	}
	if c.FilenameEncryption == EncryptionOff && c.Suffix == "" {
		return errors.New("a file ending is required when file names are not encrypted")
	}
	return nil
}

// parameters turns the configuration into rclone crypt options. Passwords
// are passed in plain text and obscured by rclone itself (opt.obscure).
func (c Config) parameters(baseRemote string) map[string]string {
	params := map[string]string{
		"remote":                    baseRemote + c.NormalisedPath(),
		"filename_encryption":       c.FilenameEncryption,
		"directory_name_encryption": fmt.Sprint(c.DirectoryNameEncryption),
		"filename_encoding":         c.FilenameEncoding,
		"suffix":                    c.Suffix,
		"password":                  c.Password,
	}
	if c.Password2 != "" {
		params["password2"] = c.Password2
	}
	return params
}

// Configure registers the vault in rclone's in-memory configuration under
// name (without colon), on top of baseRemote (e.g. "cbpcloud:"). An older
// remote of the same name is removed first so that no stale option — such
// as a salt that is no longer wanted — survives.
func Configure(ctx context.Context, client *rcd.Client, name, baseRemote string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	_ = Remove(ctx, client, name)
	request := map[string]any{
		"name":       name,
		"type":       "crypt",
		"parameters": c.parameters(baseRemote),
		"opt":        map[string]any{"nonInteractive": true, "obscure": true},
	}
	if err := client.Call(ctx, "config/create", request, nil); err != nil {
		return fmt.Errorf("register vault: %w", err)
	}
	return nil
}

// Remove deletes a remote from rclone's in-memory configuration.
func Remove(ctx context.Context, client *rcd.Client, name string) error {
	return client.Call(ctx, "config/delete", map[string]any{"name": name}, nil)
}
