// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
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
//   - sign-in and vault keys exist only sealed with the app password,
//   - the drive is closed (after its uploads are done),
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
// outlived a crashed window still holds the keys and maybe the drive: it is
// closed as if the app had just been locked.
func (e *Engine) startLockedLocked(ctx context.Context) {
	client, err := rcd.Attach(ctx, e.paths.StateFile)
	if err != nil {
		e.setPhase(PhaseLocked, "")
		return
	}
	e.mu.Lock()
	letter := e.settings.Drive.Letter
	e.client = client
	e.closing = true
	e.mu.Unlock()
	if status, err := drive.ReadStatus(ctx, client, vaultRemote+":", letter); err == nil && status.Mounted {
		e.mu.Lock()
		e.unlockConnects = true
		e.mu.Unlock()
	}
	e.setPhase(PhaseLocked, "")
	e.finishLockLocked(ctx, false)
}

// Lock locks the app. Sign-in and keys leave CryptBridge's memory at once;
// the drive closes right away if nothing is uploading, otherwise as soon as
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
		e.unlockConnects = e.wantConnected
	}
	e.locked = true
	e.closing = true
	e.wantConnected = false // no reconnects while locked
	e.key.Wipe()
	e.key = nil
	e.settings = e.settings.WithoutSecrets()
	e.usage = nil
	e.fingerprint = ""
	e.signInProblem = ""
	e.mu.Unlock()

	e.setPhase(PhaseLocked, "")
	e.finishLockLocked(ctx, now)
	return nil
}

// finishLockLocked closes the drive, stops rclone and deletes the cache once
// no upload is running; until then everything keeps running, and the
// monitor calls it again. With now, it does not wait for uploads.
func (e *Engine) finishLockLocked(ctx context.Context, now bool) {
	e.mu.Lock()
	client := e.client
	letter := e.settings.Drive.Letter
	e.mu.Unlock()

	if client != nil {
		status, err := drive.ReadStatus(ctx, client, vaultRemote+":", letter)
		if err == nil && status.Mounted {
			if !now {
				if err := e.ensureNoUploads(ctx, client, letter, status); err != nil {
					e.mu.Lock()
					e.driveStatus = status
					e.mu.Unlock()
					e.setPhase(PhaseLocked, "The drive closes as soon as the uploads are done ("+err.Error()+").")
					return
				}
			}
			if err := drive.Unmount(ctx, client, letter); err != nil {
				e.setPhase(PhaseLocked, "The drive could not be closed yet ("+err.Error()+"); CryptBridge tries again.")
				return
			}
		}
		// Forget the keys even if stopping rclone should fail.
		_ = vault.Remove(ctx, client, vaultRemote)
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
	e.driveStatus = drive.Status{}
	e.mu.Unlock()
	e.setPhase(PhaseLocked, message)
}

// wipeTraces deletes what rclone left readable on this PC: the decrypted
// cache and the log. rclone must not be running. It returns a note for the
// user when files had to stay, "" otherwise.
func (e *Engine) wipeTraces() string {
	e.mu.Lock()
	cacheDir := e.settings.Drive.CacheDir
	e.mu.Unlock()

	_ = os.Remove(e.paths.RcloneLog)
	_ = os.Remove(e.paths.RcloneLog + ".old")

	var waiting *drive.UploadsWaitingError
	switch err := drive.ClearCache(cacheDir, vaultRemote); {
	case errors.As(err, &waiting):
		return fmt.Sprintf("%d file(s) wait in the cache and are uploaded after unlocking.", waiting.Count)
	case err != nil:
		return "The cache could not be cleared: " + err.Error()
	}
	return ""
}

// Unlock opens the secrets with the app password and continues where the
// lock stopped: rclone starts again, and the drive connects if it was
// connected before locking (after the start: if "connect on start" is on).
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
	e.closing = false // a drive that was still uploading simply stays
	connect := e.unlockConnects
	e.unlockConnects = false
	e.mu.Unlock()
	e.lastUsage = time.Time{}

	if err := e.prepareLocked(ctx); err != nil {
		e.setPhase(PhaseError, err.Error())
		return nil
	}
	e.settlePhaseLocked(ctx, false)
	e.mu.Lock()
	disconnected := e.phase == PhaseDisconnected
	e.mu.Unlock()
	if connect && disconnected {
		if err := e.connectLocked(ctx); err != nil {
			e.setPhase(PhaseError, err.Error())
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

	e.mu.Lock()
	updated := e.settings
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

	e.mu.Lock()
	current := e.settings
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
	return nil
}

// DisableLock turns the app lock off after checking the password; the
// secrets are then protected by Windows (DPAPI) alone again.
func (e *Engine) DisableLock(password string) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()

	e.mu.Lock()
	updated := e.settings
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
	e.mu.Lock()
	updated := e.settings
	e.mu.Unlock()
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
// pCloud sign-in, the vault keys and the app password; drive settings stay.
// The files in pCloud are not touched — with password and salt from the
// emergency sheet the vault opens again.
func (e *Engine) Reset(ctx context.Context) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()

	e.mu.Lock()
	client := e.client
	fresh := e.settings.WithoutSecrets()
	e.mu.Unlock()
	fresh.AppLock = false
	fresh.LockOffered = false

	// Nothing may keep running with the old keys.
	if client != nil {
		if status, err := drive.ReadStatus(ctx, client, vaultRemote+":", fresh.Drive.Letter); err == nil && status.Mounted {
			_ = drive.Unmount(ctx, client, fresh.Drive.Letter)
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
	e.locked, e.closing, e.unlockConnects, e.wantConnected = false, false, false, false
	e.usage, e.fingerprint, e.signInProblem = nil, "", ""
	e.driveStatus = drive.Status{}
	e.mu.Unlock()
	e.wipeTraces()

	if err := e.prepareLocked(ctx); err != nil {
		e.setPhase(PhaseError, err.Error())
		return nil
	}
	e.settlePhaseLocked(ctx, false)
	return nil
}
