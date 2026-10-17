// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package prereq

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBuiltInChecksums(t *testing.T) {
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, goarch := range []string{"amd64", "arm64"} {
		name, sum, err := rcloneZip(goarch)
		if err != nil || !hex64.MatchString(sum) {
			t.Errorf("%s: %q %q %v", goarch, name, sum, err)
		}
		if want := "rclone-v" + RcloneVersion + "-windows-" + goarch + ".zip"; name != want {
			t.Errorf("%s: zip name %q, want %q", goarch, name, want)
		}
	}
	if _, _, err := rcloneZip("386"); err == nil || !strings.Contains(err.Error(), "386") {
		t.Errorf("an unknown processor must be a clear error, got %v", err)
	}
}

func TestRcloneSources(t *testing.T) {
	const zipName = "rclone-v" + RcloneVersion + "-windows-amd64.zip"
	got := rcloneSources(zipName, "0.9.2")
	want := []string{
		"https://downloads.rclone.org/v" + RcloneVersion + "/" + zipName,
		"https://github.com/rclone/rclone/releases/download/v" + RcloneVersion + "/" + zipName,
		"https://github.com/burnersen/CryptBridge/releases/download/v0.9.2/" + zipName,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, unreleased := range []string{"dev", "test", ""} {
		if got := rcloneSources(zipName, unreleased); len(got) != 2 {
			t.Errorf("version %q has no release of its own, got %v", unreleased, got)
		}
	}
}

// fakeServers serves "/missing" (404), "/wrong" (another file) and "/good",
// and remembers which paths were asked for.
func fakeServers(t *testing.T, good []byte) (base string, asked func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/wrong":
			_, _ = w.Write([]byte("a file that only looks like rclone"))
		case "/good":
			_, _ = w.Write(good)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestFetchVerifiedTriesTheNextServer(t *testing.T) {
	good := []byte("the real rclone zip")
	base, asked := fakeServers(t, good)
	path := filepath.Join(t.TempDir(), "rclone.zip.part")

	err := fetchVerified(context.Background(), []string{base + "/missing", base + "/wrong", base + "/good", base + "/later"},
		path, sha256Hex(good), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(good) {
		t.Fatalf("kept %q, %v", data, err)
	}
	if got := strings.Join(asked(), " "); got != "/missing /wrong /good" {
		t.Fatalf("asked %s; the servers after the good one must not be asked", got)
	}
}

func TestFetchVerifiedRejectsAWrongChecksum(t *testing.T) {
	base, _ := fakeServers(t, []byte("the real rclone zip"))
	path := filepath.Join(t.TempDir(), "rclone.zip.part")

	err := fetchVerified(context.Background(), []string{base + "/wrong", base + "/missing"}, path,
		sha256Hex([]byte("the real rclone zip")), nil)
	if err == nil {
		t.Fatal("a file with the wrong checksum must not count")
	}
	if !strings.Contains(err.Error(), "does not match the checksum") || !strings.Contains(err.Error(), "404") {
		t.Errorf("the error must say what went wrong with each server: %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatal("the rejected file was kept")
	}
}

// makeZip writes a zip with rclone.exe (the given content) and a readme.
func makeZip(t *testing.T, content string) string {
	t.Helper()
	zipPath := filepath.Join(t.TempDir(), "rclone.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	entry, _ := w.Create("rclone-v" + RcloneVersion + "-windows-amd64/rclone.exe")
	_, _ = entry.Write([]byte(content))
	other, _ := w.Create("rclone-v" + RcloneVersion + "-windows-amd64/README.txt")
	_, _ = other.Write([]byte("readme"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return zipPath
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertGone(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s was left behind", filepath.Base(path))
		}
	}
}

func TestReplaceExe(t *testing.T) {
	t.Run("first rclone", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "rclone.exe")
		if err := replaceExe(makeZip(t, "MZ new"), target); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, target); got != "MZ new" {
			t.Fatalf("rclone.exe holds %q", got)
		}
		assertGone(t, target+".new", target+".old")
	})

	t.Run("old rclone not running", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "rclone.exe")
		if err := os.WriteFile(target, []byte("MZ old"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := replaceExe(makeZip(t, "MZ new"), target); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, target); got != "MZ new" {
			t.Fatalf("rclone.exe holds %q", got)
		}
		assertGone(t, target+".new", target+".old")
	})

	t.Run("old rclone still running", func(t *testing.T) {
		t.Setenv(fakeRcloneEnv, "1")
		dir := t.TempDir()
		target := putFakeRclone(t, dir, "1.75.1")
		running := exec.Command(target, "keep-running")
		if err := running.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = running.Process.Kill(); _ = running.Wait() })

		if err := replaceExe(makeZip(t, "MZ new"), target); err != nil {
			t.Fatalf("a running rclone must not stop the replacement: %v", err)
		}
		if got := readFile(t, target); got != "MZ new" {
			t.Fatalf("rclone.exe holds %q", got)
		}
		assertGone(t, target+".new")
		_, err := os.Stat(target + ".old")
		t.Logf("while the old rclone runs, rclone.exe.old exists: %v", err == nil)

		_ = running.Process.Kill()
		_ = running.Wait()
		deadline := time.Now().Add(10 * time.Second)
		for {
			CleanUp(dir)
			if _, err := os.Stat(target + ".old"); os.IsNotExist(err) || time.Now().After(deadline) {
				break
			}
			time.Sleep(200 * time.Millisecond) // Windows releases the file a moment after the end
		}
		assertGone(t, target+".old")
	})

	t.Run("old rclone cannot be set aside", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "rclone.exe")
		if err := os.WriteFile(target, []byte("MZ old"), 0o755); err != nil {
			t.Fatal(err)
		}
		// A folder that is not empty blocks the name rclone.exe.old.
		if err := os.MkdirAll(filepath.Join(target+".old", "blocker"), 0o700); err != nil {
			t.Fatal(err)
		}
		err := replaceExe(makeZip(t, "MZ new"), target)
		if err == nil || !strings.Contains(err.Error(), "could not be set aside") {
			t.Fatalf("got %v, want a clear message", err)
		}
		if got := readFile(t, target); got != "MZ old" {
			t.Fatalf("the old rclone was changed: %q", got)
		}
		assertGone(t, target+".new")
	})

	t.Run("zip without rclone.exe", func(t *testing.T) {
		zipPath := filepath.Join(t.TempDir(), "empty.zip")
		f, _ := os.Create(zipPath)
		_ = zip.NewWriter(f).Close()
		_ = f.Close()
		target := filepath.Join(t.TempDir(), "rclone.exe")
		if err := replaceExe(zipPath, target); err == nil {
			t.Fatal("a zip without rclone.exe must be an error")
		}
		assertGone(t, target, target+".new")
	})
}

