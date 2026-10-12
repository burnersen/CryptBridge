// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package vault

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/rcd"
)

// These tests use a real rclone (from PATH) on folders of this PC; they need
// neither pCloud nor a test vault.

func localSetup(t *testing.T) (context.Context, *rcd.Client) {
	t.Helper()
	exe, err := exec.LookPath("rclone")
	if err != nil {
		t.Skip("rclone not found in PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	stateFile := filepath.Join(t.TempDir(), "rcd.json")
	client, err := rcd.Start(ctx, rcd.Options{RcloneExe: exe, StateFile: stateFile, ConfigFile: "NUL"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rcd.Stop(context.Background(), client, stateFile) })
	return ctx, client
}

// splitLocal turns `C:\x\y` into the base remote "C:/" and the path "x/y",
// as CryptBridge does for vaults on this PC.
func splitLocal(dir string) (string, string) {
	volume := filepath.VolumeName(dir)
	return volume + "/", strings.TrimPrefix(filepath.ToSlash(dir), volume+"/")
}

// makeVault creates a vault with a check file and a few encrypted folder
// names in dir.
func makeVault(t *testing.T, ctx context.Context, client *rcd.Client, dir, password string) Config {
	t.Helper()
	base, rel := splitLocal(dir)
	cfg := NewDefault(rel)
	cfg.Password = password
	if err := Configure(ctx, client, "cbtest", base, cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Remove(context.Background(), client, "cbtest") })
	if err := WriteCheckFile(ctx, client, "cbtest:"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Photos", "Documents", "Films"} {
		if err := client.Call(ctx, "operations/mkdir", map[string]any{"fs": "cbtest:", "remote": name}, nil); err != nil {
			t.Fatal(err)
		}
	}
	return cfg
}

// configureTestVault registers cfg as the remote "cbtest" on base.
func configureTestVault(t *testing.T, ctx context.Context, client *rcd.Client, base string, cfg Config) {
	t.Helper()
	if err := Configure(ctx, client, "cbtest", base, cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Remove(context.Background(), client, "cbtest") })
}

// putFile writes a file with the given content into the vault behind remote.
func putFile(t *testing.T, ctx context.Context, client *rcd.Client, remote, name string, content []byte) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
		t.Fatal(err)
	}
	request := map[string]any{"srcFs": dir, "srcRemote": name, "dstFs": remote, "dstRemote": name}
	if err := client.Call(ctx, "operations/copyfile", request, nil); err != nil {
		t.Fatal(err)
	}
}

// expectVerdict runs Verify and fails unless it gives want; content tells
// whether a file must have been read for it.
func expectVerdict(t *testing.T, ctx context.Context, client *rcd.Client, base string, cfg Config, want Verdict, content bool, what string) Verification {
	t.Helper()
	got, err := Verify(ctx, client, base, cfg)
	if err != nil || got.Verdict != want || got.ContentChecked != content {
		t.Fatalf("%s: %+v %v; want %s with content checked = %v", what, got, err, want, content)
	}
	return got
}

// TestLocalVerifyByContent covers vaults whose names prove nothing. Right
// and wrong keys take turns, so that a remote cached with old keys shows.
func TestLocalVerifyByContent(t *testing.T) {
	ctx, client := localSetup(t)
	for _, mode := range []string{EncryptionObfuscate, EncryptionOff} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			base, rel := splitLocal(filepath.Join(root, "Vault"))
			cfg := NewDefault(rel)
			cfg.FilenameEncryption = mode
			cfg.Password = "right password"
			cfg.Password2 = "right salt"
			configureTestVault(t, ctx, client, base, cfg)
			if err := WriteCheckFile(ctx, client, "cbtest:"); err != nil {
				t.Fatal(err)
			}
			putFile(t, ctx, client, "cbtest:", "Notes 2026.txt", []byte("some notes"))
			if err := client.Call(ctx, "operations/mkdir", map[string]any{"fs": "cbtest:", "remote": "Photos"}, nil); err != nil {
				t.Fatal(err)
			}

			wrong := cfg
			wrong.Password = "wrong password"
			wrongSalt := cfg
			wrongSalt.Password2 = "wrong salt"
			expectVerdict(t, ctx, client, base, cfg, VerdictMatch, true, "right keys")
			expectVerdict(t, ctx, client, base, wrong, VerdictMismatch, true, "wrong password")
			expectVerdict(t, ctx, client, base, cfg, VerdictMatch, true, "right keys again")
			expectVerdict(t, ctx, client, base, wrongSalt, VerdictMismatch, true, "wrong salt")

			// The right password with other name settings would mix two
			// kinds of names in one vault.
			for _, other := range []string{EncryptionStandard, EncryptionObfuscate, EncryptionOff} {
				if other == mode {
					continue
				}
				changed := cfg
				changed.FilenameEncryption = other
				got, err := Verify(ctx, client, base, changed)
				if err != nil || got.Verdict != VerdictMismatch {
					t.Fatalf("right password, names %s instead of %s: %+v %v; want mismatch", other, mode, got, err)
				}
			}
		})
	}
}

