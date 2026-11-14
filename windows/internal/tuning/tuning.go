// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

// Package tuning holds the rclone values that decide how a cloud drive
// reads, caches and uploads: one set per kind of use (profile), and the
// checks for values the user types in.
package tuning

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// The profiles a vault can use. A vault without a profile uses Films: these
// are the values every drive had before profiles existed.
const (
	Films     = "films"
	Mixed     = "mixed"
	Documents = "documents"
	Custom    = "custom" // the user's own values
)

// DefaultBufferSize is rclone's memory buffer per open file
// (--buffer-size), one value for all drives.
const DefaultBufferSize = "128M"

// Values are the rclone options of one cloud drive that depend on its use.
// Sizes are written like "64M", times like "30s" or "7d".
type Values struct {
	ChunkSize      string `json:"chunkSize"`      // --vfs-read-chunk-size: size of the first read request
	ChunkSizeLimit string `json:"chunkSizeLimit"` // --vfs-read-chunk-size-limit: read requests grow up to this size
	ReadAhead      string `json:"readAhead"`      // --vfs-read-ahead: read this far ahead into the cache
	ChunkStreams   int    `json:"chunkStreams"`   // --vfs-read-chunk-streams: parallel downloads of one file; 0: off
	WriteBack      string `json:"writeBack"`      // --vfs-write-back: wait this long after the last use before uploading
	CacheMaxAge    string `json:"cacheMaxAge"`    // --vfs-cache-max-age: a file leaves the cache this long after its last use
}

// Profile is a named set of values for one kind of use.
type Profile struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Use    string `json:"use"` // what it is good for, in a sentence
	Values Values `json:"values"`
}

// profiles lists the fixed profiles. Films must stay exactly as it is: it is
// what existing vaults use, and the Linux server mounts with the same values.
var profiles = []Profile{
	{
		ID:   Films,
		Name: "Films and large files",
		Use: "For streaming films, also in 4K, and for large files that are read from start to end. " +
			"Reads far ahead, so playback does not stutter.",
		Values: Values{ChunkSize: "64M", ChunkSizeLimit: "1G", ReadAhead: "128M", ChunkStreams: 0, WriteBack: "5s", CacheMaxAge: "1h"},
	},
	{
		ID:   Mixed,
		Name: "Mixed",
		Use: "For a vault with a bit of everything: photos, music, documents and a film now and then. " +
			"Films still play well, and opening a file fetches less than with the film profile.",
		Values: Values{ChunkSize: "32M", ChunkSizeLimit: "512M", ReadAhead: "32M", ChunkStreams: 0, WriteBack: "15s", CacheMaxAge: "24h"},
	},
	{
		ID:   Documents,
		Name: "Documents and small files",
		Use: "For texts, spreadsheets, photos and other small files you open and save often. " +
			"Fetches little more than what a program asks for, waits for the last save before uploading, " +
			"and keeps used files on this PC for a week so that they open at once.",
		Values: Values{ChunkSize: "8M", ChunkSizeLimit: "128M", ReadAhead: "8M", ChunkStreams: 0, WriteBack: "30s", CacheMaxAge: "7d"},
	},
}

// Profiles returns the fixed profiles in the order the window shows them.
func Profiles() []Profile {
	return append([]Profile(nil), profiles...)
}

// Of returns the values of a profile; custom are the user's own values,
// used with the profile Custom. An empty or unknown profile gives Films, so
// that a settings file from a newer CryptBridge still connects.
func Of(profile string, custom Values) Values {
	if profile == Custom {
		return custom
	}
	for _, p := range profiles {
		if p.ID == profile {
			return p.Values
		}
	}
	return profiles[0].Values
}

// Known reports whether profile is one a vault can be set to.
func Known(profile string) bool {
	if profile == Custom {
		return true
	}
	for _, p := range profiles {
		if p.ID == profile {
			return true
		}
	}
	return false
}

