// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/prereq"
)

// withoutRcloneOnPath keeps an rclone on this PC's PATH out of the test;
// Windows' own programs stay reachable.
func withoutRcloneOnPath(t *testing.T) {
	t.Setenv("PATH", filepath.Join(os.Getenv("SystemRoot"), "System32"))
}

// The very first rclone is the user's choice (the Download button):
// nothing is downloaded by itself.
func TestStartWithoutRcloneAsksForTheDownload(t *testing.T) {
	withoutRcloneOnPath(t)
	paths := testPaths(t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e := New(paths, "test")
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Shutdown(context.Background(), true) })
	waitPhase(t, e, PhaseError, 30*time.Second)

	snap := e.Snapshot()
	if snap.Rclone != nil || snap.RcloneUpdate != nil {
		t.Fatalf("rclone %+v, update %+v; want neither", snap.Rclone, snap.RcloneUpdate)
	}
	if want := "needs exactly rclone " + prereq.RcloneVersion; !strings.Contains(snap.Message, want) {
		t.Fatalf("message %q; want it to contain %q", snap.Message, want)
	}
	if entries, _ := os.ReadDir(paths.BinDir); len(entries) != 0 {
		t.Fatalf("something was downloaded by itself: %v", entries)
	}
}

// After an update of CryptBridge, the older rclone in its own folder is
// replaced by itself, and the window sees the progress. A broken rclone.exe
// stands in for the older one. Downloads about 30 MB when
// CRYPTBRIDGE_LIVE_DOWNLOAD is set.
func TestStartReplacesAnOlderRcloneLive(t *testing.T) {
	if os.Getenv("CRYPTBRIDGE_LIVE_DOWNLOAD") == "" {
		t.Skip("CRYPTBRIDGE_LIVE_DOWNLOAD not set")
	}
	withoutRcloneOnPath(t)
	paths := testPaths(t.TempDir())
	if err := os.MkdirAll(paths.BinDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"rclone.exe":                            "an older rclone",
		"rclone.exe.old":                        "left from an earlier update",
		"rclone-v1.75.1-windows-amd64.zip.part": "an unfinished download",
	} {
		if err := os.WriteFile(filepath.Join(paths.BinDir, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	e := New(paths, "test")
	var mu sync.Mutex
	var progress []int
	e.Subscribe(func(s Snapshot) {
		if s.RcloneUpdate != nil {
			mu.Lock()
			progress = append(progress, s.RcloneUpdate.Percent)
			mu.Unlock()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Shutdown(context.Background(), true) })
	waitPhase(t, e, PhaseSetup, time.Minute)

	snap := e.Snapshot()
	want := filepath.Join(paths.BinDir, "rclone.exe")
	if snap.Rclone == nil || snap.Rclone.Path != want || snap.Rclone.Version != prereq.RcloneVersion {
		t.Fatalf("rclone %+v; want %s %s", snap.Rclone, want, prereq.RcloneVersion)
	}
	if snap.RcloneUpdate != nil {
		t.Fatalf("the progress stays on screen: %+v", snap.RcloneUpdate)
	}
	mu.Lock()
	t.Logf("progress seen by the window: %v", progress)
	if len(progress) < 2 || progress[len(progress)-1] != 100 {
		t.Errorf("the window saw no real progress: %v", progress)
	}
	mu.Unlock()
	entries, err := os.ReadDir(paths.BinDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("left in the folder: %v; want only rclone.exe", entries)
	}
}
