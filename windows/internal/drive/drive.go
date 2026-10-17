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

// QuietDirCacheTime replaces DirCacheTime for clouds that report no changes
// to rclone, such as MEGA: changes made elsewhere show after this time
// instead of a day. rclone keeps MEGA's folder tree in memory and up to
// date, so looking again costs no network (rclone 1.75.2, backend/mega).
const QuietDirCacheTime = "1m"

// Values for a vault in a folder of this PC. Reading from a local disk is
// fast, so only files being written pass through the cache, and they leave
// it soon after: the cache holds them decrypted.
const (
	LocalCacheMode    = "writes"
	LocalCacheMaxAge  = "1m"  // a written file leaves the cache a minute after its last use
	LocalDirCacheTime = "10s" // local disks report no changes: look again soon
)

// Settings describe one drive.
type Settings struct {
	Remote            string   // rclone remote of the vault, e.g. "cbvault:"
	Letter            string   // drive letter without colon, e.g. "Z"
	VolumeName        string   // name shown in Explorer
	CacheMaxSize      string   // e.g. "20G"; a soft limit, files in use stay
	CacheMinFreeSpace string   // emergency brake: keep this much disk space free
	Local             bool     // the vault is a folder of this PC, not in pCloud
	NoChangeReports   bool     // the cloud does not report changes made elsewhere
	Hide              []string // names in the top folder that the drive does not show
}

// hideFilter returns rclone's filter that hides the names of Hide in the top
// folder of the drive only; files of the same name in subfolders stay. The
// filter applies to this drive alone: other calls to the same rclone still
// see the files (measured with rclone 1.75.1).
func (s Settings) hideFilter() map[string]any {
	rules := make([]string, len(s.Hide))
	for i, name := range s.Hide {
		rules[i] = "/" + escapeFilterName(name)
	}
	return map[string]any{"ExcludeRule": rules}
}

// escapeFilterName makes rclone take every character of a name literally,
// not as a pattern.
func escapeFilterName(name string) string {
	var escaped strings.Builder
	for _, r := range name {
		if strings.ContainsRune(`\*?[]{}`, r) {
			escaped.WriteRune('\\')
		}
		escaped.WriteRune(r)
	}
	return escaped.String()
}

// vfsOptions returns rclone's file system options for the drive.
func (s Settings) vfsOptions() map[string]any {
	if s.Local {
		return map[string]any{
			"CacheMode":         LocalCacheMode,
			"CacheMaxAge":       LocalCacheMaxAge,
			"CacheMaxSize":      s.CacheMaxSize,
			"CacheMinFreeSpace": s.CacheMinFreeSpace,
			"DirCacheTime":      LocalDirCacheTime,
		}
	}
	dirCacheTime := DirCacheTime
	if s.NoChangeReports {
		dirCacheTime = QuietDirCacheTime
	}
	return map[string]any{
		"CacheMode":         CacheMode,
		"CacheMaxSize":      s.CacheMaxSize,
		"CacheMinFreeSpace": s.CacheMinFreeSpace,
		"ChunkSize":         ChunkSize,
		"ChunkSizeLimit":    ChunkSizeLimit,
		"ReadAhead":         ReadAhead,
		"DirCacheTime":      dirCacheTime,
		"PollInterval":      PollInterval,
	}
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
		"vfsOpt":     s.vfsOptions(),
		"mountOpt": map[string]any{
			"VolumeName":  s.VolumeName,
			"NetworkMode": true,
		},
	}
	if len(s.Hide) > 0 {
		request["_filter"] = s.hideFilter()
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
//
// rclone's count of open files ("inUse") is deliberately not used: Windows
// keeps a written file open in its own cache long after the upload has
// finished (measured: still 1 after 12 s), so it cannot tell whether
// anything is really going on.
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

	transfers, err := readTransfers(ctx, client)
	if err != nil {
		return status, err
	}
	status.Transfers = transfersOf(transfers, remote)
	return status, nil
}

// remoteTransfer is a transfer as rclone reports it, with the remotes it
// goes between (measured with rclone 1.75: an upload from the cache has
// dstFs "cbvault:").
type remoteTransfer struct {
	Transfer
	SrcFs string `json:"srcFs"`
	DstFs string `json:"dstFs"`
}

// readTransfers lists every running transfer of all drives.
func readTransfers(ctx context.Context, client *rcd.Client) ([]remoteTransfer, error) {
	var coreStats struct {
		Transferring []remoteTransfer `json:"transferring"`
	}
	if err := client.Call(ctx, "core/stats", nil, &coreStats); err != nil {
		return nil, err
	}
	return coreStats.Transferring, nil
}

// transfersOf picks the transfers to or from one drive's remote.
func transfersOf(all []remoteTransfer, remote string) []Transfer {
	mine := []Transfer{}
	for _, t := range all {
		if t.DstFs == remote || t.SrcFs == remote {
			mine = append(mine, t.Transfer)
		}
	}
	return mine
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

// NormaliseLetter turns "z", "Z:" or "Z:\" into "Z" and rejects anything
// that is not a usable drive letter.
func NormaliseLetter(letter string) (string, error) {
	return normaliseLetter(letter)
}

// normaliseLetter is NormaliseLetter. A and B are reserved for floppy
// drives and C is the system drive in practice.
func normaliseLetter(letter string) (string, error) {
	cleaned := strings.ToUpper(strings.TrimRight(strings.TrimSpace(letter), `:\`))
	if len(cleaned) != 1 || cleaned[0] < 'D' || cleaned[0] > 'Z' {
		return "", errors.New("drive letter must be one of D to Z")
	}
	return cleaned, nil
}