func TestCleanUp(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"rclone.exe", "rclone.exe.old", "rclone.exe.new", "rclone-v1.75.2-windows-amd64.zip.part"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	CleanUp(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "rclone.exe" {
		t.Fatalf("left %v, want only rclone.exe", entries)
	}
	CleanUp(filepath.Join(dir, "no-such-folder")) // must not panic
}

// TestDownloadRcloneLive really downloads rclone (about 30 MB per server)
// when CRYPTBRIDGE_LIVE_DOWNLOAD is set: once from each of rclone's two
// servers, checked against the built-in checksum, then the whole way into
// a folder. CryptBridge's own release is only a source once it exists.
func TestDownloadRcloneLive(t *testing.T) {
	if os.Getenv("CRYPTBRIDGE_LIVE_DOWNLOAD") == "" {
		t.Skip("CRYPTBRIDGE_LIVE_DOWNLOAD not set")
	}
	zipName, want, err := rcloneZip(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range rcloneSources(zipName, "dev") {
		parsed, _ := url.Parse(source)
		t.Run(parsed.Host, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), zipName)
			if err := fetchVerified(context.Background(), []string{source}, path, want, nil); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s matches the built-in checksum", source)
		})
	}

	var calls int
	got, err := DownloadRclone(context.Background(), t.TempDir(), "dev", func(done, total int64) { calls++ })
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("downloaded %+v, %d progress reports", got, calls)
	if got.Version != RcloneVersion || calls == 0 {
		t.Fatalf("unexpected result %+v, %d progress reports", got, calls)
	}
}
