// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

// Package drive mounts the vault as a Windows drive letter through the
// running rclone process and reports what the drive is doing.
package drive

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/burnersen/CryptBridge/windows/internal/rcd"
)

// Streaming defaults for stutter-free 4K playback. They are the project's
// required values and are shared with the Linux server setup.
const (
	BufferSize     = "128M" // RAM buffer per open file (global rclone flag)
	ChunkSize      = "64M"  // first read request: fast start of a film
	ChunkSizeLimit = "1G"   // read requests grow up to this size
	CacheMode      = "full" // cache reads and writes on disk
	ReadAhead      = "128M" // read ahead of the player in full cache mode
	DirCacheTime   = "24h"  // keep folder listings; pCloud reports changes
	PollInterval   = "1m"   // ask pCloud for changes every minute
)

// Settings describe one drive.
type Settings struct {
	Remote            string // rclone remote of the vault, e.g. "cbvault:"
	Letter            string // drive letter without colon, e.g. "Z"
	VolumeName        string // name shown in Explorer
	CacheMaxSize      string // e.g. "20G"; a soft limit, files in use stay
	CacheMinFreeSpace string // emergency brake: keep this much disk space free
}

// Mount mounts the vault. The drive letter must be free.
func Mount(ctx context.Context, client *rcd.Client, s Settings) error {
	letter, err := normaliseLetter(s.Letter)
	if err != nil {
		return err
	}
	if inUse, err := letterInUse(letter); err != nil {
		return err
	} else if inUse {
		return fmt.Errorf("drive %s: is already in use; disconnect it or choose another letter", letter)
	}

	request := map[string]any{
		"fs":         s.Remote,
		"mountPoint": letter + ":",
		"mountType":  "cmount",
		"vfsOpt": map[string]any{
			"CacheMode":         CacheMode,
			"CacheMaxSize":      s.CacheMaxSize,
			"CacheMinFreeSpace": s.CacheMinFreeSpace,
			"ChunkSize":         ChunkSize,
			"ChunkSizeLimit":    ChunkSizeLimit,
			"ReadAhead":         ReadAhead,
			"DirCacheTime":      DirCacheTime,
			"PollInterval":      PollInterval,
		},
		"mountOpt": map[string]any{
			"VolumeName":  s.VolumeName,
			"NetworkMode": true,
		},
	}
	if err := client.Call(ctx, "mount/mount", request, nil); err != nil {
		return fmt.Errorf("connect drive %s: %w", letter, err)
	}
	return nil
}

// Unmount removes the drive. Uploads that have not finished stay in the
// cache and continue the next time the drive is connected.
func Unmount(ctx context.Context, client *rcd.Client, letter string) error {
	letter, err := normaliseLetter(letter)
	if err != nil {
		return err
	}
	if err := client.Call(ctx, "mount/unmount", map[string]any{"mountPoint": letter + ":"}, nil); err != nil {
		return fmt.Errorf("disconnect drive %s: %w", letter, err)
	}
	return nil
}

// Transfer is one file that is being uploaded or downloaded right now.
type Transfer struct {
	Name       string  `json:"name"`
	Size       int64   `json:"size"`
	Bytes      int64   `json:"bytes"`
	Percentage int     `json:"percentage"`
	Speed      float64 `json:"speed"`
	ETA        *int64  `json:"eta"`
}

// Status is a snapshot of what the drive is doing.
type Status struct {
	Mounted           bool       `json:"mounted"`
	UploadsInProgress int        `json:"uploadsInProgress"`
	UploadsQueued     int        `json:"uploadsQueued"`
	CacheBytes        int64      `json:"cacheBytes"`
	CacheOutOfSpace   bool       `json:"cacheOutOfSpace"`
	CacheErroredFiles int        `json:"cacheErroredFiles"`
	Transfers         []Transfer `json:"transfers"`
}

// PendingUploads is the number of files that are not safely in pCloud yet.
func (s Status) PendingUploads() int {
	return s.UploadsInProgress + s.UploadsQueued
}

// ReadStatus collects the status of the drive with the given letter.
func ReadStatus(ctx context.Context, client *rcd.Client, remote, letter string) (Status, error) {
	var status Status
	mounted, err := isMounted(ctx, client, letter)
	if err != nil {
		return status, err
	}
	status.Mounted = mounted
	if !mounted {
		return status, nil
	}

	var vfsStats struct {
		DiskCache struct {
			BytesUsed         int64 `json:"bytesUsed"`
			ErroredFiles      int   `json:"erroredFiles"`
			OutOfSpace        bool  `json:"outOfSpace"`
			UploadsInProgress int   `json:"uploadsInProgress"`
			UploadsQueued     int   `json:"uploadsQueued"`
		} `json:"diskCache"`
	}
	if err := client.Call(ctx, "vfs/stats", map[string]any{"fs": remote}, &vfsStats); err != nil {
		return status, err
	}
	status.UploadsInProgress = vfsStats.DiskCache.UploadsInProgress
	status.UploadsQueued = vfsStats.DiskCache.UploadsQueued
	status.CacheBytes = vfsStats.DiskCache.BytesUsed
	status.CacheOutOfSpace = vfsStats.DiskCache.OutOfSpace
	status.CacheErroredFiles = vfsStats.DiskCache.ErroredFiles

	var coreStats struct {
		Transferring []Transfer `json:"transferring"`
	}
	if err := client.Call(ctx, "core/stats", nil, &coreStats); err != nil {
		return status, err
	}
	status.Transfers = coreStats.Transferring
	return status, nil
}

// isMounted asks rclone whether it has a drive on this letter.
func isMounted(ctx context.Context, client *rcd.Client, letter string) (bool, error) {
	letter, err := normaliseLetter(letter)
	if err != nil {
		return false, err
	}
	var answer struct {
		MountPoints []struct {
			MountPoint string `json:"MountPoint"`
		} `json:"mountPoints"`
	}
	if err := client.Call(ctx, "mount/listmounts", nil, &answer); err != nil {
		return false, err
	}
	for _, m := range answer.MountPoints {
		if strings.EqualFold(m.MountPoint, letter+":") {
			return true, nil
		}
	}
	return false, nil
}

// normaliseLetter turns "z", "Z:" or "Z:\" into "Z" and rejects anything
// that is not a usable drive letter. A and B are reserved for floppy drives
// and C is the system drive in practice.
func normaliseLetter(letter string) (string, error) {
	cleaned := strings.ToUpper(strings.TrimRight(strings.TrimSpace(letter), `:\`))
	if len(cleaned) != 1 || cleaned[0] < 'D' || cleaned[0] > 'Z' {
		return "", errors.New("drive letter must be one of D to Z")
	}
	return cleaned, nil
}
