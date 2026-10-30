// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package drive

import (
	"context"
	"fmt"
	"math"
	"strconv"

	"github.com/burnersen/CryptBridge/windows/internal/rcd"
)

// Uploads limits how the drives write into the clouds. It counts for all
// drives together, as they share one rclone. Downloads are never limited.
type Uploads struct {
	Parallel int     // files uploaded at the same time; below 1: rclone's default
	MaxMBps  float64 // upload speed in MB/s (1 MB = 1,000,000 bytes); 0 or less: no limit
}

// defaultParallel is rclone's own --transfers.
const defaultParallel = 4

func (u Uploads) parallel() int {
	if u.Parallel < 1 {
		return defaultParallel
	}
	return u.Parallel
}

// rate is rclone's bandwidth limit for uploads only ("upload:download"),
// in bytes per second. The limit applies where rclone talks to the cloud,
// so streaming from a drive or over the home network stays at full speed
// (rclone 1.75.2, fs/fshttp/dialer.go).
func (u Uploads) rate() string {
	bytesPerSecond := int64(math.Round(u.MaxMBps * 1e6))
	if bytesPerSecond <= 0 {
		return "off"
	}
	return strconv.FormatInt(bytesPerSecond, 10) + "B:off"
}

// Args returns the global rclone flags for a new rclone process.
func (u Uploads) Args() []string {
	return []string{"--transfers", strconv.Itoa(u.parallel()), "--bwlimit", u.rate()}
}

// Apply changes the limits of a running rclone. Both take effect at once,
// for connected drives too: the speed limit even for uploads already
// running, the number when the next upload starts. rclone's drives read
// --transfers each time they start an upload and keep no copy of their own
// (rclone 1.75.2, vfs/vfscache/writeback).
func (u Uploads) Apply(ctx context.Context, client *rcd.Client) error {
	if err := client.Call(ctx, "core/bwlimit", map[string]any{"rate": u.rate()}, nil); err != nil {
		return fmt.Errorf("set the upload speed: %w", err)
	}
	transfers := map[string]any{"main": map[string]any{"Transfers": u.parallel()}}
	if err := client.Call(ctx, "options/set", transfers, nil); err != nil {
		return fmt.Errorf("set the number of uploads: %w", err)
	}
	return nil
}