// TestLocalVerifyObfuscatedCheckFileName covers the case that made names
// worthless as proof with obfuscate: a wrong password that produces the
// very name of the check file.
func TestLocalVerifyObfuscatedCheckFileName(t *testing.T) {
	ctx, client := localSetup(t)
	root := t.TempDir()
	base, rel := splitLocal(filepath.Join(root, "Vault"))
	cfg := NewDefault(rel)
	cfg.FilenameEncryption = EncryptionObfuscate
	cfg.Password = "right password"
	configureTestVault(t, ctx, client, base, cfg)
	if err := WriteCheckFile(ctx, client, "cbtest:"); err != nil {
		t.Fatal(err)
	}
	rightName, err := encodeOne(ctx, client, "cbtest:", CheckFileName)
	if err != nil {
		t.Fatal(err)
	}

	// About one password in 25 hits the name; 300 tries miss with a
	// probability of about 5 in a million.
	var colliding Config
	for try := range 300 {
		candidate := cfg
		candidate.Password = fmt.Sprintf("wrong password %d", try)
		if err := Configure(ctx, client, "cbguess", base, candidate); err != nil {
			t.Fatal(err)
		}
		name, err := encodeOne(ctx, client, "cbguess:", CheckFileName)
		if err != nil {
			t.Fatal(err)
		}
		if name == rightName {
			colliding = candidate
			break
		}
	}
	_ = Remove(context.Background(), client, "cbguess")
	if colliding.Password == "" {
		t.Fatal("no wrong password produced the name of the check file in 300 tries")
	}

	got := expectVerdict(t, ctx, client, base, colliding, VerdictMismatch, true, "wrong password, same check file name")
	if !got.HasCheckFile {
		t.Fatalf("%+v; want the check file found by its name", got)
	}
}

// TestLocalVerifyUnverifiable: without a file that can prove the keys, a
// vault with plain names cannot be checked; with one, it can.
func TestLocalVerifyUnverifiable(t *testing.T) {
	ctx, client := localSetup(t)
	root := t.TempDir()
	base, rel := splitLocal(filepath.Join(root, "Vault"))
	cfg := NewDefault(rel)
	cfg.FilenameEncryption = EncryptionOff
	cfg.Password = "right password"
	configureTestVault(t, ctx, client, base, cfg)
	wrong := cfg
	wrong.Password = "wrong password"

	putFile(t, ctx, client, "cbtest:", "empty.txt", nil)
	putFile(t, ctx, client, "cbtest:", "large.bin", make([]byte, maxContentCheckSize))
	expectVerdict(t, ctx, client, base, cfg, VerdictUnverifiable, false, "only an empty and a large file")
	expectVerdict(t, ctx, client, base, wrong, VerdictUnverifiable, false, "only an empty and a large file, wrong keys")

	putFile(t, ctx, client, "cbtest:", "small.txt", []byte("small"))
	expectVerdict(t, ctx, client, base, wrong, VerdictMismatch, true, "a small file, wrong keys")
	got := expectVerdict(t, ctx, client, base, cfg, VerdictMatch, true, "a small file, right keys")
	if got.HasCheckFile {
		t.Fatalf("%+v; want no check file in this vault", got)
	}
}

func TestLocalVerifyAndInspect(t *testing.T) {
	ctx, client := localSetup(t)
	root := t.TempDir()
	vaultDir := filepath.Join(root, "Vault")
	cfg := makeVault(t, ctx, client, vaultDir, "right password")
	base, _ := splitLocal(root)

	got, err := Verify(ctx, client, base, cfg)
	if err != nil || got.Verdict != VerdictMatch || !got.HasCheckFile {
		t.Fatalf("right keys: %+v %v; want match by the check file", got, err)
	}
	wrong := cfg
	wrong.Password = "wrong password"
	if got, err := Verify(ctx, client, base, wrong); err != nil || got.Verdict != VerdictMismatch {
		t.Fatalf("wrong keys: %+v %v; want mismatch", got, err)
	}

	folder, err := Inspect(ctx, client, base, cfg.Path)
	if err != nil || !folder.Exists || !folder.LooksLikeVault || folder.Entries != 4 || len(folder.Subfolders) != 3 {
		t.Fatalf("inspect vault: %+v %v; want an existing vault with 4 entries, 3 of them folders", folder, err)
	}

	plain := filepath.Join(root, "Plain")
	if err := os.MkdirAll(filepath.Join(plain, "b-folder"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plain, "A file.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(plain, "a-folder"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, plainRel := splitLocal(plain)
	folder, err = Inspect(ctx, client, base, plainRel)
	if err != nil || !folder.Exists || folder.LooksLikeVault || folder.Entries != 3 ||
		strings.Join(folder.Subfolders, ",") != "a-folder,b-folder" {
		t.Fatalf("inspect plain folder: %+v %v", folder, err)
	}

	_, missingRel := splitLocal(filepath.Join(root, "Missing"))
	if folder, err := Inspect(ctx, client, base, missingRel); err != nil || folder.Exists {
		t.Fatalf("inspect missing folder: %+v %v; want not existing, no error", folder, err)
	}
}

func TestLocalEnclosingVault(t *testing.T) {
	ctx, client := localSetup(t)
	root := t.TempDir()
	vaultDir := filepath.Join(root, "Vault")
	cfg := makeVault(t, ctx, client, vaultDir, "password")
	base, _ := splitLocal(root)

	// A folder that does not exist yet, two levels below the vault.
	_, nested := splitLocal(filepath.Join(vaultDir, "Sub", "New vault"))
	found, inside, err := EnclosingVault(ctx, client, base, nested)
	if err != nil || !inside || found != cfg.Path {
		t.Fatalf("below the vault: %q %v %v; want %q", found, inside, err, cfg.Path)
	}

	_, sibling := splitLocal(filepath.Join(root, "Other vault"))
	if found, inside, err := EnclosingVault(ctx, client, base, sibling); err != nil || inside {
		t.Fatalf("next to the vault: %q %v %v; want no enclosing vault", found, inside, err)
	}
}
