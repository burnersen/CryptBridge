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
	"strconv"
	"strings"
)

// MinRcloneVersion is the oldest rclone CryptBridge accepts. Everything was
// developed and measured with 1.75.1; older versions are not tested.
const MinRcloneVersion = "1.75.0"

// WinFspDownloadURL is the official download page of WinFsp.
const WinFspDownloadURL = "https://winfsp.dev/rel/"

// ErrRcloneNotFound means no usable rclone.exe was found.
var ErrRcloneNotFound = errors.New("rclone was not found")

// Rclone describes a usable rclone.exe.
type Rclone struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}

// FindRclone looks for rclone.exe in CryptBridge's own folder for
// downloaded tools, next to CryptBridge.exe, and on the PATH, in this order.
// The first one that is new enough wins.
func FindRclone(binDir string) (Rclone, error) {
	var candidates []string
	candidates = append(candidates, filepath.Join(binDir, "rclone.exe"))
	if self, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(self), "rclone.exe"))
	}
	if onPath, err := exec.LookPath("rclone"); err == nil {
		candidates = append(candidates, onPath)
	}

	var tooOld []string
	for _, path := range candidates {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		version, err := rcloneVersion(path)
		if err != nil {
			continue
		}
		if CompareVersions(version, MinRcloneVersion) >= 0 {
			return Rclone{Path: path, Version: version}, nil
		}
		tooOld = append(tooOld, fmt.Sprintf("%s (%s)", path, version))
	}
	if len(tooOld) > 0 {
		return Rclone{}, fmt.Errorf("%w: only older versions found: %s; at least %s is needed",
			ErrRcloneNotFound, strings.Join(tooOld, ", "), MinRcloneVersion)
	}
	return Rclone{}, ErrRcloneNotFound
}

// versionLine matches the first line of "rclone version", e.g. "rclone v1.75.1".
var versionLine = regexp.MustCompile(`rclone v(\d+\.\d+\.\d+)`)

// rcloneVersion runs "rclone version" without a console window.
func rcloneVersion(path string) (string, error) {
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

// CompareVersions compares two versions "x.y.z" number by number (as text,
// "1.9.0" would wrongly count as newer than "1.10.0"). Missing or broken
// parts count as 0.
func CompareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		na, nb := part(pa, i), part(pb, i)
		if na != nb {
			if na < nb {
				return -1
			}
			return 1
		}
	}
	return 0
}

func part(parts []string, i int) int {
	if i >= len(parts) {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
	if err != nil {
		return 0
	}
	return n
}
