// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// The per-user autostart list of Windows. No administrator rights needed.
const (
	runKeyPath   = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValueName = "CryptBridge"
)

// autostartCommand is what Windows runs at sign-in: this program, hidden.
func autostartCommand() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`"%s" %s`, self, argBackground), nil
}

// autostartEnabled reports whether Windows starts this very program at
// sign-in (an entry of an older copy elsewhere does not count).
func autostartEnabled() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	value, _, err := key.GetStringValue(runValueName)
	if err != nil {
		return false
	}
	want, err := autostartCommand()
	return err == nil && strings.EqualFold(value, want)
}

// setAutostart adds or removes the autostart entry.
func setAutostart(enabled bool) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open autostart list: %w", err)
	}
	defer key.Close()
	if !enabled {
		if err := key.DeleteValue(runValueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return fmt.Errorf("remove autostart entry: %w", err)
		}
		return nil
	}
	command, err := autostartCommand()
	if err != nil {
		return err
	}
	if err := key.SetStringValue(runValueName, command); err != nil {
		return fmt.Errorf("add autostart entry: %w", err)
	}
	return nil
}
