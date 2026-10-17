// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/drive"
	"github.com/burnersen/CryptBridge/windows/internal/prereq"
	"github.com/burnersen/CryptBridge/windows/internal/settings"
	"github.com/burnersen/CryptBridge/windows/internal/vault"
)

// These tests run the real engine with real rclone, WinFsp and drive
// letters on folders of this PC. They need no pCloud and no test vault.

// localEngine starts an engine in a test profile, or skips the test when
// this PC cannot run it.
func localEngine(t *testing.T) (*Engine, context.Context, []string) {
	t.Helper()
	paths := testPaths(t.TempDir())
	useTestRclone(t, paths)
	if _, ok := prereq.WinFspInstalled(); !ok {
		t.Skip("WinFsp is not installed")
	}
	free, err := drive.FreeLetters()
	if err != nil || len(free) < 4 {
		t.Skip("not enough free drive letters")
	}
	letters := []string{free[len(free)-2], free[len(free)-3]} // leave Z: alone if it is free

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	e := New(paths, "test")
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Shutdown(context.Background(), true) })
	waitPhase(t, e, PhaseSetup, 30*time.Second)
	return e, ctx, letters
}

func newLocal(name, letter, folder, password string, create bool) NewVault {
	cfg := vault.NewDefault(folder)
	cfg.Password = password
	return NewVault{Kind: settings.KindLocal, Name: name, Letter: letter, AutoConnect: true, Config: cfg, Create: create}
}

