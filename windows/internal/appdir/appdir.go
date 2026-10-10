// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

// Package appdir knows where CryptBridge keeps its files. Everything lives
// in the user's own profile, which only this user (and administrators) can
// read.
package appdir

import (
	"errors"
	"os"
	"path/filepath"
)

const appFolder = "CryptBridge"

// Paths lists every file and folder CryptBridge uses.
type Paths struct {
	SettingsFile string // %APPDATA%\CryptBridge\settings.dat (encrypted with DPAPI)
	StateFile    string // %LOCALAPPDATA%\CryptBridge\run\rcd.json
	LogDir       string // %LOCALAPPDATA%\CryptBridge\logs
	RcloneLog    string // %LOCALAPPDATA%\CryptBridge\logs\rclone.log
	CacheDir     string // default cache: %LOCALAPPDATA%\CryptBridge\cache
	BinDir       string // downloaded rclone: %LOCALAPPDATA%\CryptBridge\bin
}

// Resolve returns the paths for the current user. Folders are created by
// Ensure, not here.
func Resolve() (Paths, error) {
	roaming := os.Getenv("APPDATA")
	local := os.Getenv("LOCALAPPDATA")
	if roaming == "" || local == "" {
		return Paths{}, errors.New("APPDATA or LOCALAPPDATA is not set")
	}
	base := filepath.Join(local, appFolder)
	logs := filepath.Join(base, "logs")
	return Paths{
		SettingsFile: filepath.Join(roaming, appFolder, "settings.dat"),
		StateFile:    filepath.Join(base, "run", "rcd.json"),
		LogDir:       logs,
		RcloneLog:    filepath.Join(logs, "rclone.log"),
		CacheDir:     filepath.Join(base, "cache"),
		BinDir:       filepath.Join(base, "bin"),
	}, nil
}

// Ensure creates the folders that must exist before CryptBridge starts
// working. The cache folder is created by rclone itself.
func (p Paths) Ensure() error {
	for _, dir := range []string{filepath.Dir(p.SettingsFile), filepath.Dir(p.StateFile), p.LogDir, p.BinDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}
