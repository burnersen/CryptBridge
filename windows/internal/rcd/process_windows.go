// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

//go:build windows

package rcd

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// stillActive is the exit code Windows reports for a process that has not
// ended yet (STILL_ACTIVE).
const stillActive = 259

// hiddenDetachedProcess returns the start attributes for rclone: no console
// window, and its own process group so that nothing aimed at CryptBridge's
// group reaches it. rclone is stopped through its API, never by a signal.
func hiddenDetachedProcess() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP,
	}
}

// processRunning reports whether a process with this ID still runs.
func processRunning(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)

	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	return code == stillActive
}
