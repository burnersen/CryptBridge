// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package drive

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// cacheFile creates a cached file and its metadata as rclone 1.75 does
// (cacheDir\vfs\<remote>\<path> and cacheDir\vfsMeta\<remote>\<path>).
func cacheFile(t *testing.T, cacheDir, name, meta string) {
	t.Helper()
	for dir, content := range map[string]string{"vfs": "plain text content", "vfsMeta": meta} {
		path := filepath.Join(cacheDir, dir, "cbvault", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestClearCacheDeletesUploadedFiles(t *testing.T) {
	cacheDir := t.TempDir()
	cacheFile(t, cacheDir, `Movies\film.mkv`, `{"Size": 18, "Dirty": false}`)
	other := filepath.Join(cacheDir, "vfs", "otherremote", "keep.bin")
	if err := os.MkdirAll(filepath.Dir(other), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ClearCache(cacheDir, "cbvault"); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"vfs", "vfsMeta"} {
		if _, err := os.Stat(filepath.Join(cacheDir, dir, "cbvault")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s\\cbvault still there", dir)
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("the cache of another remote must stay")
	}
}

func TestClearCacheKeepsEverythingWhileUploadsWait(t *testing.T) {
	for name, meta := range map[string]string{
		"waiting":    `{"Size": 18, "Dirty": true}`,
		"unreadable": `not json`,
		"no marker":  `{"Size": 18}`,
	} {
		t.Run(name, func(t *testing.T) {
			cacheDir := t.TempDir()
			cacheFile(t, cacheDir, "done.bin", `{"Dirty": false}`)
			cacheFile(t, cacheDir, `sub\new.bin`, meta)

			var waiting *UploadsWaitingError
			if err := ClearCache(cacheDir, "cbvault"); !errors.As(err, &waiting) || waiting.Count != 1 {
				t.Fatalf("got %v, want 1 waiting upload", err)
			}
			for _, name := range []string{"done.bin", `sub\new.bin`} {
				if _, err := os.Stat(filepath.Join(cacheDir, "vfs", "cbvault", name)); err != nil {
					t.Errorf("%s was deleted although an upload waits", name)
				}
			}
		})
	}
}

func TestClearCacheEdgeCases(t *testing.T) {
	if err := ClearCache(t.TempDir(), "cbvault"); err != nil {
		t.Errorf("no cache yet: %v", err)
	}
	if err := ClearCache(`relative\cache`, "cbvault"); err == nil {
		t.Error("a relative cache folder must be refused")
	}
	if err := ClearCache(t.TempDir(), ""); err == nil {
		t.Error("an empty remote name must be refused")
	}
}

func TestCacheRemotesAndWaitingUploads(t *testing.T) {
	cacheDir := t.TempDir()
	if got, err := CacheRemotes(cacheDir); err != nil || len(got) != 0 {
		t.Fatalf("empty cache: %v %v", got, err)
	}
	cacheFile(t, cacheDir, "waiting.bin", `{"Dirty":true}`)
	cacheFile(t, cacheDir, "done.bin", `{"Dirty":false}`)
	if err := os.MkdirAll(filepath.Join(cacheDir, "vfsMeta", "cbv1a2b3c4d"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := CacheRemotes(cacheDir)
	if err != nil || len(got) != 2 || got[0] != "cbv1a2b3c4d" || got[1] != "cbvault" {
		t.Fatalf("CacheRemotes = %v, %v; want [cbv1a2b3c4d cbvault]", got, err)
	}
	if n, err := WaitingUploads(cacheDir, "cbvault"); err != nil || n != 1 {
		t.Fatalf("WaitingUploads(cbvault) = %d, %v; want 1", n, err)
	}
	if n, err := WaitingUploads(cacheDir, "cbunknown"); err != nil || n != 0 {
		t.Fatalf("WaitingUploads of a remote without cache = %d, %v; want 0", n, err)
	}
	if _, err := WaitingUploads("relative", "cbvault"); err == nil {
		t.Fatal("WaitingUploads accepted a relative cache folder")
	}
}
