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
	"sync"
	"testing"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/appdir"
	"github.com/burnersen/CryptBridge/windows/internal/drive"
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
	root := t.TempDir()
	paths := appdir.Paths{
		SettingsFile: filepath.Join(root, "roaming", "settings.dat"),
		StateFile:    filepath.Join(root, "local", "run", "rcd.json"),
		LogDir:       filepath.Join(root, "local", "logs"),
		RcloneLog:    filepath.Join(root, "local", "logs", "rclone.log"),
		CacheDir:     filepath.Join(root, "local", "cache"),
		BinDir:       filepath.Join(root, "local", "bin"),
	}
	s := settings.Defaults(paths.CacheDir)
	s.PCloud = account
	s.Vault = vaultCfg
	s.Drive.Letter = letter
	if err := settings.Save(paths.SettingsFile, s); err != nil {
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
