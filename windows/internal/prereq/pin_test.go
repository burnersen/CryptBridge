// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package prereq

import (
	"os"
	"regexp"
	"testing"
)

// The Windows program and the Linux server must use the same rclone: a
// vault is tested with one version only.
func TestLinuxSetupPinsTheSameRclone(t *testing.T) {
	script, err := os.ReadFile("../../../linux/setup.sh")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^readonly RCLONE_VERSION="([^"]*)"`).FindSubmatch(script)
	if match == nil {
		t.Fatal(`linux/setup.sh has no line readonly RCLONE_VERSION="..."`)
	}
	if got := string(match[1]); got != RcloneVersion {
		t.Fatalf("linux/setup.sh pins rclone %s, the Windows program %s", got, RcloneVersion)
	}
}
