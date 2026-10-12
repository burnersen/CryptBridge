// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

//go:build windows

package cloud

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// hiddenWindow keeps the short-lived rclone process from opening a console.
func hiddenWindow() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
