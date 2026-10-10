// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"time"
)

// Why a guardian: when the Windows web view (WebView2) fails, Wails shows a
// message and ends the whole process. On the developer's PC this hit roughly
// every 5th to 20th start of another Wails program (measured over about 215
// starts). For CryptBridge that would mean: started with Windows, crashed,
// no tray icon, no drive. So this small process starts the real program as
// a child and starts it again if it ends with an error. The drive itself is
// safe either way: rclone runs on its own and is picked up again.

const (
	// argWindow marks the child process that opens the window.
	argWindow = "--window"

	// maxRestarts within restartWindow; beyond that something is broken
	// for good and restarting would only loop.
	maxRestarts   = 5
	restartWindow = 2 * time.Minute
)

// isWindowProcess reports whether this process is the child with the window.
func isWindowProcess(args []string) bool {
	return slices.Contains(args, argWindow)
}

// runGuardian starts the window process and restarts it after a crash.
// It returns guarded=false if the child could not be started at all.
func runGuardian() (exitCode int, guarded bool) {
	self, err := os.Executable()
	if err != nil {
		return 0, false
	}
	args := append([]string{argWindow}, os.Args[1:]...)

	var restarts []time.Time
	for {
		cmd := exec.Command(self, args...)
		if err := cmd.Start(); err != nil {
			return 0, false
		}
		err := cmd.Wait()
		var exitErr *exec.ExitError
		if err == nil || !errors.As(err, &exitErr) {
			return 0, true // normal end: the user quit CryptBridge
		}

		now := time.Now()
		restarts = append(restarts, now)
		restarts = slices.DeleteFunc(restarts, func(t time.Time) bool { return now.Sub(t) > restartWindow })
		if len(restarts) > maxRestarts {
			return exitErr.ExitCode(), true
		}
		time.Sleep(time.Second)
	}
}
