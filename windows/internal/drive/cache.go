// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package drive

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
)

// UploadsWaitingError is returned by ClearCache when files in the cache are
// not in pCloud yet; nothing is deleted then.
type UploadsWaitingError struct {
	Count int
}

func (e *UploadsWaitingError) Error() string {
	return fmt.Sprintf("%d file(s) in the cache still wait for upload", e.Count)
}

// ClearCache deletes the cache of the drive of remoteName below cacheDir.
// rclone keeps that cache decrypted — names and contents of recently used
// files in plain text — so a locked CryptBridge removes it.
//
// Files that still wait for upload are never deleted: rclone marks them
// "Dirty" in its metadata (cacheDir\vfsMeta\<remote>\..., measured with
// rclone 1.75). If any file is marked, or a marker cannot be read, nothing
// is deleted and an *UploadsWaitingError is returned. rclone must not be
// running.
func ClearCache(cacheDir, remoteName string) error {
	if !filepath.IsAbs(cacheDir) || remoteName == "" {
		return fmt.Errorf("refusing to clear the cache in %q", cacheDir)
	}
	dataDir := filepath.Join(cacheDir, "vfs", remoteName)
	metaDir := filepath.Join(cacheDir, "vfsMeta", remoteName)

	waiting, err := countWaitingUploads(metaDir)
	if err != nil {
		return err
	}
	if waiting > 0 {
		return &UploadsWaitingError{Count: waiting}
	}
	if err := os.RemoveAll(dataDir); err != nil {
		return fmt.Errorf("clear the cache: %w", err)
	}
	if err := os.RemoveAll(metaDir); err != nil {
		return fmt.Errorf("clear the cache: %w", err)
	}
	return nil
}

// CacheRemotes lists the remotes that have a cache below cacheDir, also
// those of vaults CryptBridge no longer knows.
func CacheRemotes(cacheDir string) ([]string, error) {
	found := map[string]bool{}
	for _, sub := range []string{"vfs", "vfsMeta"} {
		entries, err := os.ReadDir(filepath.Join(cacheDir, sub))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("look into the cache: %w", err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				found[entry.Name()] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(found)), nil
}

// WaitingUploads counts the files in the cache of remoteName that are not
// written into the vault yet. It only reads.
func WaitingUploads(cacheDir, remoteName string) (int, error) {
	if !filepath.IsAbs(cacheDir) || remoteName == "" {
		return 0, fmt.Errorf("refusing to look into the cache in %q", cacheDir)
	}
	return countWaitingUploads(filepath.Join(cacheDir, "vfsMeta", remoteName))
}

// countWaitingUploads counts metadata files that mark a file as not yet
// uploaded. A marker that cannot be read counts as waiting: deleting a file
// that was never uploaded would lose it for good.
func countWaitingUploads(metaDir string) (int, error) {
	waiting := 0
	err := filepath.WalkDir(metaDir, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == metaDir {
			return filepath.SkipDir // no cache yet
		}
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !markedClean(path) {
			waiting++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("check the cache: %w", err)
	}
	return waiting, nil
}

// markedClean reports whether a metadata file says its file is uploaded.
func markedClean(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var meta struct {
		Dirty *bool `json:"Dirty"`
	}
	if json.Unmarshal(data, &meta) != nil || meta.Dirty == nil {
		return false
	}
	return !*meta.Dirty
}
