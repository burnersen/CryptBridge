// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package engine

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/appdir"
	"github.com/burnersen/CryptBridge/windows/internal/rcd"
	"github.com/burnersen/CryptBridge/windows/internal/settings"
	"github.com/burnersen/CryptBridge/windows/internal/testrclone"
)

// testPaths puts every file of a test engine below root.
func testPaths(root string) appdir.Paths {
	return appdir.Paths{
		SettingsFile: filepath.Join(root, "roaming", "settings.dat"),
		StateFile:    filepath.Join(root, "local", "run", "rcd.json"),
		LogDir:       filepath.Join(root, "local", "logs"),
		RcloneLog:    filepath.Join(root, "local", "logs", "rclone.log"),
		CacheDir:     filepath.Join(root, "local", "cache"),
		BinDir:       filepath.Join(root, "local", "bin"),
	}
}

// useTestRclone copies the rclone for tests (exactly the pinned version, see
// testrclone) into the test profile's own folder, where the engine looks
// first, so that the engine runs exactly that rclone. Without it the test
// is skipped.
func useTestRclone(t *testing.T, paths appdir.Paths) {
	t.Helper()
	exe := testrclone.Path(t)
	if err := os.MkdirAll(paths.BinDir, 0o700); err != nil {
		t.Fatal(err)
	}
	src, err := os.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.Create(filepath.Join(paths.BinDir, "rclone.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
}

// clientOf returns the engine's current rclone client.
func clientOf(e *Engine) *rcd.Client {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.client
}

// askPCloudNow makes the next status round ask pCloud right away instead of
// waiting for the usual interval.
func askPCloudNow(e *Engine) {
	e.mu.Lock()
	e.accountRuntimeLocked(settings.MigratedAccountID).lastAsked = time.Time{}
	e.mu.Unlock()
}

// accountOf returns the snapshot of one account.
func accountOf(e *Engine, id string) AccountSnapshot {
	for _, a := range e.Snapshot().Accounts {
		if a.ID == id {
			return a
		}
	}
	return AccountSnapshot{}
}

// pcloudProblem is the sign-in warning of the test's pCloud account.
func pcloudProblem(e *Engine) string {
	return accountOf(e, settings.MigratedAccountID).SignInProblem
}

func waitFor(t *testing.T, what string, timeout time.Duration, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		if done() {
			return
		}
	}
	t.Fatalf("%s not seen within %s", what, timeout)
}

// vaultOf returns the snapshot of one vault.
func vaultOf(e *Engine, id string) VaultSnapshot {
	for _, v := range e.Snapshot().Vaults {
		if v.ID == id {
			return v
		}
	}
	return VaultSnapshot{}
}

func waitPhase(t *testing.T, e *Engine, want Phase, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if e.Snapshot().Phase == want {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	snap := e.Snapshot()
	t.Fatalf("phase %q not reached within %s; now %q: %s", want, timeout, snap.Phase, snap.Message)
}

func waitState(t *testing.T, e *Engine, id string, want VaultState, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if vaultOf(e, id).State == want {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	v := vaultOf(e, id)
	t.Fatalf("drive %s: state %q not reached within %s; now %q: %s", v.Letter, want, timeout, v.State, v.Message)
}

func waitUploadsDone(t *testing.T, e *Engine, id string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	// The status refreshes every few seconds; give it a moment first.
	time.Sleep(3 * time.Second)
	for time.Now().Before(deadline) {
		if vaultOf(e, id).Drive.PendingUploads() == 0 {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("uploads not finished within %s", timeout)
}
