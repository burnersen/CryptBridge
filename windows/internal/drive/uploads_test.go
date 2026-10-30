// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package drive

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/rcd"
	"github.com/burnersen/CryptBridge/windows/internal/testrclone"
)

// The limit must apply to uploads only, in bytes per second, and the
// defaults must be rclone's own.
func TestUploadsArgs(t *testing.T) {
	cases := []struct {
		in   Uploads
		want []string
	}{
		{Uploads{}, []string{"--transfers", "4", "--bwlimit", "off"}},
		{Uploads{Parallel: 2, MaxMBps: 5}, []string{"--transfers", "2", "--bwlimit", "5000000B:off"}},
		{Uploads{Parallel: 1, MaxMBps: 0.5}, []string{"--transfers", "1", "--bwlimit", "500000B:off"}},
		{Uploads{Parallel: 8, MaxMBps: 2.25}, []string{"--transfers", "8", "--bwlimit", "2250000B:off"}},
		{Uploads{Parallel: 3, MaxMBps: -1}, []string{"--transfers", "3", "--bwlimit", "off"}},
	}
	for _, c := range cases {
		if got := c.in.Args(); !slices.Equal(got, c.want) {
			t.Errorf("%+v.Args() = %v; want %v", c.in, got, c.want)
		}
	}
}

// nearRate compares upload limits read back from rclone. rclone keeps a
// limit given on its command line with four digits only (5000000B becomes
// 4.768Mi, 4999610 bytes per second); one set while it runs stays exact.
func nearRate(got, want int64) bool {
	if want <= 0 {
		return got == want
	}
	diff := got - want
	return diff*1000 <= want && -diff*1000 <= want
}

// TestUploadsApply runs the real rclone: the limits change while it runs,
// the speed for uploads only, and back. Skipped when rclone is missing.
func TestUploadsApply(t *testing.T) {
	exe := testrclone.Path(t)
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "rcd.json")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	client, err := rcd.Start(ctx, rcd.Options{
		RcloneExe:  exe,
		StateFile:  stateFile,
		ConfigFile: "NUL",
		ExtraArgs:  Uploads{Parallel: 2, MaxMBps: 5}.Args(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rcd.Stop(context.Background(), client, stateFile) }()

	check := func(when string, wantParallel int, wantTx int64) {
		t.Helper()
		var limit struct {
			Tx int64 `json:"bytesPerSecondTx"`
			Rx int64 `json:"bytesPerSecondRx"`
		}
		if err := client.Call(ctx, "core/bwlimit", nil, &limit); err != nil {
			t.Fatal(err)
		}
		if !nearRate(limit.Tx, wantTx) || limit.Rx != -1 {
			t.Errorf("%s: upload limit %d, download limit %d; want %d and -1 (none)", when, limit.Tx, limit.Rx, wantTx)
		}
		var options struct {
			Main struct {
				Transfers int `json:"Transfers"`
			} `json:"main"`
		}
		if err := client.Call(ctx, "options/get", map[string]any{"blocks": "main"}, &options); err != nil {
			t.Fatal(err)
		}
		if options.Main.Transfers != wantParallel {
			t.Errorf("%s: %d uploads at the same time; want %d", when, options.Main.Transfers, wantParallel)
		}
	}

	check("started", 2, 5_000_000)
	if err := (Uploads{Parallel: 6, MaxMBps: 1.5}).Apply(ctx, client); err != nil {
		t.Fatal(err)
	}
	check("changed", 6, 1_500_000)
	if err := (Uploads{Parallel: 4}).Apply(ctx, client); err != nil {
		t.Fatal(err)
	}
	check("no limit", 4, -1)
}
