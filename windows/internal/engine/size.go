// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package engine

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/rcd"
	"github.com/burnersen/CryptBridge/windows/internal/settings"
)

// Counting the size of a vault means listing every file in it. rclone does
// that as a background job (operations/size with _async, measured with
// rclone 1.75.1); its progress is the number of entries listed so far.

const (
	// countPollEvery is how often a running count is looked at.
	countPollEvery = time.Second

	// localCountEvery: the size of a connected local vault is counted again
	// after this time. pCloud vaults are only counted when the user asks,
	// because listing terabytes in pCloud takes minutes.
	localCountEvery = 10 * time.Minute
)

// SizeProgress is a size count that is still running.
type SizeProgress struct {
	Job     int64  `json:"-"`
	Listed  int64  `json:"listed"`  // entries listed so far
	Started string `json:"started"` // RFC 3339
}

// CountSize starts counting the size of a vault in the background. The
// result is kept with the vault (settings.Vault.LastSize).
func (e *Engine) CountSize(ctx context.Context, id string) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	v, ok := e.currentSettings().Vault(id)
	if !ok {
		return errUnknownVault
	}
	client, err := e.clientForPlaceLocked(ctx, v)
	if err != nil {
		return err
	}
	return e.startCountLocked(ctx, client, v)
}

// CancelCount stops a running size count.
func (e *Engine) CancelCount(ctx context.Context, id string) error {
	e.mu.Lock()
	rt := e.runtimeLocked(id)
	progress := rt.counting
	rt.counting = nil
	client := e.client
	e.mu.Unlock()
	e.publish()
	if progress == nil || client == nil {
		return nil
	}
	return client.Call(ctx, "job/stop", map[string]any{"jobid": progress.Job}, nil)
}

// startCountLocked starts a count unless one is running already.
func (e *Engine) startCountLocked(ctx context.Context, client *rcd.Client, v settings.Vault) error {
	e.mu.Lock()
	running := e.runtimeLocked(v.ID).counting != nil
	e.mu.Unlock()
	if running {
		return nil
	}
	var started struct {
		JobID int64 `json:"jobid"`
	}
	if err := client.Call(ctx, "operations/size", map[string]any{"fs": remoteOf(v), "_async": true}, &started); err != nil {
		return fmt.Errorf("count the size: %w", err)
	}
	progress := &SizeProgress{Job: started.JobID, Started: time.Now().UTC().Format(time.RFC3339)}
	e.update(v.ID, func(rt *vaultRuntime) {
		rt.counting = progress
		rt.sizeError = ""
		rt.nextCount = time.Now().Add(localCountEvery)
	})
	e.publish()
	go e.followCount(client, v.ID, started.JobID)
	return nil
}

// countAutomaticallyLocked counts the size of a connected local vault now
// and then; a failure is shown and tried again next time.
func (e *Engine) countAutomaticallyLocked(ctx context.Context, client *rcd.Client, v settings.Vault) {
	e.mu.Lock()
	due := time.Now().After(e.runtimeLocked(v.ID).nextCount)
	e.mu.Unlock()
	if due {
		if err := e.startCountLocked(ctx, client, v); err != nil {
			e.update(v.ID, func(rt *vaultRuntime) {
				rt.sizeError = err.Error()
				rt.nextCount = time.Now().Add(localCountEvery)
			})
		}
	}
}

// jobStatus is rclone's answer to job/status for operations/size.
type jobStatus struct {
	Finished bool   `json:"finished"`
	Success  bool   `json:"success"`
	Error    string `json:"error"`
	Output   struct {
		Bytes int64 `json:"bytes"`
		Count int64 `json:"count"`
	} `json:"output"`
}

// followCount watches a running count until it ends, then keeps its result.
// It stops quietly when the count was cancelled or replaced, or CryptBridge
// was locked in the meantime.
func (e *Engine) followCount(client *rcd.Client, id string, job int64) {
	ticker := time.NewTicker(countPollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
		}
		if !e.stillCounting(id, job) {
			return
		}
		var status jobStatus
		if err := client.Call(e.ctx, "job/status", map[string]any{"jobid": job}, &status); err != nil {
			e.endCount(id, job, nil, "The count was interrupted: "+err.Error())
			return
		}
		if !status.Finished {
			var stats struct {
				Listed int64 `json:"listed"`
			}
			if client.Call(e.ctx, "core/stats", map[string]any{"group": fmt.Sprintf("job/%d", job)}, &stats) == nil {
				e.update(id, func(rt *vaultRuntime) {
					if rt.counting != nil && rt.counting.Job == job {
						rt.counting.Listed = stats.Listed
					}
				})
				e.publish()
			}
			continue
		}
		if !status.Success {
			e.endCount(id, job, nil, "The size could not be counted: "+status.Error)
			return
		}
		e.endCount(id, job, &settings.SizeRecord{
			Bytes: status.Output.Bytes,
			Files: status.Output.Count,
			At:    time.Now().UTC().Format(time.RFC3339),
		}, "")
		return
	}
}

func (e *Engine) stillCounting(id string, job int64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	rt, ok := e.vaults[id]
	return ok && !e.locked && rt.counting != nil && rt.counting.Job == job
}

// endCount ends a count and keeps its result with the vault.
func (e *Engine) endCount(id string, job int64, record *settings.SizeRecord, problem string) {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	if !e.stillCounting(id, job) {
		return
	}
	e.update(id, func(rt *vaultRuntime) {
		rt.counting = nil
		rt.sizeError = problem
	})
	if record != nil {
		s := e.currentSettings()
		if index := slices.IndexFunc(s.Vaults, func(v settings.Vault) bool { return v.ID == id }); index >= 0 {
			s.Vaults[index].LastSize = record
			if err := e.saveSettings(s); err != nil {
				e.update(id, func(rt *vaultRuntime) { rt.sizeError = "The size could not be saved: " + err.Error() })
			} else {
				e.mu.Lock()
				e.settings = s
				e.mu.Unlock()
			}
		}
	}
	e.publish()
}

// stopCountsLocked forgets every running count (when locking); rclone stops
// with them anyway. The caller holds mu.
func (e *Engine) stopCountsLocked() {
	for _, rt := range e.vaults {
		rt.counting = nil
	}
}
