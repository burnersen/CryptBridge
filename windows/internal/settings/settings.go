// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

// Package settings stores everything CryptBridge needs to remember. The
// whole file is encrypted with Windows DPAPI, bound to the user's Windows
// account: the pCloud sign-in and the vault keys never touch the disk in a
// readable form.
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/burnersen/CryptBridge/windows/internal/pcloud"
	"github.com/burnersen/CryptBridge/windows/internal/vault"
)

// fileHeader marks the file format so that a future version can recognise
// and convert older files.
var fileHeader = []byte("CryptBridge-Settings-1\n")

// Default sizes of the drive cache.
const (
	DefaultCacheMaxSize      = "20G" // the project's required cache size
	DefaultCacheMinFreeSpace = "10G" // emergency brake for the system disk
	DefaultDriveLetter       = "Z"
)

// Drive holds the settings of the drive letter.
type Drive struct {
	Letter            string `json:"letter"`
	CacheDir          string `json:"cacheDir"`
	CacheMaxSize      string `json:"cacheMaxSize"`
	CacheMinFreeSpace string `json:"cacheMinFreeSpace"`
}

// Settings is everything CryptBridge remembers.
type Settings struct {
	PCloud        pcloud.Account `json:"pcloud"`
	PCloudApp     pcloud.App     `json:"pcloudApp"`   // empty: rclone's built-in app
	SignInAppID   string         `json:"signInAppId"` // client ID the sign-in was made with; empty: rclone's app
	Vault         vault.Config   `json:"vault"`
	Drive         Drive          `json:"drive"`
	AutoConnect   bool           `json:"autoConnect"`   // connect the drive when CryptBridge starts
	AutoReconnect bool           `json:"autoReconnect"` // reconnect after rclone stopped unexpectedly
}

// Defaults returns the settings of a fresh installation.
func Defaults(cacheDir string) Settings {
	return Settings{
		Drive: Drive{
			Letter:            DefaultDriveLetter,
			CacheDir:          cacheDir,
			CacheMaxSize:      DefaultCacheMaxSize,
			CacheMinFreeSpace: DefaultCacheMinFreeSpace,
		},
		AutoConnect:   true,
		AutoReconnect: true,
	}
}

// SignedIn reports whether a pCloud sign-in is stored.
func (s Settings) SignedIn() bool {
	return s.PCloud.Token != "" && s.PCloud.Hostname != ""
}

// HasVault reports whether vault keys are stored.
func (s Settings) HasVault() bool {
	return s.Vault.Password != "" && s.Vault.NormalisedPath() != ""
}

// Load reads the settings. A missing file is not an error: it means a fresh
// installation, and the defaults are returned.
func Load(path, defaultCacheDir string) (Settings, error) {
	defaults := Defaults(defaultCacheDir)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaults, nil
	}
	if err != nil {
		return defaults, fmt.Errorf("read settings: %w", err)
	}
	if !bytes.HasPrefix(data, fileHeader) {
		return defaults, errors.New("settings file has an unknown format")
	}
	plain, err := unprotect(data[len(fileHeader):])
	if err != nil {
		return defaults, fmt.Errorf("decrypt settings (was the file copied from another Windows account?): %w", err)
	}
	settings := defaults
	if err := json.Unmarshal(plain, &settings); err != nil {
		return defaults, fmt.Errorf("read settings: %w", err)
	}
	return settings, nil
}

// Save encrypts and writes the settings. The old file is replaced only
// after the new one is completely on disk.
func Save(path string, s Settings) error {
	plain, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	protected, err := protect(plain)
	if err != nil {
		return fmt.Errorf("encrypt settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create settings folder: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(append([]byte{}, fileHeader...), protected...), 0o600); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}
