// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

// Package prereq finds the two programs CryptBridge depends on: rclone
// (does the work) and WinFsp (lets Windows show a drive letter).
package prereq

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// RcloneVersion is the one rclone CryptBridge uses: everything is tested
// with exactly this version. linux/setup.sh pins the same one
// (pin_test.go checks that).
const RcloneVersion = "1.75.2"

// rcloneZipSHA256 holds the SHA-256 of rclone's official ZIP files of
// RcloneVersion, taken from rclone's PGP-signed SHA256SUMS list. A download
// counts only if it matches, whichever server it came from.
var rcloneZipSHA256 = map[string]string{
	"windows-amd64": "8a9db2bad1dc79a65b3348fd857069598e2aab7cf4d118b77b1df23055aff818",
	"windows-arm64": "7eb9844c9e5b723b9ef28821891caa17f6d9d1a375c49365cbe04114cec8dcd9",
}

// WinFspDownloadURL is the official download page of WinFsp.
const WinFspDownloadURL = "https://winfsp.dev/rel/"

// ErrRcloneNotFound means no usable rclone.exe was found.
var ErrRcloneNotFound = errors.New("rclone was not found")

// ErrRcloneOutdated means CryptBridge's own folder holds another rclone
// than RcloneVersion (or one that does not start). CryptBridge put it
// there, so it replaces it instead of using an rclone from elsewhere: the
// firewall rule of the LAN access names exactly this path.
var ErrRcloneOutdated = errors.New("CryptBridge's own rclone is not the version it needs")

// Rclone describes a usable rclone.exe.
type Rclone struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}

// FindRclone looks for rclone.exe in CryptBridge's own folder for
// downloaded tools, next to CryptBridge.exe, and on the PATH, in this order.
// Only exactly RcloneVersion counts. An rclone.exe in the own folder always
// wins: if it has another version, the answer is ErrRcloneOutdated.
func FindRclone(binDir string) (Rclone, error) {
	own := filepath.Join(binDir, "rclone.exe")
	if _, err := os.Stat(own); err == nil {
		version, err := ReadRcloneVersion(own)
		if err != nil {
			return Rclone{}, fmt.Errorf("%w: %s does not start", ErrRcloneOutdated, own)
		}
		if version != RcloneVersion {
			return Rclone{}, fmt.Errorf("%w: %s is rclone %s", ErrRcloneOutdated, own, version)
		}
		return Rclone{Path: own, Version: version}, nil
	}

	var candidates []string
	if self, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(self), "rclone.exe"))
	}
	if onPath, err := exec.LookPath("rclone"); err == nil {
		candidates = append(candidates, onPath)
	}

	var others []string
	for _, path := range candidates {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		version, err := ReadRcloneVersion(path)
		if err != nil {
			continue
		}
		if version == RcloneVersion {
			return Rclone{Path: path, Version: version}, nil
		}
		others = append(others, fmt.Sprintf("%s (%s)", path, version))
	}
	if len(others) > 0 {
		return Rclone{}, fmt.Errorf("%w: only other versions found: %s", ErrRcloneNotFound, strings.Join(others, ", "))
	}
	return Rclone{}, ErrRcloneNotFound
}

// versionLine matches the first line of "rclone version", e.g. "rclone v1.75.1".
var versionLine = regexp.MustCompile(`rclone v(\d+\.\d+\.\d+)`)

// ReadRcloneVersion runs "rclone version" without a console window and
// returns the version, e.g. "1.75.2".
func ReadRcloneVersion(path string) (string, error) {
	cmd := exec.Command(path, "version")
	cmd.SysProcAttr = hiddenWindow()
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	match := versionLine.FindStringSubmatch(string(out))
	if match == nil {
		return "", fmt.Errorf("unexpected version output of %s", path)
	}
	return match[1], nil
}