// countFiles counts the files below dir.
func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dir, func(_ string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			n++
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// checkFileInVault reports whether the vault holds its check file. It asks
// rclone, as the drive does not show the file.
func checkFileInVault(t *testing.T, e *Engine, id string) bool {
	t.Helper()
	var answer struct {
		Item *struct{ Name string } `json:"item"`
	}
	request := map[string]any{"fs": remoteName(id) + ":", "remote": vault.CheckFileName}
	if err := clientOf(e).Call(t.Context(), "operations/stat", request, &answer); err != nil {
		t.Fatalf("look for the check file: %v", err)
	}
	return answer.Item != nil
}

// TestLocalVaultSafety plays everything that must never damage a vault:
// adding twice, nesting, wrong keys, another folder under the vault's name,
// and removing — and checks that nothing was written where it must not be.
func TestLocalVaultSafety(t *testing.T) {
	e, ctx, letters := localEngine(t)
	root := t.TempDir()
	folderA := filepath.Join(root, "A")
	folderB := filepath.Join(root, "B")

	checkA, err := e.AddVault(ctx, newLocal("Private", letters[0], folderA, "password A", true))
	if err != nil {
		t.Fatal(err)
	}
	if checkA.VaultID == "" || checkA.Fingerprint == "" || countFiles(t, folderA) != 1 {
		t.Fatalf("new vault: %+v, %d file(s) in the folder; want an ID, a check code and the check file", checkA, countFiles(t, folderA))
	}
	if e.Snapshot().Phase != PhaseReady {
		t.Fatalf("phase %q after adding a vault, want ready", e.Snapshot().Phase)
	}

	// Never a second vault on the same files.
	for name, req := range map[string]NewVault{
		"same folder":       newLocal("Again", letters[1], folderA, "password A", false),
		"same folder, case": newLocal("Again", letters[1], filepath.Join(root, "a"), "password A", false),
		"folder inside":     newLocal("Inner", letters[1], filepath.Join(folderA, "Inner"), "x", true),
		"folder around":     newLocal("Outer", letters[1], root, "x", false),
		"name taken":        newLocal("private", letters[1], folderB, "x", true),
		"letter taken":      newLocal("Other", letters[0], folderB, "x", true),
		"base64 on Windows": func() NewVault {
			r := newLocal("B64", letters[1], folderB, "x", true)
			r.Config.FilenameEncoding = vault.EncodingBase64
			return r
		}(),
	} {
		if _, err := e.AddVault(ctx, req); err == nil {
			t.Fatalf("%s: added", name)
		}
	}
	if n := countFiles(t, folderA); n != 1 {
		t.Fatalf("%d files in vault A after the refused attempts, want 1", n)
	}

	// A vault that CryptBridge does not know: no new vault inside it, and
	// only the right keys open it.
	checkB, err := e.AddVault(ctx, newLocal("Second", letters[1], folderB, "password B", true))
	if err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"Photos", "Letters", "Music"} {
		if err := clientOf(e).Call(ctx, "operations/mkdir", map[string]any{"fs": "cb" + checkB.VaultID + ":", "remote": sub}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.RemoveVault(ctx, checkB.VaultID, "not the name"); err == nil {
		t.Fatal("removed without the right name")
	}
	if err := e.RemoveVault(ctx, checkB.VaultID, " second "); err != nil {
		t.Fatal(err)
	}
	if n := countFiles(t, folderB); n != 1 {
		t.Fatalf("removing vault B changed its folder: %d file(s), want the check file", n)
	}
	if _, err := e.AddVault(ctx, newLocal("Nested", letters[1], filepath.Join(folderB, "Inner"), "x", true)); err == nil {
		t.Fatal("a new vault inside an unknown vault was added")
	}
	if _, err := e.AddVault(ctx, newLocal("Second", letters[1], folderB, "wrong password", false)); err == nil {
		t.Fatal("vault B opened with wrong keys")
	}
	if _, err := e.AddVault(ctx, newLocal("Second", letters[1], folderB, "password B", true)); err == nil {
		t.Fatal("a new vault was created over vault B")
	}
	again, err := e.AddVault(ctx, newLocal("Second", letters[1], folderB, "password B", false))
	if err != nil {
		t.Fatal(err)
	}
	if again.VaultID != checkB.VaultID || again.Fingerprint != checkB.Fingerprint {
		t.Fatalf("added again: ID %q check code %q; want the old %q %q", again.VaultID, again.Fingerprint, checkB.VaultID, checkB.Fingerprint)
	}

	// Another folder under the vault's name (like another USB drive with the
	// same letter): the drive must not connect, and nothing is written.
	if err := os.Rename(folderA, folderA+"-away"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(folderB, folderA); err != nil {
		t.Fatal(err)
	}
	var wrong *errWrongFolder
	if err := e.Connect(ctx, checkA.VaultID); !errors.As(err, &wrong) {
		t.Fatalf("connect to vault B's folder with vault A's keys: got %v, want errWrongFolder", err)
	}
	if _, err := os.Stat(letters[0] + `:\`); err == nil {
		t.Fatalf("drive %s: was connected to the wrong folder", letters[0])
	}
	if v := vaultOf(e, checkA.VaultID); v.State != StateError {
		t.Fatalf("state %q after the refused connect, want error", v.State)
	}
	if err := os.Rename(folderA, folderB); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(folderA, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := e.Connect(ctx, checkA.VaultID); !errors.As(err, &wrong) || !wrong.empty {
		t.Fatalf("connect to an empty folder in the vault's place: got %v, want errWrongFolder (empty)", err)
	}
	if n := countFiles(t, folderA); n != 0 {
		t.Fatalf("%d file(s) written into the empty folder, want none", n)
	}
	if err := os.Remove(folderA); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(folderA+"-away", folderA); err != nil {
		t.Fatal(err)
	}
	if err := e.Connect(ctx, checkA.VaultID); err != nil {
		t.Fatalf("connect after putting the folder back: %v", err)
	}
	waitState(t, e, checkA.VaultID, StateConnected, 30*time.Second)

	// A copy of the vault in its place (like a backup USB drive): same keys,
	// but not the same folder. It is used only when the user says so.
	if err := e.Disconnect(ctx, checkA.VaultID, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(folderA, folderA+"-original"); err != nil {
		t.Fatal(err)
	}
	copyDir(t, folderA+"-original", folderA)
	var other *errOtherFolder
	if err := e.Connect(ctx, checkA.VaultID); !errors.As(err, &other) {
		t.Fatalf("connect to a copy of the vault: got %v, want errOtherFolder", err)
	}
	if v := vaultOf(e, checkA.VaultID); v.State != StateError || !v.OtherFolder || e.wants(checkA.VaultID) {
		t.Fatalf("after finding a copy: %+v; want an error offering to use the folder", v)
	}
	if _, err := os.Stat(letters[0] + `:\`); err == nil {
		t.Fatalf("drive %s: was connected to the copy", letters[0])
	}
	if err := e.UseFolder(ctx, checkA.VaultID); err != nil {
		t.Fatalf("use the copy on purpose: %v", err)
	}
	waitState(t, e, checkA.VaultID, StateConnected, 30*time.Second)
	if v := vaultOf(e, checkA.VaultID); v.OtherFolder {
		t.Fatal("still offering to use the folder after using it")
	}
	// From now on the copy is the vault's folder, and the original is the
	// other one.
	if err := e.Disconnect(ctx, checkA.VaultID, false); err != nil {
		t.Fatal(err)
	}
	if err := e.Connect(ctx, checkA.VaultID); err != nil {
		t.Fatalf("connect to the folder in use: %v", err)
	}
	if err := e.Disconnect(ctx, checkA.VaultID, false); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(folderA); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(folderA+"-original", folderA); err != nil {
		t.Fatal(err)
	}
	if err := e.Connect(ctx, checkA.VaultID); !errors.As(err, &other) {
		t.Fatalf("connect to the original after using the copy: got %v, want errOtherFolder", err)
	}
}

// copyDir copies the files of a folder tree.
func copyDir(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestLocalVaultDrives plays the daily use of local vaults: two drives at
// once, uploads, the size, a USB drive pulled without disconnecting and
// plugged in again, and a folder replaced while connected.
func TestLocalVaultDrives(t *testing.T) {
	e, ctx, letters := localEngine(t)
	root := t.TempDir()
	folderA := filepath.Join(root, "A")
	folderB := filepath.Join(root, "B")
	a, err := e.AddVault(ctx, newLocal("Private", letters[0], folderA, "password A", true))
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.AddVault(ctx, newLocal("Work", letters[1], folderB, "password B", true))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ConnectAll(ctx); err != nil {
		t.Fatal(err)
	}
	waitState(t, e, a.VaultID, StateConnected, 30*time.Second)
	waitState(t, e, b.VaultID, StateConnected, 30*time.Second)

	// A fresh write blocks a normal disconnect, on this drive only.
	file := letters[0] + `:\hello.txt`
	if err := os.WriteFile(file, []byte("hello from the test"), 0o600); err != nil {
		t.Fatal(err)
	}
	var pending *UploadsPendingError
	if err := e.Disconnect(ctx, a.VaultID, false); !errors.As(err, &pending) {
		t.Fatalf("disconnect right after writing: got %v, want UploadsPendingError", err)
	}
	waitUploadsDone(t, e, a.VaultID, time.Minute)
	if n := countFiles(t, folderA); n != 2 {
		t.Fatalf("%d file(s) in folder A, want the check file and hello.txt", n)
	}
	if n := countFiles(t, folderB); n != 1 {
		t.Fatalf("drive A wrote into folder B: %d file(s)", n)
	}

	// The size of a connected local vault is counted by itself.
	waitFor(t, "size of vault A", 30*time.Second, func() bool {
		size := vaultOf(e, a.VaultID).Size
		return size != nil && size.Bytes > 0
	})

	// The USB drive is pulled: the drive closes and waits, and comes back
	// with the folder.
	if err := os.Rename(folderA, folderA+"-unplugged"); err != nil {
		t.Fatal(err)
	}
	waitState(t, e, a.VaultID, StateWaiting, 15*time.Second)
	if _, err := os.Stat(letters[0] + `:\`); err == nil {
		t.Fatalf("drive %s: still there without its folder", letters[0])
	}
	if _, err := os.Stat(folderA); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the missing folder was created anew")
	}
	if v := vaultOf(e, b.VaultID); v.State != StateConnected {
		t.Fatalf("the other drive went %q", v.State)
	}
	if err := os.Rename(folderA+"-unplugged", folderA); err != nil {
		t.Fatal(err)
	}
	waitState(t, e, a.VaultID, StateConnected, 30*time.Second)
	if content, err := os.ReadFile(file); err != nil || string(content) != "hello from the test" {
		t.Fatalf("file after plugging in again: %q %v", content, err)
	}

	// The check file is in the vault, but the drive does not show it. Once it
	// is deleted from the vault (through rclone, as the drive cannot reach
	// it), the next connect, which finds the keys right by the other names,
	// puts it back.
	if !checkFileInVault(t, e, a.VaultID) {
		t.Fatal("the vault has no check file")
	}
	if _, err := os.Stat(letters[0] + `:\` + vault.CheckFileName); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the drive shows the check file: %v", err)
	}
	checkFile := map[string]any{"fs": remoteName(a.VaultID) + ":", "remote": vault.CheckFileName}
	if err := clientOf(e).Call(ctx, "operations/deletefile", checkFile, nil); err != nil {
		t.Fatalf("delete the check file: %v", err)
	}
	if checkFileInVault(t, e, a.VaultID) {
		t.Fatal("the check file is still there after deleting it")
	}
	waitUploadsDone(t, e, a.VaultID, time.Minute)
	if err := e.Disconnect(ctx, a.VaultID, false); err != nil {
		t.Fatal(err)
	}
	if err := e.Connect(ctx, a.VaultID); err != nil {
		t.Fatal(err)
	}
	if !checkFileInVault(t, e, a.VaultID) {
		t.Fatal("check file not restored by connecting")
	}

	// The folder is replaced while connected: the drive closes and does not
	// come back by itself.
	if err := os.Rename(folderA, folderA+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(folderA, 0o700); err != nil {
		t.Fatal(err)
	}
	waitState(t, e, a.VaultID, StateError, 15*time.Second)
	time.Sleep(3 * pollEvery)
	if v := vaultOf(e, a.VaultID); v.State != StateError || e.wants(a.VaultID) {
		t.Fatalf("replaced folder: state %q, wanted %v; want an error that stays", v.State, e.wants(a.VaultID))
	}
	if n := countFiles(t, folderA); n != 0 {
		t.Fatalf("%d file(s) written into the replacing folder", n)
	}

	// Removing needs a disconnected drive.
	if err := e.RemoveVault(ctx, b.VaultID, "Work"); !errors.Is(err, errConnected) {
		t.Fatalf("remove while connected: got %v", err)
	}
	if err := e.DisconnectAll(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := e.RemoveVault(ctx, b.VaultID, "Work"); err != nil {
		t.Fatal(err)
	}
	if n := countFiles(t, folderB); n != 1 {
		t.Fatalf("removing vault B changed its folder: %d file(s)", n)
	}
	if len(e.Snapshot().Vaults) != 1 {
		t.Fatalf("vaults after removing one: %+v", e.Snapshot().Vaults)
	}
}
