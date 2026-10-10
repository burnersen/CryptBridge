// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package vault

import (
	"bytes"
	"context"
	"path"
	"sort"
	"strings"

	"github.com/burnersen/CryptBridge/windows/internal/rcd"
)

// fileMagic is how every file encrypted by rclone crypt begins (measured).
var fileMagic = []byte("RCLONE\x00\x00")

// encryptedShare is the share of entries in a folder that must look
// encrypted before the folder counts as a vault. A little tolerance allows
// for stray system files such as desktop.ini.
const encryptedShare = 0.9

// maxBlocks covers names up to rclone's limit of 255 characters.
const maxBlocks = 16

// Candidate is a folder in pCloud that looks like a vault.
type Candidate struct {
	Path      string `json:"path"`      // folder in pCloud
	Entries   int    `json:"entries"`   // entries directly inside
	Confirmed bool   `json:"confirmed"` // a file inside starts with rclone's mark
}

// Detect scans all of pCloud below baseRemote for folders that look like
// rclone crypt vaults. It only lists names and reads 8 bytes of one file per
// candidate; it never downloads files.
func Detect(ctx context.Context, client *rcd.Client, baseRemote string) ([]Candidate, error) {
	items, err := listDir(ctx, client, baseRemote, "", true)
	if err != nil {
		return nil, err
	}

	children := map[string][]listItem{}
	for _, item := range items {
		parent := path.Dir(item.Path)
		if parent == "." {
			parent = ""
		}
		children[parent] = append(children[parent], item)
	}

	looksLikeVault := map[string]bool{}
	for dir, entries := range children {
		looksLikeVault[dir] = mostlyEncrypted(entries)
	}

	var candidates []Candidate
	for dir, isVault := range looksLikeVault {
		// Only the top folder of a vault counts: below it, encrypted folder
		// names make every subfolder look like a vault of its own.
		if !isVault || dir == "" || looksLikeVault[parentOf(dir)] {
			continue
		}
		candidate := Candidate{Path: dir, Entries: len(children[dir])}
		candidate.Confirmed = hasMagic(ctx, client, baseRemote, children[dir])
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Path < candidates[j].Path })
	return candidates, nil
}

// hasMagic reads the first bytes of the first file in a folder.
func hasMagic(ctx context.Context, client *rcd.Client, baseRemote string, entries []listItem) bool {
	for _, entry := range entries {
		if entry.IsDir || entry.Size < int64(len(fileMagic)) {
			continue
		}
		head, err := client.ReadRange(ctx, baseRemote, entry.Path, 0, int64(len(fileMagic)))
		return err == nil && bytes.Equal(head, fileMagic)
	}
	return false
}

// parentOf returns the parent folder, with "" for the top level.
func parentOf(dir string) string {
	parent := path.Dir(dir)
	if parent == "." || parent == "/" {
		return ""
	}
	return parent
}

// mostlyEncrypted reports whether nearly all entries carry encrypted names.
func mostlyEncrypted(entries []listItem) bool {
	if len(entries) == 0 {
		return false
	}
	encrypted := 0
	for _, entry := range entries {
		if LooksEncrypted(entry.Name) {
			encrypted++
		}
	}
	return float64(encrypted) >= encryptedShare*float64(len(entries))
}

// LooksEncrypted reports whether a name has the shape of an rclone crypt
// name in standard mode (base32 or base64 encoding). Encrypted names are
// whole AES blocks of 16 bytes, so only certain lengths are possible.
func LooksEncrypted(name string) bool {
	switch {
	case isBase32Name(name):
		return validLength(len(name), 5)
	case isBase64Name(name):
		return validLength(len(name), 6)
	}
	return false
}

// validLength checks that n characters of bitsPerChar bits each encode a
// whole number of 16-byte blocks (without padding).
func validLength(n, bitsPerChar int) bool {
	for blocks := 1; blocks <= maxBlocks; blocks++ {
		bits := blocks * 16 * 8
		if n == (bits+bitsPerChar-1)/bitsPerChar {
			return true
		}
	}
	return false
}

// isBase32Name: rclone writes base32hex in lower case, letters a to v.
func isBase32Name(name string) bool {
	return name != "" && strings.Trim(name, "0123456789abcdefghijklmnopqrstuv") == ""
}

// isBase64Name: URL-safe base64 without padding. A name of only lower-case
// letters and digits is left to the base32 check.
func isBase64Name(name string) bool {
	if name == "" {
		return false
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	return strings.Trim(name, alphabet) == ""
}
