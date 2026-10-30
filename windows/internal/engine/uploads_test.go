// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package engine

import (
	"context"
	"testing"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/settings"
)

// optionsOf returns the options as the Settings page shows them.
func optionsOf(s Snapshot) Options {
	return Options{
		CacheDir:          s.CacheDir,
		CacheMaxSize:      s.CacheMaxSize,
		CacheMinFreeSpace: s.CacheMinFreeSpace,
		AutoReconnect:     s.AutoReconnect,
		Theme:             s.Theme,
		UploadsParallel:   s.UploadsParallel,
		UploadMaxMBps:     s.UploadMaxMBps,
	}
}

// Upload settings outside their range are refused; valid ones are stored,
// also while no rclone runs.
func TestUpdateOptionsUploads(t *testing.T) {
	paths := testPaths(t.TempDir())
	e := New(paths, "test")
	e.settings = settings.Defaults(paths.CacheDir)
	ctx := context.Background()

	snap := e.Snapshot()
	if snap.UploadsParallel != 4 || snap.UploadMaxMBps != 0 {
		t.Fatalf("defaults: %d at the same time, %g MB/s; want 4 and no limit", snap.UploadsParallel, snap.UploadMaxMBps)
	}
	for _, bad := range []Options{
		{UploadsParallel: 0},
		{UploadsParallel: 17},
		{UploadsParallel: 4, UploadMaxMBps: 0.05},
		{UploadsParallel: 4, UploadMaxMBps: -2},
		{UploadsParallel: 4, UploadMaxMBps: 10001},
	} {
		o := optionsOf(snap)
		o.UploadsParallel, o.UploadMaxMBps = bad.UploadsParallel, bad.UploadMaxMBps
		if err := e.UpdateOptions(ctx, o); err == nil {
			t.Errorf("%d at the same time, %g MB/s: accepted", bad.UploadsParallel, bad.UploadMaxMBps)
		}
	}

	o := optionsOf(snap)
	o.UploadsParallel, o.UploadMaxMBps = 2, 2.5
	if err := e.UpdateOptions(ctx, o); err != nil {
		t.Fatal(err)
	}
	if snap := e.Snapshot(); snap.UploadsParallel != 2 || snap.UploadMaxMBps != 2.5 {
		t.Fatalf("after saving: %d at the same time, %g MB/s; want 2 and 2.5", snap.UploadsParallel, snap.UploadMaxMBps)
	}
	stored, err := settings.Load(paths.SettingsFile, paths.CacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Uploads != (settings.Uploads{Parallel: 2, MaxMBps: 2.5}) {
		t.Fatalf("stored uploads %+v", stored.Uploads)
	}
}

// limits are the upload limits a running rclone reports.
type limits struct {
	Tx, Rx    int64 // bytes per second; -1: no limit
	Transfers int
}

// near compares limits read back from rclone. rclone keeps a speed given on
// its command line with four digits only (1500000B becomes 1.431Mi, 1500512
// bytes per second); one set while it runs stays exact.
func (l limits) near(want limits) bool {
	if l.Rx != want.Rx || l.Transfers != want.Transfers {
		return false
	}
	if want.Tx <= 0 {
		return l.Tx == want.Tx
	}
	diff := l.Tx - want.Tx
	return diff*1000 <= want.Tx && -diff*1000 <= want.Tx
}

// TestUploadLimitsReachRclone runs the real rclone: a change on the Settings
// page reaches the running rclone at once, and a newly started rclone gets
// the stored limits. Skipped when rclone is missing.
func TestUploadLimitsReachRclone(t *testing.T) {
	paths := testPaths(t.TempDir())
	useTestRclone(t, paths)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	limitsOf := func(e *Engine) limits {
		t.Helper()
		client := clientOf(e)
		if client == nil {
			t.Fatal("no rclone running")
		}
		var bw struct {
			Tx int64 `json:"bytesPerSecondTx"`
			Rx int64 `json:"bytesPerSecondRx"`
		}
		if err := client.Call(ctx, "core/bwlimit", nil, &bw); err != nil {
			t.Fatal(err)
		}
		var options struct {
			Main struct{ Transfers int } `json:"main"`
		}
		if err := client.Call(ctx, "options/get", map[string]any{"blocks": "main"}, &options); err != nil {
			t.Fatal(err)
		}
		return limits{bw.Tx, bw.Rx, options.Main.Transfers}
	}

	firstCtx, stopFirst := context.WithCancel(ctx) // ends the first engine's monitor
	defer stopFirst()
	e := New(paths, "test")
	if err := e.Start(firstCtx); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, e, PhaseSetup, 30*time.Second)
	if got := limitsOf(e); !got.near(limits{-1, -1, 4}) {
		t.Fatalf("fresh installation: %+v; want no speed limit and 4 at the same time", got)
	}

	o := optionsOf(e.Snapshot())
	o.UploadsParallel, o.UploadMaxMBps = 3, 1.5
	if err := e.UpdateOptions(ctx, o); err != nil {
		t.Fatal(err)
	}
	if got := limitsOf(e); !got.near(limits{1_500_000, -1, 3}) {
		t.Fatalf("after saving: %+v; want 1.5 MB/s for uploads only and 3 at the same time", got)
	}
	if err := e.Shutdown(ctx, true); err != nil {
		t.Fatal(err)
	}
	stopFirst()

	again := New(paths, "test")
	if err := again.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = again.Shutdown(context.Background(), true) }()
	waitPhase(t, again, PhaseSetup, 30*time.Second)
	if got := limitsOf(again); !got.near(limits{1_500_000, -1, 3}) {
		t.Fatalf("new rclone: %+v; want the stored limits", got)
	}
}