// Limits of the values. They keep typing mistakes from making a drive
// unusable or from eating the PC's memory.
const (
	minChunkSize    = 1 << 20   // 1M: smaller requests only add round trips to the cloud
	maxChunkSize    = 1 << 30   // 1G
	maxChunkLimit   = 16 << 30  // 16G
	maxReadAhead    = 4 << 30   // 4G
	maxChunkStreams = 16        // rclone's own suggestion for the fastest stores
	maxStreamMemory = 512 << 20 // 512M: parallel downloads hold their chunks in memory
	maxBufferSize   = 1 << 30   // 1G per open file
	minWriteBack    = time.Second
	maxWriteBack    = time.Hour // a file that waits longer is too easily forgotten in the cache
	minCacheMaxAge  = time.Minute
	maxCacheMaxAge  = 365 * 24 * time.Hour
)

var (
	sizePattern     = regexp.MustCompile(`^([1-9][0-9]{0,5})([KMG])$`)
	durationPattern = regexp.MustCompile(`^([1-9][0-9]{0,5})([smhd])$`)
)

// parseSize reads a size such as "64M" in bytes. Like rclone, K, M and G
// count in steps of 1024. With zeroOK, "0" is a size too.
func parseSize(text string, zeroOK bool) (int64, bool) {
	if zeroOK && text == "0" {
		return 0, true
	}
	m := sizePattern.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	n, _ := strconv.ParseInt(m[1], 10, 64)
	switch m[2] {
	case "K":
		return n << 10, true
	case "M":
		return n << 20, true
	default:
		return n << 30, true
	}
}

// parseDuration reads a time such as "30s", "5m", "24h" or "7d".
func parseDuration(text string) (time.Duration, bool) {
	m := durationPattern.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	n, _ := strconv.ParseInt(m[1], 10, 64)
	unit := map[string]time.Duration{"s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}[m[2]]
	return time.Duration(n) * unit, true
}

// Check reports the first value that rclone could not use or that is out of
// its sensible range, in words for the window.
func (v Values) Check() error {
	chunk, ok := parseSize(v.ChunkSize, false)
	if !ok || chunk < minChunkSize || chunk > maxChunkSize {
		return errors.New(`the first read size must be between 1M and 1G, written like "64M"`)
	}
	limit, ok := parseSize(v.ChunkSizeLimit, false)
	if !ok || limit < chunk || limit > maxChunkLimit {
		return errors.New(`the largest read size must be at least the first read size and at most 16G, written like "1G"`)
	}
	if ahead, ok := parseSize(v.ReadAhead, true); !ok || ahead > maxReadAhead {
		return errors.New(`read ahead must be between 0 and 4G, written like "128M"`)
	}
	if v.ChunkStreams < 0 || v.ChunkStreams > maxChunkStreams {
		return fmt.Errorf("parallel downloads per file must be between 0 and %d", maxChunkStreams)
	}
	if int64(v.ChunkStreams)*chunk > maxStreamMemory {
		return fmt.Errorf("parallel downloads keep their parts in memory: %d times %s is more than 512M per open file; "+
			"choose fewer downloads or a smaller first read size", v.ChunkStreams, v.ChunkSize)
	}
	if wait, ok := parseDuration(v.WriteBack); !ok || wait < minWriteBack || wait > maxWriteBack {
		return errors.New(`the wait before uploading must be between 1s and 1h, written like "5s" or "2m"`)
	}
	if age, ok := parseDuration(v.CacheMaxAge); !ok || age < minCacheMaxAge || age > maxCacheMaxAge {
		return errors.New(`the time files stay in the cache must be between 1m and 365d, written like "1h" or "7d"`)
	}
	return nil
}

// CheckBufferSize checks the memory buffer per open file.
func CheckBufferSize(size string) error {
	if bytes, ok := parseSize(size, true); !ok || bytes > maxBufferSize {
		return errors.New(`the memory buffer must be between 0 and 1G, written like "128M"`)
	}
	return nil
}
