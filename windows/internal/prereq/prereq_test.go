// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package prereq

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.75.1", "1.75.0", 1},
		{"1.75.0", "1.75.0", 0},
		{"1.9.0", "1.10.0", -1}, // as text this would be the wrong way round
		{"2.0", "1.99.99", 1},
		{"1.75", "1.75.0", 0},
		{"garbage", "0.0.0", 0},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestFindRcloneOnThisMachine(t *testing.T) {
	found, err := FindRclone(t.TempDir())
	if err != nil {
		t.Skipf("no rclone here: %v", err)
	}
	if CompareVersions(found.Version, MinRcloneVersion) < 0 {
		t.Fatalf("accepted too old rclone %+v", found)
	}
	t.Logf("found %+v", found)
}

func TestWinFspOnThisMachine(t *testing.T) {
	dir, ok := WinFspInstalled()
	t.Logf("WinFsp installed=%v dir=%q", ok, dir)
}
