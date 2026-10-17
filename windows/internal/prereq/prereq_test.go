// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package prereq

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeRcloneEnv makes the test binary act as a fake rclone.exe: copied into
// a folder, it answers "version" with the version in version.txt next to
// it, and otherwise just keeps running for a while.
const fakeRcloneEnv = "CRYPTBRIDGE_FAKE_RCLONE"

func TestMain(m *testing.M) {
	if os.Getenv(fakeRcloneEnv) == "1" {
		fakeRclone()
		return
	}
	os.Exit(m.Run())
}

func fakeRclone() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		self, err := os.Executable()
		if err != nil {
			os.Exit(2)
		}
		version, err := os.ReadFile(filepath.Join(filepath.Dir(self), "version.txt"))
		if err != nil {
			os.Exit(2)
		}
		fmt.Printf("rclone v%s\n- os/version: fake\n", strings.TrimSpace(string(version)))
		os.Exit(0)
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}

// putFakeRclone copies the test binary as dir\rclone.exe, answering version.
func putFakeRclone(t *testing.T, dir, version string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "rclone.exe")
	copyFile(t, self, target)
	if err := os.WriteFile(filepath.Join(dir, "version.txt"), []byte(version), 0o600); err != nil {
		t.Fatal(err)
	}
	return target
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	src, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFindRcloneTakesOnlyThePinnedVersion(t *testing.T) {
	t.Setenv(fakeRcloneEnv, "1")
	root := t.TempDir()
	emptyBin := filepath.Join(root, "empty-bin")

	for _, version := range []string{"1.75.1", "1.75.20", "1.76.0"} {
		t.Run("other version "+version+" on the PATH", func(t *testing.T) {
			onPath := filepath.Join(root, "path-"+version)
			putFakeRclone(t, onPath, version)
			t.Setenv("PATH", onPath)
			_, err := FindRclone(emptyBin)
			if !errors.Is(err, ErrRcloneNotFound) || !strings.Contains(err.Error(), version) {
				t.Fatalf("got %v, want ErrRcloneNotFound naming %s", err, version)
			}
		})
	}

	t.Run("pinned version on the PATH", func(t *testing.T) {
		onPath := filepath.Join(root, "path-pinned")
		want := putFakeRclone(t, onPath, RcloneVersion)
		t.Setenv("PATH", onPath)
		got, err := FindRclone(emptyBin)
		if err != nil || !strings.EqualFold(got.Path, want) || got.Version != RcloneVersion {
			t.Fatalf("got %+v, %v; want %s", got, err, want)
		}
	})

	t.Run("pinned version in the own folder", func(t *testing.T) {
		bin := filepath.Join(root, "bin-pinned")
		want := putFakeRclone(t, bin, RcloneVersion)
		t.Setenv("PATH", filepath.Join(root, "nothing-here"))
		got, err := FindRclone(bin)
		if err != nil || got.Path != want {
			t.Fatalf("got %+v, %v; want %s", got, err, want)
		}
	})

	t.Run("older version in the own folder wins over the PATH", func(t *testing.T) {
		bin := filepath.Join(root, "bin-old")
		putFakeRclone(t, bin, "1.75.1")
		onPath := filepath.Join(root, "path-pinned-2")
		putFakeRclone(t, onPath, RcloneVersion)
		t.Setenv("PATH", onPath)
		_, err := FindRclone(bin)
		if !errors.Is(err, ErrRcloneOutdated) || !strings.Contains(err.Error(), "1.75.1") {
			t.Fatalf("got %v, want ErrRcloneOutdated naming 1.75.1", err)
		}
	})

	t.Run("broken rclone in the own folder", func(t *testing.T) {
		bin := filepath.Join(root, "bin-broken")
		if err := os.MkdirAll(bin, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "rclone.exe"), []byte("not a program"), 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := FindRclone(bin)
		if !errors.Is(err, ErrRcloneOutdated) {
			t.Fatalf("got %v, want ErrRcloneOutdated", err)
		}
	})
}

func TestFindRcloneOnThisMachine(t *testing.T) {
	found, err := FindRclone(t.TempDir())
	if err != nil {
		t.Skipf("no rclone %s here: %v", RcloneVersion, err)
	}
	if found.Version != RcloneVersion {
		t.Fatalf("accepted another rclone %+v", found)
	}
	t.Logf("found %+v", found)
}

func TestWinFspOnThisMachine(t *testing.T) {
	dir, ok := WinFspInstalled()
	t.Logf("WinFsp installed=%v dir=%q", ok, dir)
}
