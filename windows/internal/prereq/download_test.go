// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package prereq

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestChecksumFor(t *testing.T) {
	sums := `-----BEGIN PGP SIGNED MESSAGE-----
Hash: SHA1

3a0d7cb1e1c6e3f1d5bd4ac5b4e2a5c1e2b0f6a1d3c4b5a6978f8e7d6c5b4a39  rclone-v1.75.2-linux-amd64.zip
4b1e8dc2f2d7f4e2e6ce5bd6c5f3b6d2f3c1a7b2e4d5c6b7a8909f8e7d6c5b4a  rclone-v1.75.2-windows-amd64.zip
-----BEGIN PGP SIGNATURE-----
`
	got, err := checksumFor(sums, "rclone-v1.75.2-windows-amd64.zip")
	if err != nil || got != "4b1e8dc2f2d7f4e2e6ce5bd6c5f3b6d2f3c1a7b2e4d5c6b7a8909f8e7d6c5b4a" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := checksumFor(sums, "rclone-v1.75.2-windows-arm64.zip"); err == nil {
		t.Fatal("missing entry must be an error")
	}
}

func TestExtractExe(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "r.zip")
	f, _ := os.Create(zipPath)
	w := zip.NewWriter(f)
	entry, _ := w.Create("rclone-v1.75.2-windows-amd64/rclone.exe")
	_, _ = entry.Write([]byte("MZ fake exe"))
	other, _ := w.Create("rclone-v1.75.2-windows-amd64/README.txt")
	_, _ = other.Write([]byte("readme"))
	_ = w.Close()
	_ = f.Close()

	target := filepath.Join(dir, "bin", "rclone.exe")
	_ = os.MkdirAll(filepath.Dir(target), 0o700)
	if err := extractExe(zipPath, target); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "MZ fake exe" {
		t.Fatalf("extracted %q, %v", data, err)
	}
	if _, err := os.Stat(target + ".new"); !os.IsNotExist(err) {
		t.Fatal("temporary file left behind")
	}
}

// TestDownloadRcloneLive really downloads rclone (about 25 MB) when
// CRYPTBRIDGE_LIVE_DOWNLOAD is set.
func TestDownloadRcloneLive(t *testing.T) {
	if os.Getenv("CRYPTBRIDGE_LIVE_DOWNLOAD") == "" {
		t.Skip("CRYPTBRIDGE_LIVE_DOWNLOAD not set")
	}
	var calls int
	got, err := DownloadRclone(context.Background(), t.TempDir(), func(done, total int64) { calls++ })
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("downloaded %+v, %d progress reports", got, calls)
	if CompareVersions(got.Version, MinRcloneVersion) < 0 || calls == 0 {
		t.Fatalf("unexpected result %+v, %d progress reports", got, calls)
	}
}
