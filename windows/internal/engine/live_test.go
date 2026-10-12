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
	"github.com/burnersen/CryptBridge/windows/internal/cloud"
	"github.com/burnersen/CryptBridge/windows/internal/drive"
	"github.com/burnersen/CryptBridge/windows/internal/passlock"
	"github.com/burnersen/CryptBridge/windows/internal/pcloud"
	"github.com/burnersen/CryptBridge/windows/internal/rcd"
	"github.com/burnersen/CryptBridge/windows/internal/settings"
	"github.com/burnersen/CryptBridge/windows/internal/testenv"
	"github.com/burnersen/CryptBridge/windows/internal/vault"
)

// These tests run against the developer's pCloud test vault; they need
// CRYPTBRIDGE_DEV_RCLONE_CONF (see testenv).

// testVaultID is the ID of the test vault: the one taken over from 0.3, so
// that its remote is "cbvault" as in the developer's rclone.conf.
const testVaultID = settings.MigratedVaultID

// liveSettings writes settings with the pCloud test vault on a free drive
// letter and returns the test profile and the letter.
func liveSettings(t *testing.T, appLock *passlock.Key) (appdir.Paths, string) {
	t.Helper()
	return liveSettingsWith(t, appLock, nil)
}

// liveSettingsWith is liveSettings with the vault settings changed by change
// (if not nil), for example to wrong keys.
func liveSettingsWith(t *testing.T, appLock *passlock.Key, change func(*vault.Config)) (appdir.Paths, string) {
	t.Helper()
	_, account, vaultCfg := testenv.Live(t)
	if change != nil {
		change(&vaultCfg)
	}
	free, err := drive.FreeLetters()
	if err != nil || len(free) < 2 {
		t.Skip("no free drive letter")
	}
	letter := free[len(free)-2] // leave Z: alone if it is free
	paths := testPaths(t.TempDir())
	s := settings.Defaults(paths.CacheDir)
	s.Accounts = []settings.Account{{
		ID: settings.MigratedAccountID, Service: cloud.PCloud, Name: "pCloud",
		Remote: map[string]string{"type": cloud.PCloud, "hostname": account.Hostname, "token": account.Token},
	}}
	s.Vaults = []settings.Vault{{ID: testVaultID, Name: "Test vault", Kind: settings.KindCloud, Account: settings.MigratedAccountID,
		Letter: letter, AutoConnect: true, Config: vaultCfg}}
	s.AppLock = appLock != nil
	if err := settings.Save(paths.SettingsFile, s, appLock); err != nil {
		t.Fatal(err)
	}
	return paths, letter
}

