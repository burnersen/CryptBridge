// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package vault

import (
	"context"
	"sort"
	"strings"

	"github.com/burnersen/CryptBridge/windows/internal/rcd"
)

// Folder describes a folder while the user chooses where a vault is.
type Folder struct {
	Path           string   `json:"path"`           // the folder, as asked for
	Exists         bool     `json:"exists"`         // false: it would be created by a new vault
	Entries        int      `json:"entries"`        // files and folders directly inside
	LooksLikeVault bool     `json:"looksLikeVault"` // nearly all names inside look encrypted
	Subfolders     []string `json:"subfolders"`     // names of the folders inside, sorted
}

// Inspect lists the folder dir on baseRemote (e.g. "cbpcloud:" or "E:/").
// Only names are read.
func Inspect(ctx context.Context, client *rcd.Client, baseRemote, dir string) (Folder, error) {
	folder := Folder{Path: dir, Subfolders: []string{}}
	items, err := listDir(ctx, client, baseRemote, dir, false)
	if isNotFound(err) {
		return folder, nil
	}
	if err != nil {
		return folder, err
	}
	folder.Exists = true
	folder.Entries = len(items)
	folder.LooksLikeVault = mostlyEncrypted(items)
	for _, item := range items {
		if item.IsDir {
			folder.Subfolders = append(folder.Subfolders, item.Name)
		}
	}
	sort.Slice(folder.Subfolders, func(i, j int) bool {
		return strings.ToLower(folder.Subfolders[i]) < strings.ToLower(folder.Subfolders[j])
	})
	return folder, nil
}

// EnclosingVault looks at every folder above dir, up to the top level, and
// returns the nearest one that looks like a vault. A vault inside another
// vault would be a folder of plain files among encrypted ones — or, opened
// with the same keys, a second drive on part of the same files.
func EnclosingVault(ctx context.Context, client *rcd.Client, baseRemote, dir string) (string, bool, error) {
	for parent := parentOf(dir); ; parent = parentOf(parent) {
		items, err := listDir(ctx, client, baseRemote, parent, false)
		switch {
		case isNotFound(err):
			// Not there yet: a new vault may create it.
		case err != nil:
			return "", false, err
		case mostlyEncrypted(items):
			return parent, true, nil
		}
		if parent == "" {
			return "", false, nil
		}
	}
}
