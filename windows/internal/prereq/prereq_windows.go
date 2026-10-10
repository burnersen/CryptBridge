// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

//go:build windows

package prereq

import (
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// winFspKeys are where the WinFsp installer records its folder. The 32-bit
// view is the usual one (measured: HKLM\SOFTWARE\WOW6432Node\WinFsp).
var winFspKeys = []string{`SOFTWARE\WOW6432Node\WinFsp`, `SOFTWARE\WinFsp`}

// WinFspInstalled reports whether WinFsp is installed and returns its
// folder. The registry entry alone is not trusted: the driver DLL must exist.
func WinFspInstalled() (string, bool) {
	for _, keyPath := range winFspKeys {
		key, err := registry.OpenKey(registry.LOCAL_MACHINE, keyPath, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		dir, _, err := key.GetStringValue("InstallDir")
		key.Close()
		if err != nil || dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "bin", "winfsp-x64.dll")); err == nil {
			return dir, true
		}
	}
	return "", false
}

// hiddenWindow keeps short-lived helper processes from opening a console.
func hiddenWindow() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