// TestLiveLifecycle plays a day in the life of CryptBridge against the real
// test vault: start with auto-connect, refuse to disconnect during an
// upload, survive a crash of the window, recover from a killed rclone, quit.
func TestLiveLifecycle(t *testing.T) {
	paths, letter := liveSettings(t, nil)
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
	waitState(t, first, testVaultID, StateConnected, 60*time.Second)
	snap := vaultOf(first, testVaultID)
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
	if err := first.Disconnect(ctx, testVaultID, false); !errors.As(err, &pending) {
		t.Fatalf("disconnect right after writing: got %v, want UploadsPendingError", err)
	}
	t.Logf("disconnect refused as expected: %v", pending)
	waitUploadsDone(t, first, testVaultID, 2*time.Minute)

	// 3. "Crash" of the window: the first engine stops watching, a second
	// one starts and must attach to the rclone that is still running.
	pidBefore := clientOf(first).PID()
	crashFirst()
	second := New(paths, "test")
	var statesMu sync.Mutex
	var states []VaultState
	second.Subscribe(func(s Snapshot) {
		statesMu.Lock()
		for _, v := range s.Vaults {
			states = append(states, v.State)
		}
		statesMu.Unlock()
	})
	if err := second.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitState(t, second, testVaultID, StateConnected, 30*time.Second)
	if clientOf(second).PID() != pidBefore {
		t.Fatalf("second start used rclone %d, want the running %d", clientOf(second).PID(), pidBefore)
	}

	// 4. rclone gets killed: the engine must bring the drive back by itself.
	if out, err := exec.Command("taskkill", "/F", "/PID", fmt.Sprint(pidBefore)).CombinedOutput(); err != nil {
		t.Fatalf("taskkill: %v %s", err, out)
	}
	waitFor(t, "drive back with a new rclone", 90*time.Second, func() bool {
		client := clientOf(second)
		return vaultOf(second, testVaultID).State == StateConnected && client != nil && client.PID() != pidBefore
	})
	statesMu.Lock()
	sawReconnect := slices.Contains(states, StateReconnecting)
	statesMu.Unlock()
	if !sawReconnect {
		t.Error("the reconnect was never shown as state \"reconnecting\"")
	}
	t.Logf("recovered: rclone %d replaced the killed %d", clientOf(second).PID(), pidBefore)
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("uploaded file not visible after reconnect: %v", err)
	}

	// 5. Quit: drive and rclone are gone.
	if err := os.Remove(target); err != nil {
		t.Fatalf("delete test file: %v", err)
	}
	waitUploadsDone(t, second, testVaultID, time.Minute)
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
	_, account, _ := testenv.Live(t)
	paths, letter := liveSettings(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	e := New(paths, "test")
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Shutdown(context.Background(), true) })
	waitState(t, e, testVaultID, StateConnected, 60*time.Second)

	// 1. pCloud starts refusing the sign-in while the drive is connected.
	refused := pcloud.Account{Hostname: account.Hostname, Token: `{"access_token":"refused","token_type":"bearer","expiry":"0001-01-01T00:00:00Z"}`}
	if err := pcloud.Configure(ctx, clientOf(e), remoteName(settings.MigratedAccountID), refused); err != nil {
		t.Fatal(err)
	}
	askPCloudNow(e)
	waitFor(t, "sign-in warning", 15*time.Second, func() bool { return pcloudProblem(e) != "" })
	t.Logf("warning: %s", pcloudProblem(e))

	// 2. The warning must stay, and the drive must stay connected (until
	// version 0.1.1 the next status round hid the warning after 2 s).
	for end := time.Now().Add(3 * pollEvery); time.Now().Before(end); time.Sleep(250 * time.Millisecond) {
		if problem := pcloudProblem(e); problem == "" || vaultOf(e, testVaultID).State != StateConnected {
			t.Fatalf("warning %q, state %q; want the warning to stay while connected", problem, vaultOf(e, testVaultID).State)
		}
	}

	// 3. A connect while pCloud refuses must not mount an empty drive, and
	// with automatic reconnects on it keeps trying in the background.
	if err := e.Disconnect(ctx, testVaultID, true); err != nil {
		t.Fatal(err)
	}
	if err := e.Connect(ctx, testVaultID); !cloud.IsSignInProblem(err) {
		t.Fatalf("connect while refused: got %v, want a sign-in problem", err)
	}
	if _, err := os.Stat(letter + `:\`); err == nil {
		t.Fatalf("drive %s: was mounted although pCloud refuses the sign-in", letter)
	}
	waitState(t, e, testVaultID, StateReconnecting, 15*time.Second)

	// 4. pCloud accepts the sign-in again: the drive comes back by itself
	// and the warning goes away.
	if err := pcloud.Configure(ctx, clientOf(e), remoteName(settings.MigratedAccountID), account); err != nil {
		t.Fatal(err)
	}
	waitState(t, e, testVaultID, StateConnected, 90*time.Second)
	if problem := pcloudProblem(e); problem != "" {
		t.Fatalf("warning still shown after pCloud accepted the sign-in: %s", problem)
	}
}

// TestLivePCloudUnreachable: a pCloud drive connects only after its folder
// proved to hold the vault (since 0.6.1). While pCloud cannot be reached,
// nothing is connected, and the drive comes by itself once pCloud answers.
// "Cannot be reached" is simulated with a host name that does not exist.
func TestLivePCloudUnreachable(t *testing.T) {
	_, account, _ := testenv.Live(t)
	paths, letter := liveSettings(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	e := New(paths, "test")
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Shutdown(context.Background(), true) })
	waitState(t, e, testVaultID, StateConnected, 60*time.Second)
	if err := e.Disconnect(ctx, testVaultID, true); err != nil {
		t.Fatal(err)
	}

	unreachable := pcloud.Account{Hostname: "eapi.pcloud.invalid", Token: account.Token}
	if err := pcloud.Configure(ctx, clientOf(e), remoteName(settings.MigratedAccountID), unreachable); err != nil {
		t.Fatal(err)
	}
	var unchecked *errNotChecked
	if err := e.Connect(ctx, testVaultID); !errors.As(err, &unchecked) {
		t.Fatalf("connect while pCloud cannot be reached: got %v, want errNotChecked", err)
	}
	if _, err := os.Stat(letter + `:\`); err == nil {
		t.Fatalf("drive %s: was mounted without checking the vault", letter)
	}
	waitState(t, e, testVaultID, StateReconnecting, 15*time.Second)
	t.Logf("while unreachable: %s", vaultOf(e, testVaultID).Message)

	if err := pcloud.Configure(ctx, clientOf(e), remoteName(settings.MigratedAccountID), account); err != nil {
		t.Fatal(err)
	}
	waitState(t, e, testVaultID, StateConnected, 90*time.Second)
}

// TestLivePCloudWrongFolder: wrong keys or a renamed vault folder stop a
// pCloud drive for good, without writing anything (since 0.6.1).
func TestLivePCloudWrongFolder(t *testing.T) {
	missing := fmt.Sprintf("-renamed-%d", time.Now().UnixNano())
	cases := []struct {
		what    string
		change  func(*vault.Config)
		message string
	}{
		{"wrong password", func(c *vault.Config) { c.Password += " wrong" }, "do not open the folder"},
		{"renamed folder", func(c *vault.Config) { c.Path += missing }, "is empty or gone"},
	}
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			paths, letter := liveSettingsWith(t, nil, c.change)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			e := New(paths, "test")
			if err := e.Start(ctx); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = e.Shutdown(context.Background(), true) })

			waitState(t, e, testVaultID, StateError, 60*time.Second)
			if message := vaultOf(e, testVaultID).Message; !strings.Contains(message, c.message) {
				t.Fatalf("message %q; want it to contain %q", message, c.message)
			}
			// It must not keep trying: the folder will not fix itself.
			time.Sleep(3 * pollEvery)
			if state := vaultOf(e, testVaultID).State; state != StateError {
				t.Fatalf("state %q after a while; want it to stay %q", state, StateError)
			}
			if _, err := os.Stat(letter + `:\`); err == nil {
				t.Fatalf("drive %s: was mounted although the folder does not hold the vault", letter)
			}
		})
	}

	// Nothing may have been written where the renamed folder was.
	_, _, cfg := testenv.Live(t)
	conf := os.Getenv("CRYPTBRIDGE_DEV_RCLONE_CONF")
	out, err := exec.Command("rclone", "--config", conf, "lsf", "cbpcloud:"+cfg.NormalisedPath()+missing).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "directory not found") {
		t.Fatalf("the missing folder exists now (err %v): %s", err, out)
	}
}

// TestLiveAppLock plays the app lock against the real test vault: locked
// start, wrong and right password, locking while an upload runs (the drive
// waits for it), and locking without waiting, as when signing out of
// Windows (the file stays in the cache and is uploaded after unlocking).
func TestLiveAppLock(t *testing.T) {
	const appPassword = "test app password"
	key, err := passlock.NewKey(appPassword)
	if err != nil {
		t.Fatal(err)
	}
	paths, letter := liveSettings(t, key)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	t.Cleanup(func() { removeTestFiles(t) })
	cacheOfVault := filepath.Join(paths.CacheDir, "vfs", remoteName(testVaultID))

	// 1. Locked start: nothing known, nothing running.
	e := New(paths, "test")
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Shutdown(context.Background(), true) })
	if snap := e.Snapshot(); !snap.Locked || snap.Phase != PhaseLocked || accountOf(e, settings.MigratedAccountID).SignedIn || vaultOf(e, testVaultID).HasKeys || clientOf(e) != nil {
		t.Fatalf("start: %+v; want locked without secrets and without rclone", snap)
	}
	if got := e.Snapshot().UnlockConnects; len(got) != 1 || got[0] != letter {
		t.Fatalf("drives to connect after unlocking: %v, want [%s]", got, letter)
	}
	if err := e.Connect(ctx, testVaultID); !errors.Is(err, errLocked) {
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
	waitState(t, e, testVaultID, StateConnected, 60*time.Second) // "connect on start" is on

	// 3. Lock while an upload runs: the drive stays until it is done.
	name := fmt.Sprintf("engine-test-%d.bin", time.Now().UnixNano())
	if err := os.WriteFile(letter+`:\`+name, make([]byte, 8<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.Lock(ctx); err != nil {
		t.Fatal(err)
	}
	if snap := e.Snapshot(); !snap.Locked || accountOf(e, settings.MigratedAccountID).SignedIn || vaultOf(e, testVaultID).HasKeys {
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
	for _, path := range []string{cacheOfVault, paths.RcloneLog} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s still there after locking", path)
		}
	}

	// 4. Unlock: the drive comes back with the uploaded file.
	if err := e.Unlock(ctx, appPassword); err != nil {
		t.Fatal(err)
	}
	waitState(t, e, testVaultID, StateConnected, 60*time.Second)
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
	if _, err := os.Stat(filepath.Join(cacheOfVault, late)); err != nil {
		t.Fatalf("waiting file was not kept in the cache: %v", err)
	}
	if err := e.Unlock(ctx, appPassword); err != nil {
		t.Fatal(err)
	}
	waitState(t, e, testVaultID, StateConnected, 60*time.Second)
	waitUploadsDone(t, e, testVaultID, 2*time.Minute)
	out, err := exec.Command("rclone", "--config", os.Getenv("CRYPTBRIDGE_DEV_RCLONE_CONF"), "lsf", "cbvault:", "--include", late).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != late {
		t.Fatalf("file held back by LockNow is not in pCloud after unlocking: %q %v", out, err)
	}
	for _, n := range []string{name, late} {
		if err := os.Remove(letter + `:\` + n); err != nil {
			t.Errorf("remove %s: %v", n, err)
		}
	}
	waitUploadsDone(t, e, testVaultID, time.Minute)
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
