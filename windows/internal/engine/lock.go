// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/drive"
	"github.com/burnersen/CryptBridge/windows/internal/passlock"
	"github.com/burnersen/CryptBridge/windows/internal/rcd"
	"github.com/burnersen/CryptBridge/windows/internal/settings"
	"github.com/burnersen/CryptBridge/windows/internal/vault"
)

// The app lock: with an app password set, CryptBridge starts locked and
// locks again when Windows is locked or the user signs out. Locked means
// that nothing readable is left behind:
//
//   - sign-in, vault locations and vault keys exist only sealed with the
//     app password,
//   - the drives are closed (after their uploads are done),
//   - rclone is stopped: it held the keys, and its control API can be
//     reached by anyone in the Windows session,
//   - the cache, which rclone keeps decrypted, and rclone's log are deleted.

// lockedLogLevel: with the app lock on, rclone logs only warnings and
// errors; at INFO it writes every file name in plain text.
const lockedLogLevel = "NOTICE"

// maxUnlockPause is the longest wait after wrong app passwords.
const maxUnlockPause = 30 * time.Second

var errLocked = errors.New("CryptBridge is locked; enter the app password first")

// isLocked reports whether the app password is needed first.
func (e *Engine) isLocked() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.locked
}

// saveSettings writes the settings, sealed with the key of the app password
// while the app lock is on.
func (e *Engine) saveSettings(s settings.Settings) error {
	e.mu.Lock()
	key := e.key
	e.mu.Unlock()
	return settings.Save(e.paths.SettingsFile, s, key)
}

// startLockedLocked is the start with the app lock on. An rclone that
// outlived a crashed window still holds the keys and maybe drives: they are
// closed as if the app had just been locked, and connect again after
// unlocking.
func (e *Engine) startLockedLocked(ctx context.Context) {
	client, err := rcd.Attach(ctx, e.paths.StateFile)
	if err != nil {
		e.setPhase(PhaseLocked, "")
		return
	}
	s := e.currentSettings()
	e.mu.Lock()
	e.client = client
	e.closing = true
	e.mu.Unlock()
	for _, v := range s.Vaults {
		if status, err := drive.ReadStatus(ctx, client, remoteOf(v), v.Letter); err == nil && status.Mounted {
			e.mu.Lock()
			if !slices.Contains(e.unlockConnects, v.ID) {
				e.unlockConnects = append(e.unlockConnects, v.ID)
			}
			e.runtimeLocked(v.ID).status = status
			e.mu.Unlock()
		}
	}
	e.setPhase(PhaseLocked, "")
	e.finishLockLocked(ctx, false)
}

// Lock locks the app. Sign-in and keys leave CryptBridge's memory at once;
// the drives close right away if nothing is uploading, otherwise as soon as
// the uploads are done (Windows is locked in the meantime anyway).
func (e *Engine) Lock(ctx context.Context) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	return e.lockLocked(ctx, false)
}

// LockNow locks without waiting for uploads, for signing out of Windows or
// shutting down, when there is no time to wait. Waiting uploads stay in the
// cache and continue after the next unlock.
func (e *Engine) LockNow(ctx context.Context) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	return e.lockLocked(ctx, true)
}

func (e *Engine) lockLocked(ctx context.Context, now bool) error {
	e.mu.Lock()
	if !e.settings.AppLock {
		e.mu.Unlock()
		return errors.New("set an app password first (Settings → App lock)")
	}
	if !e.locked {
		e.unlockConnects = nil
		for _, v := range e.settings.Vaults {
			if e.runtimeLocked(v.ID).want {
				e.unlockConnects = append(e.unlockConnects, v.ID)
			}
		}
	}
	e.locked = true
	e.closing = true
	for _, rt := range e.vaults {
		rt.want = false // no reconnects while locked
		rt.fingerprint = ""
	}
	e.stopCountsLocked()
	e.key.Wipe()
	e.key = nil
	e.settings = e.settings.WithoutSecrets()
	e.usage = nil
	e.signInProblem = ""
	e.mu.Unlock()

	e.setPhase(PhaseLocked, "")
	e.finishLockLocked(ctx, now)
	return nil
}

