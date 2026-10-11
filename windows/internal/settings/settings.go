// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

// Package settings stores everything CryptBridge needs to remember. The
// whole file is encrypted with Windows DPAPI, bound to the user's Windows
// account: the pCloud sign-in and the vault keys never touch the disk in a
// readable form.
//
// With the app lock on, the secrets are sealed once more with the user's
// app password: DPAPI opens the file for anyone signed in to the Windows
// account, the app password only for the user.
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/burnersen/CryptBridge/windows/internal/passlock"
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
	AppLock       bool           `json:"appLock"`       // the secrets are sealed with the app password
	LockOffered   bool           `json:"lockOffered"`   // the app password was offered once (set up or declined)
}

// secrets are the parts of the settings that the app password protects.
// Everything else (drive, behaviour) stays readable, so that a locked
// CryptBridge can still show its window.
type secrets struct {
	PCloud      pcloud.Account `json:"pcloud"`
	PCloudApp   pcloud.App     `json:"pcloudApp"`
	SignInAppID string         `json:"signInAppId"`
	Vault       vault.Config   `json:"vault"`
}

// fileFormat is what is stored inside the DPAPI layer: the settings, and
// with the app lock on, the sealed secrets instead of the readable ones.
type fileFormat struct {
	Settings
	Sealed *passlock.Sealed `json:"sealed,omitempty"`
}

// ErrNeedsKey: settings with the app lock on can only be saved with the key
// derived from the app password.
var ErrNeedsKey = errors.New("the settings are locked with the app password")

// WithoutSecrets returns the settings with sign-in and vault keys removed:
// what a locked CryptBridge knows.
func (s Settings) WithoutSecrets() Settings {
	s.PCloud = pcloud.Account{}
	s.PCloudApp = pcloud.App{}
	s.SignInAppID = ""
	s.Vault = vault.Config{}
	return s
}

func (s Settings) secrets() secrets {
	return secrets{PCloud: s.PCloud, PCloudApp: s.PCloudApp, SignInAppID: s.SignInAppID, Vault: s.Vault}
}

func (s Settings) withSecrets(sec secrets) Settings {
	s.PCloud = sec.PCloud
	s.PCloudApp = sec.PCloudApp
	s.SignInAppID = sec.SignInAppID
	s.Vault = sec.Vault
	return s
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
// installation, and the defaults are returned. With the app lock on, the
// result has no secrets; Unlock opens them.
func Load(path, defaultCacheDir string) (Settings, error) {
	stored, err := readFile(path, defaultCacheDir)
	if stored.AppLock {
		// A locked file holds no readable secrets anyway; this keeps the
		// promise even for a damaged one.
		return stored.Settings.WithoutSecrets(), err
	}
	return stored.Settings, err
}

// Unlock reads the settings and opens the secrets with the app password.
// The returned key seals later changes (see Save); wipe it when locking.
func Unlock(path, defaultCacheDir, password string) (Settings, *passlock.Key, error) {
	stored, err := readFile(path, defaultCacheDir)
	if err != nil {
		return Settings{}, nil, err
	}
	if !stored.AppLock {
		return stored.Settings, nil, errors.New("CryptBridge is not locked with an app password")
	}
	if stored.Sealed == nil {
		return Settings{}, nil, errors.New("the locked settings are damaged")
	}
	plain, key, err := passlock.Open(*stored.Sealed, password)
	if err != nil {
		return Settings{}, nil, err
	}
	var sec secrets
	if err := json.Unmarshal(plain, &sec); err != nil {
		key.Wipe()
		return Settings{}, nil, fmt.Errorf("read locked settings: %w", err)
	}
	return stored.Settings.withSecrets(sec), key, nil
}

// readFile reads and decrypts the DPAPI layer.
func readFile(path, defaultCacheDir string) (fileFormat, error) {
	stored := fileFormat{Settings: Defaults(defaultCacheDir)}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return stored, nil
	}
	if err != nil {
		return stored, fmt.Errorf("read settings: %w", err)
	}
	if !bytes.HasPrefix(data, fileHeader) {
		return stored, errors.New("settings file has an unknown format")
	}
	plain, err := unprotect(data[len(fileHeader):])
	if err != nil {
		return stored, fmt.Errorf("decrypt settings (was the file copied from another Windows account?): %w", err)
	}
	if err := json.Unmarshal(plain, &stored); err != nil {
		return fileFormat{Settings: Defaults(defaultCacheDir)}, fmt.Errorf("read settings: %w", err)
	}
	return stored, nil
}

// Save encrypts and writes the settings. With the app lock on, key must be
// the key from Unlock or passlock.NewKey: the secrets are sealed with it and
// never written readable. The old file is replaced only after the new one is
// completely on disk.
func Save(path string, s Settings, key *passlock.Key) error {
	stored := fileFormat{Settings: s}
	if s.AppLock {
		if key == nil {
			return ErrNeedsKey
		}
		secretJSON, err := json.Marshal(s.secrets())
		if err != nil {
			return fmt.Errorf("encode settings: %w", err)
		}
		sealed, err := key.Seal(secretJSON)
		clear(secretJSON)
		if err != nil {
			return fmt.Errorf("lock settings: %w", err)
		}
		stored = fileFormat{Settings: s.WithoutSecrets(), Sealed: &sealed}
	}
	plain, err := json.Marshal(stored)
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
