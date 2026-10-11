// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/appdir"
	"github.com/burnersen/CryptBridge/windows/internal/drive"
	"github.com/burnersen/CryptBridge/windows/internal/passlock"
	"github.com/burnersen/CryptBridge/windows/internal/pcloud"
	"github.com/burnersen/CryptBridge/windows/internal/rcd"
	"github.com/burnersen/CryptBridge/windows/internal/settings"
	"github.com/burnersen/CryptBridge/windows/internal/testenv"
)

// TestLiveLifecycle plays a day in the life of CryptBridge against the real
// test vault: start with auto-connect, refuse to disconnect during an
// upload, survive a crash of the window, recover from a killed rclone, quit.
func TestLiveLifecycle(t *testing.T) {
	_, account, vaultCfg := testenv.Live(t)
	free, err := drive.FreeLetters()
	if err != nil || len(free) == 0 {
		t.Skip("no free drive letter")
	}
	letter := free[len(free)-2] // leave Z: alone if it is free
	paths := testPaths(t.TempDir())
	s := settings.Defaults(paths.CacheDir)
	s.PCloud = account
	s.Vault = vaultCfg
	s.Drive.Letter = letter
	if err := settings.Save(paths.SettingsFile, s, nil); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	t.Cleanup(func() { removeTestFiles(t) })

	// 1. Start with auto-connect. The first engine gets its own context:
	// cancelling it later stops its watching, like a crashed window would.
	firstCtx, crashFirst := context.WithCancel(ctx)
	defer crashFirst()
	first := New(paths, "test")
	if err := first.Start(firstCtx); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, first, PhaseConnected, 60*time.Second)
	snap := first.Snapshot()
	t.Logf("connected %s: fingerprint %s", letter, snap.Fingerprint)
	if snap.Fingerprint == "" {
		t.Error("no fingerprint after start")
	}
	t.Cleanup(func() { _ = first.Shutdown(context.Background(), true) })

	// 2. A fresh upload blocks a normal disconnect.
	name := fmt.Sprintf("engine-test-%d.bin", time.Now().UnixNano())
	target := letter + `:\` + name
	if err := os.WriteFile(target, make([]byte, 8<<20), 0o600); err != nil {
		t.Fatalf("write to drive: %v", err)
	}
	var pending *UploadsPendingError
	if err := first.Disconnect(ctx, false); !errors.As(err, &pending) {
		t.Fatalf("disconnect right after writing: got %v, want UploadsPendingError", err)
	}
	t.Logf("disconnect refused as expected: %v", pending)
	waitUploadsDone(t, first, 2*time.Minute)

	// 3. "Crash" of the window: the first engine stops watching, a second
	// one starts and must attach to the rclone that is still running.
	pidBefore := first.client.PID()
	crashFirst()
	second := New(paths, "test")
	var phasesMu sync.Mutex
	var phases []Phase
	second.Subscribe(func(s Snapshot) {
		phasesMu.Lock()
		phases = append(phases, s.Phase)
		phasesMu.Unlock()
	})
	if err := second.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, second, PhaseConnected, 30*time.Second)
	if second.client.PID() != pidBefore {
		t.Fatalf("second start used rclone %d, want the running %d", second.client.PID(), pidBefore)
	}

	// 4. rclone gets killed: the engine must bring the drive back by itself.
	if out, err := exec.Command("taskkill", "/F", "/PID", fmt.Sprint(pidBefore)).CombinedOutput(); err != nil {
		t.Fatalf("taskkill: %v %s", err, out)
	}
	deadline := time.Now().Add(90 * time.Second)
	for second.Snapshot().Phase != PhaseConnected || second.client == nil || second.client.PID() == pidBefore {
		if time.Now().After(deadline) {
			t.Fatalf("drive not back within 90 s; phase %q: %s", second.Snapshot().Phase, second.Snapshot().Message)
		}
		time.Sleep(250 * time.Millisecond)
	}
	phasesMu.Lock()
	sawReconnect := slices.Contains(phases, PhaseReconnecting)
	phasesMu.Unlock()
	if !sawReconnect {
		t.Error("the reconnect was never shown as phase \"reconnecting\"")
	}
	t.Logf("recovered: rclone %d replaced the killed %d", second.client.PID(), pidBefore)
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("uploaded file not visible after reconnect: %v", err)
	}

	// 5. Quit: drive and rclone are gone.
	if err := os.Remove(target); err != nil {
		t.Fatalf("delete test file: %v", err)
	}
	waitUploadsDone(t, second, time.Minute)
	if err := second.Shutdown(ctx, false); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if _, err := os.Stat(letter + `:\`); err == nil {
		t.Fatalf("drive %s: still there after shutdown", letter)
	}
}

// TestLiveSignInProblem plays the outage of 2026-10-05 against the real test
// vault: pCloud refuses the sign-in while the drive is connected, and later
// accepts it again. The refusal is simulated by giving the running rclone a
// sign-in that pCloud rejects; the stored sign-in is not touched.
func TestLiveSignInProblem(t *testing.T) {
	_, account, vaultCfg := testenv.Live(t)
	free, err := drive.FreeLetters()
	if err != nil || len(free) == 0 {
		t.Skip("no free drive letter")
	}
	letter := free[len(free)-2]
	paths := testPaths(t.TempDir())
	s := settings.Defaults(paths.CacheDir)
	s.PCloud = account
	s.Vault = vaultCfg
	s.Drive.Letter = letter
	if err := settings.Save(paths.SettingsFile, s, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	e := New(paths, "test")
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Shutdown(context.Background(), true) })
	waitPhase(t, e, PhaseConnected, 60*time.Second)

	// 1. pCloud starts refusing the sign-in while the drive is connected.
	refused := pcloud.Account{Hostname: account.Hostname, Token: `{"access_token":"refused","token_type":"bearer","expiry":"0001-01-01T00:00:00Z"}`}
	if err := pcloud.Configure(ctx, clientOf(e), pcloudRemote, refused); err != nil {
		t.Fatal(err)
	}
	askPCloudNow(e)
	waitFor(t, "sign-in warning", 15*time.Second, func() bool { return e.Snapshot().SignInProblem != "" })
	t.Logf("warning: %s", e.Snapshot().SignInProblem)

	// 2. The warning must stay, and the drive must stay connected (until
	// version 0.1.1 the next status round hid the warning after 2 s).
	for end := time.Now().Add(3 * pollEvery); time.Now().Before(end); time.Sleep(250 * time.Millisecond) {
		if snap := e.Snapshot(); snap.SignInProblem == "" || snap.Phase != PhaseConnected {
			t.Fatalf("warning %q, phase %q; want the warning to stay while connected", snap.SignInProblem, snap.Phase)
		}
	}

	// 3. A connect while pCloud refuses must not mount an empty drive, and
	// with automatic reconnects on it keeps trying in the background.
	if err := e.Disconnect(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := e.Connect(ctx); !pcloud.IsSignInProblem(err) {
		t.Fatalf("connect while refused: got %v, want a sign-in problem", err)
	}
	if _, err := os.Stat(letter + `:\`); err == nil {
		t.Fatalf("drive %s: was mounted although pCloud refuses the sign-in", letter)
	}
	waitPhase(t, e, PhaseReconnecting, 15*time.Second)

	// 4. pCloud accepts the sign-in again: the drive comes back by itself
	// and the warning goes away.
	if err := pcloud.Configure(ctx, clientOf(e), pcloudRemote, account); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, e, PhaseConnected, 90*time.Second)
	if problem := e.Snapshot().SignInProblem; problem != "" {
		t.Fatalf("warning still shown after pCloud accepted the sign-in: %s", problem)
	}
}

// TestLiveAppLock plays the app lock against the real test vault: locked
// start, wrong and right password, locking while an upload runs (the drive
// waits for it), and locking without waiting, as when signing out of
// Windows (the file stays in the cache and is uploaded after unlocking).
func TestLiveAppLock(t *testing.T) {
	_, account, vaultCfg := testenv.Live(t)
	free, err := drive.FreeLetters()
	if err != nil || len(free) == 0 {
		t.Skip("no free drive letter")
	}
	letter := free[len(free)-2]
	paths := testPaths(t.TempDir())
	const appPassword = "test app password"
	s := settings.Defaults(paths.CacheDir)
	s.PCloud = account
	s.Vault = vaultCfg
	s.Drive.Letter = letter
	s.AppLock = true
	key, err := passlock.NewKey(appPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := settings.Save(paths.SettingsFile, s, key); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	t.Cleanup(func() { removeTestFiles(t) })

	// 1. Locked start: nothing known, nothing running.
	e := New(paths, "test")
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Shutdown(context.Background(), true) })
	if snap := e.Snapshot(); !snap.Locked || snap.Phase != PhaseLocked || snap.SignedIn || snap.HasVault || clientOf(e) != nil {
		t.Fatalf("start: %+v; want locked without secrets and without rclone", snap)
	}
	if err := e.Connect(ctx); !errors.Is(err, errLocked) {
		t.Fatalf("connect while locked: got %v", err)
	}

	// 2. A wrong password makes the next try wait.
	if err := e.Unlock(ctx, "wrong password"); err == nil {
		t.Fatal("wrong password accepted")
	}
	if err := e.Unlock(ctx, appPassword); err == nil || !e.isLocked() {
		t.Fatalf("right password right after a wrong one: got %v, want a pause", err)
	}
	time.Sleep(unlockPause(1) + 100*time.Millisecond)
	if err := e.Unlock(ctx, appPassword); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, e, PhaseConnected, 60*time.Second) // "connect on start" is on

	// 3. Lock while an upload runs: the drive stays until it is done.
	name := fmt.Sprintf("engine-test-%d.bin", time.Now().UnixNano())
	if err := os.WriteFile(letter+`:\`+name, make([]byte, 8<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	meta, err := os.ReadFile(filepath.Join(paths.CacheDir, "vfsMeta", vaultRemote, name))
	t.Logf("cache marker right after writing: %s (err %v)", strings.Join(strings.Fields(string(meta)), " "), err)
	if err := e.Lock(ctx); err != nil {
		t.Fatal(err)
	}
	if snap := e.Snapshot(); !snap.Locked || snap.SignedIn || snap.HasVault {
		t.Fatalf("right after Lock: %+v; want the secrets gone at once", snap)
	}
	t.Logf("right after Lock: %q", e.Snapshot().Message)
	waitFor(t, "drive closed, rclone stopped", 3*time.Minute, func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return !e.closing && e.client == nil
	})
	if _, err := os.Stat(letter + `:\`); err == nil {
		t.Fatalf("drive %s: still there after locking", letter)
	}
	if _, err := rcd.Attach(ctx, paths.StateFile); err == nil {
		t.Fatal("rclone still running after locking")
	}
	for _, path := range []string{filepath.Join(paths.CacheDir, "vfs", vaultRemote), paths.RcloneLog} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s still there after locking", path)
		}
	}

	// 4. Unlock: the drive comes back with the uploaded file.
	if err := e.Unlock(ctx, appPassword); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, e, PhaseConnected, 60*time.Second)
	if _, err := os.Stat(letter + `:\` + name); err != nil {
		t.Fatalf("file uploaded before locking is missing: %v", err)
	}

	// 5. Lock without waiting (signing out): the new file stays in the
	// cache, nothing is deleted, and it is uploaded after unlocking.
	late := fmt.Sprintf("engine-test-%d.bin", time.Now().UnixNano())
	if err := os.WriteFile(letter+`:\`+late, make([]byte, 8<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.LockNow(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("after LockNow: %q", e.Snapshot().Message)
	if _, err := os.Stat(filepath.Join(paths.CacheDir, "vfs", vaultRemote, late)); err != nil {
		t.Fatalf("waiting file was not kept in the cache: %v", err)
	}
	if err := e.Unlock(ctx, appPassword); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, e, PhaseConnected, 60*time.Second)
	waitUploadsDone(t, e, 2*time.Minute)
	out, err := exec.Command("rclone", "--config", os.Getenv("CRYPTBRIDGE_DEV_RCLONE_CONF"), "lsf", "cbvault:", "--include", late).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != late {
		t.Fatalf("file held back by LockNow is not in pCloud after unlocking: %q %v", out, err)
	}
	for _, n := range []string{name, late} {
		if err := os.Remove(letter + `:\` + n); err != nil {
			t.Errorf("remove %s: %v", n, err)
		}
	}
	waitUploadsDone(t, e, time.Minute)
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
	e.actionMu.Lock()
	e.lastUsage = time.Time{}
	e.actionMu.Unlock()
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

// removeTestFiles deletes what this test may have left in the test vault
// when it stopped half-way.
func removeTestFiles(t *testing.T) {
	conf := os.Getenv("CRYPTBRIDGE_DEV_RCLONE_CONF")
	out, err := exec.Command("rclone", "--config", conf, "delete", "cbvault:", "--include", "engine-test-*.bin").CombinedOutput()
	if err != nil {
		t.Logf("cleaning the test vault failed: %v %s", err, out)
	}
}

func waitPhase(t *testing.T, e *Engine, want Phase, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if snap := e.Snapshot(); snap.Phase == want {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	snap := e.Snapshot()
	t.Fatalf("phase %q not reached within %s; now %q: %s", want, timeout, snap.Phase, snap.Message)
}

func waitUploadsDone(t *testing.T, e *Engine, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	// The status refreshes every few seconds; give it a moment first.
	time.Sleep(3 * time.Second)
	for time.Now().Before(deadline) {
		if e.Snapshot().Drive.PendingUploads() == 0 {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("uploads not finished within %s", timeout)
}