// finishLockLocked closes the drives, stops rclone and deletes the cache
// once no upload is running; until then everything keeps running, and the
// monitor calls it again. With now, it does not wait for uploads.
func (e *Engine) finishLockLocked(ctx context.Context, now bool) {
	e.mu.Lock()
	client := e.client
	e.mu.Unlock()
	s := e.currentSettings()

	if client != nil {
		if !now {
			if err := e.ensureNoUploadsAnywhere(ctx, s.Vaults); err != nil {
				e.refreshStatusLocked(ctx, client, s)
				e.setPhase(PhaseLocked, "The drives close as soon as the uploads are done ("+err.Error()+").")
				return
			}
		}
		for _, v := range s.Vaults {
			status, err := drive.ReadStatus(ctx, client, remoteOf(v), v.Letter)
			if err != nil || !status.Mounted {
				continue
			}
			if err := drive.Unmount(ctx, client, v.Letter); err != nil {
				e.setPhase(PhaseLocked, fmt.Sprintf("The drive %s could not be closed yet (%v); CryptBridge tries again.", describe(v), err))
				return
			}
		}
		// Forget the keys even if stopping rclone should fail.
		for _, v := range s.Vaults {
			_ = vault.Remove(ctx, client, remoteName(v.ID))
		}
		_ = vault.Remove(ctx, client, pcloudRemote)
		if err := rcd.Stop(ctx, client, e.paths.StateFile); err != nil {
			e.setPhase(PhaseLocked, "rclone could not be stopped yet ("+err.Error()+"); CryptBridge tries again.")
			return
		}
		e.mu.Lock()
		e.client = nil
		e.mu.Unlock()
	}

	message := e.wipeTraces()
	e.mu.Lock()
	e.closing = false
	for _, rt := range e.vaults {
		rt.state = StateDisconnected
		rt.message = ""
		rt.status = drive.Status{}
		rt.folder = drive.FolderID{}
		rt.space = nil
	}
	e.mu.Unlock()
	e.setPhase(PhaseLocked, message)
}

// refreshStatusLocked reads the status of every drive, so that a locked
// window can show the uploads it waits for.
func (e *Engine) refreshStatusLocked(ctx context.Context, client *rcd.Client, s settings.Settings) {
	for _, v := range s.Vaults {
		if status, err := drive.ReadStatus(ctx, client, remoteOf(v), v.Letter); err == nil {
			e.update(v.ID, func(rt *vaultRuntime) { rt.status = status })
		}
	}
}

// wipeTraces deletes what rclone left readable on this PC: the decrypted
// cache of every vault — also of vaults CryptBridge no longer knows — and
// the log. rclone must not be running. It returns a note for the user when
// files had to stay, "" otherwise.
func (e *Engine) wipeTraces() string {
	e.mu.Lock()
	cacheDir := e.settings.Cache.Dir
	e.mu.Unlock()

	_ = os.Remove(e.paths.RcloneLog)
	_ = os.Remove(e.paths.RcloneLog + ".old")

	remotes, err := drive.CacheRemotes(cacheDir)
	if err != nil {
		return "The cache could not be cleared: " + err.Error()
	}
	waiting := 0
	for _, remote := range remotes {
		var stillWaiting *drive.UploadsWaitingError
		switch err := drive.ClearCache(cacheDir, remote); {
		case errors.As(err, &stillWaiting):
			waiting += stillWaiting.Count
		case err != nil:
			return "The cache could not be cleared: " + err.Error()
		}
	}
	if waiting > 0 {
		return fmt.Sprintf("%d file(s) wait in the cache and are written into their vaults after unlocking.", waiting)
	}
	return ""
}

