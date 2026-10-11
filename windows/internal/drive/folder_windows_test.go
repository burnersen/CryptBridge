// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

//go:build windows

package drive

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// The folder ID is what tells CryptBridge that the folder of a connected
// local vault is gone or was replaced, so these cases must all be told apart.
func TestIdentifyFolder(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Vault")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	first, err := IdentifyFolder(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := IdentifyFolder(dir); err != nil || again != first {
		t.Fatalf("same folder twice: %+v %v; want %+v", again, err, first)
	}

	// Moved away and replaced by a new folder of the same name.
	if err := os.Rename(dir, filepath.Join(root, "Moved")); err != nil {
		t.Fatal(err)
	}
	if _, err := IdentifyFolder(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("moved folder: got %v, want fs.ErrNotExist", err)
	}
	if moved, err := IdentifyFolder(filepath.Join(root, "Moved")); err != nil || moved != first {
		t.Fatalf("the moved folder keeps its ID: %+v %v", moved, err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if replaced, err := IdentifyFolder(dir); err != nil || replaced == first {
		t.Fatalf("replaced folder: %+v %v; want a different ID than %+v", replaced, err, first)
	}

	// A drive letter without a disk, as after unplugging a USB drive.
	free, err := FreeLetters()
	if err != nil || len(free) == 0 {
		t.Fatalf("no free drive letter to test with: %v", err)
	}
	if _, err := IdentifyFolder(free[0] + `:\Vault`); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing drive %s: got %v, want fs.ErrNotExist", free[0], err)
	}

	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := IdentifyFolder(file); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a file: got %v, want an error that is not fs.ErrNotExist", err)
	}
}

func TestDiskSpace(t *testing.T) {
	space, err := DiskSpace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if space.Total <= 0 || space.Free < 0 || space.Free > space.Total || space.Used <= 0 || space.Used > space.Total {
		t.Fatalf("implausible space: %+v", space)
	}
	free, _ := FreeLetters()
	if len(free) > 0 {
		if _, err := DiskSpace(free[0] + `:\`); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("missing drive: got %v, want fs.ErrNotExist", err)
		}
	}
}
