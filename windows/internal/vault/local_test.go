// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package vault

import (
	"context"
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