// Unlock opens the secrets with the app password and continues where the
// lock stopped: rclone starts again, and the drives connect that were
// connected before locking (after the start: those that connect on start).
func (e *Engine) Unlock(ctx context.Context, password string) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	if !e.isLocked() {
		return nil
	}
	unlocked, key, err := e.openSettingsLocked(password)
	if err != nil {
		return err
	}

	e.mu.Lock()
	e.settings = unlocked
	e.key = key
	e.locked = false
	e.closing = false // drives that were still uploading simply stay
	connect := e.unlockConnects
	e.unlockConnects = nil
	e.mu.Unlock()
	e.lastUsage = time.Time{}

	if settings.IsLegacy(e.paths.SettingsFile) {
		// The first unlock after the update writes the new format.
		if err := e.saveSettings(unlocked); err != nil {
			e.setPhase(PhaseError, "Your settings could not be converted for this version: "+err.Error())
			return nil
		}
	}
	if err := e.prepareLocked(ctx); err != nil {
		e.setPhase(PhaseError, err.Error())
		return nil
	}
	e.settleLocked(ctx, false)
	for _, id := range connect {
		e.mu.Lock()
		connected := e.runtimeLocked(id).state == StateConnected
		e.mu.Unlock()
		if !connected {
			_ = e.connectVaultLocked(ctx, id) // problems show up at the drive
		}
	}
	return nil
}

// openSettingsLocked opens the sealed settings with the app password. To
// slow down guessing, each wrong password makes the next try wait longer:
// 2, 4, 8, 16, then 30 seconds.
func (e *Engine) openSettingsLocked(password string) (settings.Settings, *passlock.Key, error) {
	if wait := time.Until(e.nextUnlock); wait > 0 {
		return settings.Settings{}, nil, fmt.Errorf("too many wrong passwords; please wait %d seconds", int(wait.Round(time.Second).Seconds())+1)
	}
	unlocked, key, err := settings.Unlock(e.paths.SettingsFile, e.paths.CacheDir, password)
	if errors.Is(err, passlock.ErrWrongPassword) {
		e.unlockFailures++
		e.nextUnlock = time.Now().Add(unlockPause(e.unlockFailures))
		return settings.Settings{}, nil, errors.New("wrong app password")
	}
	if err != nil {
		return settings.Settings{}, nil, err
	}
	e.unlockFailures = 0
	e.nextUnlock = time.Time{}
	return unlocked, key, nil
}

// unlockPause is the wait after the given number of wrong passwords in a row.
func unlockPause(failures int) time.Duration {
	return min(time.Second<<min(failures, 5), maxUnlockPause)
}

// VerifyPassword asks for the app password again before the keys are shown
// (emergency sheet) or handed on (transfer block). Without app lock there
// is nothing to check.
func (e *Engine) VerifyPassword(password string) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	e.mu.Lock()
	appLock, locked := e.settings.AppLock, e.locked
	e.mu.Unlock()
	switch {
	case !appLock:
		return nil
	case locked:
		return errLocked
	}
	_, key, err := e.openSettingsLocked(password)
	key.Wipe()
	return err
}

// EnableLock sets the app password: from now on the secrets are stored
// sealed with it, and CryptBridge starts locked.
func (e *Engine) EnableLock(password string) error {
	key, err := passlock.NewKey(password)
	if err != nil {
		return err
	}
	e.actionMu.Lock()
	defer e.actionMu.Unlock()

	updated := e.currentSettings()
	e.mu.Lock()
	locked := e.locked
	e.mu.Unlock()
	switch {
	case locked:
		key.Wipe()
		return errLocked
	case updated.AppLock:
		key.Wipe()
		return errors.New("the app lock is already on; change the password instead")
	}
	updated.AppLock = true
	updated.LockOffered = true
	if err := settings.Save(e.paths.SettingsFile, updated, key); err != nil {
		key.Wipe()
		return err
	}
	e.mu.Lock()
	e.settings = updated
	e.key = key
	e.mu.Unlock()
	e.publish()
	return removeOldBackup(e.paths.SettingsFile)
}

// removeOldBackup deletes the backup of the settings of CryptBridge 0.3
// when the protection of the secrets changes: it holds them protected the
// old way.
func removeOldBackup(path string) error {
	if err := settings.RemoveLegacyBackup(path); err != nil {
		return fmt.Errorf("the backup of your old settings (%s%s) could not be deleted; please delete it yourself: %w",
			path, settings.LegacyBackupSuffix, err)
	}
	return nil
}

