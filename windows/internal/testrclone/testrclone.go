// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

// Package testrclone finds the rclone that tests run: exactly the version
// CryptBridge pins (prereq.RcloneVersion), so that every test result holds
// for the rclone users get. It is only imported by tests.
//
// It is not part of testenv on purpose: testenv imports vault and pcloud,
// whose own tests need rclone too and would then import themselves. For
// the same reason prereq's own tests cannot use it.
package testrclone

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/burnersen/CryptBridge/windows/internal/prereq"
)

// EnvVar names an rclone.exe for the tests; when it is set, nothing else
// is searched.
const EnvVar = "CRYPTBRIDGE_TEST_RCLONE"

// Path returns the rclone.exe for a test. It looks at CRYPTBRIDGE_TEST_RCLONE
// (only there when it is set), at %LOCALAPPDATA%\CryptBridge\bin\rclone.exe
// (where CryptBridge downloads it) and on the PATH, and takes the first with
// exactly prereq.RcloneVersion. Without any rclone the test is skipped; with
// other versions only it fails and says how to fix that.
func Path(t testing.TB) string {
	t.Helper()
	path, others := find()
	if path != "" {
		return path
	}
	if len(others) == 0 {
		t.Skipf("rclone not found (%s, CryptBridge's folder, PATH)", EnvVar)
	}
	t.Fatalf("the tests need exactly rclone %s, found only: %s.\n"+
		"Fix: download rclone-v%s-windows-amd64.zip from https://downloads.rclone.org/v%s/ and set %s "+
		"to the rclone.exe in it, or let CryptBridge download rclone (it goes to %%LOCALAPPDATA%%\\CryptBridge\\bin).",
		prereq.RcloneVersion, strings.Join(others, ", "), prereq.RcloneVersion, prereq.RcloneVersion, EnvVar)
	return ""
}

// Lookup is Path for tests that also have something to check without
// rclone: it returns "" instead of skipping or failing.
func Lookup() string {
	path, _ := find()
	return path
}

// find returns the first rclone.exe with exactly prereq.RcloneVersion, or
// the places and versions of the others it saw.
func find() (path string, others []string) {
	for _, candidate := range candidates() {
		if _, err := os.Stat(candidate); err != nil {
			if os.Getenv(EnvVar) != "" {
				others = append(others, fmt.Sprintf("%s=%s (does not exist)", EnvVar, candidate))
			}
			continue
		}
		version, err := prereq.ReadRcloneVersion(candidate)
		if err != nil {
			others = append(others, fmt.Sprintf("%s (does not start: %v)", candidate, err))
			continue
		}
		if version == prereq.RcloneVersion {
			return candidate, nil
		}
		others = append(others, fmt.Sprintf("%s (%s)", candidate, version))
	}
	return "", others
}

func candidates() []string {
	if chosen := os.Getenv(EnvVar); chosen != "" {
		return []string{chosen}
	}
	var list []string
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		list = append(list, filepath.Join(local, "CryptBridge", "bin", "rclone.exe"))
	}
	if onPath, err := exec.LookPath("rclone"); err == nil {
		list = append(list, onPath)
	}
	return list
}
