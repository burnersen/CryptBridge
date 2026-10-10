// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

//go:build windows

package drive

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// letterInUse reports whether Windows already uses the drive letter (a disk,
// a USB stick, a mapped network drive or another mount).
func letterInUse(letter string) (bool, error) {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return false, fmt.Errorf("list drive letters: %w", err)
	}
	bit := uint32(1) << (letter[0] - 'A')
	return mask&bit != 0, nil
}

// FreeLetters lists the drive letters from D to Z that are not in use.
func FreeLetters() ([]string, error) {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil, fmt.Errorf("list drive letters: %w", err)
	}
	var free []string
	for c := byte('D'); c <= 'Z'; c++ {
		if mask&(uint32(1)<<(c-'A')) == 0 {
			free = append(free, string(c))
		}
	}
	return free, nil
}