// ChangePassword replaces the app password.
func (e *Engine) ChangePassword(oldPassword, newPassword string) error {
	newKey, err := passlock.NewKey(newPassword)
	if err != nil {
		return err
	}
	e.actionMu.Lock()
	defer e.actionMu.Unlock()

	current := e.currentSettings()
	e.mu.Lock()
	locked := e.locked
	e.mu.Unlock()
	switch {
	case locked:
		newKey.Wipe()
		return errLocked
	case !current.AppLock:
		newKey.Wipe()
		return errors.New("there is no app password to change")
	}
	_, oldKey, err := e.openSettingsLocked(oldPassword)
	oldKey.Wipe()
	if err != nil {
		newKey.Wipe()
		return err
	}
	if err := settings.Save(e.paths.SettingsFile, current, newKey); err != nil {
		newKey.Wipe()
		return err
	}
	e.mu.Lock()
	e.key.Wipe()
	e.key = newKey
	e.mu.Unlock()
	return removeOldBackup(e.paths.SettingsFile)
}

// DisableLock turns the app lock off after checking the password; the
// secrets are then protected by Windows (DPAPI) alone again.
func (e *Engine) DisableLock(password string) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()

	updated := e.currentSettings()
	e.mu.Lock()
	locked := e.locked
	e.mu.Unlock()
	switch {
	case locked:
		return errLocked
	case !updated.AppLock:
		return nil
	}
	_, key, err := e.openSettingsLocked(password)
	key.Wipe()
	if err != nil {
		return err
	}
	updated.AppLock = false
	if err := settings.Save(e.paths.SettingsFile, updated, nil); err != nil {
		return err
	}
	e.mu.Lock()
	e.settings = updated
	e.key.Wipe()
	e.key = nil
	e.mu.Unlock()
	e.publish()
	return nil
}

// DeclineLock remembers that the user does not want an app password now,
// so that CryptBridge does not offer it again.
func (e *Engine) DeclineLock() error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	if e.isLocked() {
		return errLocked
	}
	updated := e.currentSettings()
	updated.LockOffered = true
	if err := e.saveSettings(updated); err != nil {
		return err
	}
	e.mu.Lock()
	e.settings = updated
	e.mu.Unlock()
	e.publish()
	return nil
}

// Reset is the way out of a forgotten app password: CryptBridge forgets the
// pCloud sign-in, all vaults and the app password; cache and behaviour
// settings stay. The files of the vaults are not touched — with the
// emergency sheets every vault can be added again.
func (e *Engine) Reset(ctx context.Context) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()

	e.mu.Lock()
	client := e.client
	old := e.settings.Clone()
	e.mu.Unlock()
	fresh := old.WithoutSecrets()
	fresh.Vaults = nil
	fresh.AppLock = false
	fresh.LockOffered = false

	// Nothing may keep running with the old keys.
	if client != nil {
		for _, v := range old.Vaults {
			if status, err := drive.ReadStatus(ctx, client, remoteOf(v), v.Letter); err == nil && status.Mounted {
				_ = drive.Unmount(ctx, client, v.Letter)
			}
		}
		if err := rcd.Stop(ctx, client, e.paths.StateFile); err != nil {
			return fmt.Errorf("stop rclone: %w", err)
		}
	}
	if err := settings.Save(e.paths.SettingsFile, fresh, nil); err != nil {
		return err
	}

	e.mu.Lock()
	e.client = nil
	e.key.Wipe()
	e.key = nil
	e.settings = fresh
	e.locked, e.closing = false, false
	e.unlockConnects = nil
	e.vaults = map[string]*vaultRuntime{}
	e.usage, e.signInProblem = nil, ""
	e.mu.Unlock()
	e.wipeTraces()
	backupErr := removeOldBackup(e.paths.SettingsFile)

	if err := e.prepareLocked(ctx); err != nil {
		e.setPhase(PhaseError, err.Error())
		return backupErr
	}
	e.settleLocked(ctx, false)
	return backupErr
}
