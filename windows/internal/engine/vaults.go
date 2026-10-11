// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/drive"
	"github.com/burnersen/CryptBridge/windows/internal/pcloud"
	"github.com/burnersen/CryptBridge/windows/internal/prereq"
	"github.com/burnersen/CryptBridge/windows/internal/rcd"
	"github.com/burnersen/CryptBridge/windows/internal/settings"
	"github.com/burnersen/CryptBridge/windows/internal/vault"
)

// VaultState is what the drive of one vault is doing, in one word.
type VaultState string

const (
	StateDisconnected VaultState = "disconnected"
	StateConnecting   VaultState = "connecting"
	StateConnected    VaultState = "connected"
	StateReconnecting VaultState = "reconnecting"
	StateWaiting      VaultState = "waiting" // a local vault waits for its folder (drive not plugged in)
	StateError        VaultState = "error"
)

const (
	// spaceEvery is how often the free space of a local vault's disk is read.
	spaceEvery = 30 * time.Second

	// volumePrefix: Explorer shows a drive as "<name> (\\CryptBridge)".
	volumePrefix = `\\CryptBridge\`
)

// vaultRuntime is what the engine knows about a vault beyond its settings.
// It is guarded by Engine.mu.
type vaultRuntime struct {
	want          bool // the user wants the drive connected
	state         VaultState
	message       string
	status        drive.Status
	fingerprint   string
	folder        drive.FolderID // local: the folder the drive was connected to
	space         *drive.Space   // local: the disk of the folder
	spaceAt       time.Time
	counting      *SizeProgress // a size count is running
	sizeError     string
	nextCount     time.Time // local: next automatic size count
	otherFolder   bool      // local: the keys fit, but it is not the folder the vault was last connected to
	reconnectTry  int
	nextReconnect time.Time
}

// VaultSnapshot is what the window and the tray show about one vault.
type VaultSnapshot struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Kind        string               `json:"kind"`
	Letter      string               `json:"letter"`
	AutoConnect bool                 `json:"autoConnect"`
	Location    string               `json:"location"` // pCloud folder or folder on this PC; "" while locked
	HasKeys     bool                 `json:"hasKeys"`
	State       VaultState           `json:"state"`
	Message     string               `json:"message"`
	Fingerprint string               `json:"fingerprint"`
	Drive       drive.Status         `json:"drive"`
	Space       *drive.Space         `json:"space"` // local vaults: the disk; pCloud: see Snapshot.Usage
	Size        *settings.SizeRecord `json:"size"`
	Counting    *SizeProgress        `json:"counting"`
	SizeError   string               `json:"sizeError"`
	OtherFolder bool                 `json:"otherFolder"` // see Engine.UseFolder
}

// runtimeLocked returns the runtime state of a vault; the caller holds mu.
func (e *Engine) runtimeLocked(id string) *vaultRuntime {
	rt, ok := e.vaults[id]
	if !ok {
		rt = &vaultRuntime{state: StateDisconnected}
		e.vaults[id] = rt
	}
	return rt
}

// update changes the runtime state of a vault under mu.
func (e *Engine) update(id string, change func(rt *vaultRuntime)) {
	e.mu.Lock()
	change(e.runtimeLocked(id))
	e.mu.Unlock()
}

// wants reports whether the user wants the drive of a vault connected.
func (e *Engine) wants(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.runtimeLocked(id).want
}

func (e *Engine) vaultSnapshotLocked(v settings.Vault) VaultSnapshot {
	rt := e.runtimeLocked(v.ID)
	snap := VaultSnapshot{
		ID:          v.ID,
		Name:        v.Name,
		Kind:        v.Kind,
		Letter:      v.Letter,
		AutoConnect: v.AutoConnect,
		Location:    location(v),
		HasKeys:     v.HasKeys(),
		State:       rt.state,
		Message:     rt.message,
		Fingerprint: rt.fingerprint,
		Drive:       rt.status,
		Space:       rt.space,
		Size:        v.LastSize,
		Counting:    rt.counting,
		SizeError:   rt.sizeError,
		OtherFolder: rt.otherFolder,
	}
	if snap.Drive.Transfers == nil {
		snap.Drive.Transfers = []drive.Transfer{}
	}
	if v.Kind == settings.KindPCloud && !e.settings.SignedIn() && rt.state == StateDisconnected {
		snap.Message = "Sign in to pCloud to connect this drive."
	}
	return snap
}

// errFolderMissing: the folder of a local vault is not there, usually
// because its USB drive is not plugged in.
type errFolderMissing struct {
	path string
}

func (e *errFolderMissing) Error() string {
	return fmt.Sprintf("the folder %s is not there — is its drive plugged in?", e.path)
}

// errWrongFolder: the folder of a local vault is there, but it is not the
// vault — the keys do not open it (for example because another USB drive got
// the drive letter), or it is empty although a vault always holds at least
// its check file (the vault folder was moved and an empty one took its place).
type errWrongFolder struct {
	name, path string
	empty      bool
}

func (e *errWrongFolder) Error() string {
	if e.empty {
		return fmt.Sprintf("the folder %s is empty, but the vault %q is not — was the vault folder moved? "+
			"Nothing was connected and nothing was written", e.path, e.name)
	}
	return fmt.Sprintf("the keys of %q do not open the folder %s — maybe another drive got this drive letter. "+
		"Nothing was connected and nothing was written", e.name, e.path)
}

// errOtherFolder: the keys open the folder, but it is not the folder the
// vault was last connected to — a copy of the vault on another USB drive, or
// the vault moved to a new disk. Working on a copy without noticing would
// let the copies drift apart, so the user decides (Engine.UseFolder).
type errOtherFolder struct {
	name, path string
}

func (e *errOtherFolder) Error() string {
	return fmt.Sprintf("%s holds the vault %q, but it is not the folder the drive was last connected to — "+
		"another drive with a copy, or the vault moved? Nothing was connected. If this is the vault you want to "+
		"work with from now on, use this folder", e.path, e.name)
}

// Connect connects the drive of a vault. A local vault whose folder is
// missing waits for it and connects as soon as it appears.
func (e *Engine) Connect(ctx context.Context, id string) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	err := e.connectVaultLocked(ctx, id)
	e.syncLANLocked(ctx, "")
	return err
}

// UseFolder connects a local vault to the folder now at its place although
// it is not the folder it was last connected to (see errOtherFolder). The
// keys are checked as always.
func (e *Engine) UseFolder(ctx context.Context, id string) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	err := e.connectWithLocked(ctx, id, true)
	e.syncLANLocked(ctx, "")
	return err
}

// ConnectAll connects every drive. It returns the first problem; every
// drive shows its own.
func (e *Engine) ConnectAll(ctx context.Context) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	var first error
	for _, v := range e.currentSettings().Vaults {
		if err := e.connectVaultLocked(ctx, v.ID); err != nil && first == nil {
			first = fmt.Errorf("%s (%s:): %w", v.Name, v.Letter, err)
		}
	}
	e.syncLANLocked(ctx, "")
	return first
}

func (e *Engine) connectVaultLocked(ctx context.Context, id string) error {
	return e.connectWithLocked(ctx, id, false)
}

// connectWithLocked connects a drive; with otherFolder, a local vault may
// use a folder other than the one it was last connected to.
func (e *Engine) connectWithLocked(ctx context.Context, id string, otherFolder bool) error {
	if e.isLocked() {
		return errLocked
	}
	v, ok := e.currentSettings().Vault(id)
	if !ok {
		return errUnknownVault
	}
	if !v.HasKeys() {
		return errors.New("the keys of this vault are missing")
	}
	e.update(id, func(rt *vaultRuntime) {
		rt.want = true
		rt.state = StateConnecting
		rt.message = ""
		rt.otherFolder = false
	})
	e.publish()

	folder, err := e.mountLocked(ctx, v, otherFolder)
	if err != nil {
		e.connectFailed(v, err)
		var missing *errFolderMissing
		if errors.As(err, &missing) {
			return nil // the drive waits for its folder; its state says so
		}
		return err
	}
	e.markConnected(v, e.readStatus(ctx, v), folder)
	return e.rememberFolderLocked(v, folder)
}

// rememberFolderLocked keeps the mark of the folder a local vault is
// connected to, so that a copy elsewhere is recognised later.
func (e *Engine) rememberFolderLocked(v settings.Vault, folder drive.FolderID) error {
	mark := settings.FolderMark{Volume: folder.Volume, Index: folder.Index}
	if v.Kind != settings.KindLocal || folder == (drive.FolderID{}) || (v.Folder != nil && *v.Folder == mark) {
		return nil
	}
	s := e.currentSettings()
	index := slices.IndexFunc(s.Vaults, func(other settings.Vault) bool { return other.ID == v.ID })
	if index < 0 {
		return nil
	}
	s.Vaults[index].Folder = &mark
	if err := e.saveSettings(s); err != nil {
		return fmt.Errorf("the drive is connected, but CryptBridge could not remember its folder: %w", err)
	}
	e.mu.Lock()
	e.settings = s
	e.mu.Unlock()
	return nil
}

// connectFailed shows why a drive did not connect and decides whether it
// keeps trying in the background.
func (e *Engine) connectFailed(v settings.Vault, err error) {
	autoReconnect := e.currentSettings().AutoReconnect
	var missing *errFolderMissing
	var other *errOtherFolder
	e.update(v.ID, func(rt *vaultRuntime) {
		switch {
		case errors.As(err, &missing):
			rt.state = StateWaiting
			rt.message = "Waiting for " + missing.path + " — plug in its drive and the vault connects by itself."
		case errors.As(err, &other):
			rt.want = false
			rt.state = StateError
			rt.message = err.Error()
			rt.otherFolder = true
		case pcloud.IsSignInProblem(err) && autoReconnect:
			// Keep trying: the drive comes by itself once pCloud accepts
			// the sign-in again.
			rt.state = StateError
			rt.message = err.Error()
		default:
			rt.want = false
			rt.state = StateError
			rt.message = err.Error()
		}
	})
	e.publish()
}

// mountLocked connects the drive of a vault after the checks that keep a
// vault safe. It returns the ID of a local vault's folder. With otherFolder,
// a local vault may use a folder other than the one it was last connected to.
func (e *Engine) mountLocked(ctx context.Context, v settings.Vault, otherFolder bool) (drive.FolderID, error) {
	if err := e.ensureWinFsp(); err != nil {
		return drive.FolderID{}, err
	}
	local := v.Kind == settings.KindLocal
	if !local && !e.currentSettings().SignedIn() {
		return drive.FolderID{}, errors.New("sign in to pCloud first")
	}
	if err := e.prepareLocked(ctx); err != nil {
		return drive.FolderID{}, err
	}
	e.mu.Lock()
	client := e.client
	cache := e.settings.Cache
	e.mu.Unlock()

	if status, err := drive.ReadStatus(ctx, client, remoteOf(v), v.Letter); err == nil && status.Mounted {
		return currentFolderID(v), nil
	}
	var folder drive.FolderID
	if local {
		var err error
		if folder, err = checkLocalFolder(ctx, client, v, otherFolder); err != nil {
			return drive.FolderID{}, err
		}
	} else if err := e.checkSignInLocked(ctx, client); err != nil {
		return drive.FolderID{}, err
	}
	err := drive.Mount(ctx, client, drive.Settings{
		Remote:            remoteOf(v),
		Letter:            v.Letter,
		VolumeName:        volumePrefix + v.Name,
		CacheMaxSize:      cache.MaxSize,
		CacheMinFreeSpace: cache.MinFreeSpace,
		Local:             local,
	})
	return folder, err
}

// ensureWinFsp checks once that WinFsp is installed.
func (e *Engine) ensureWinFsp() error {
	e.mu.Lock()
	installed := e.winFsp
	e.mu.Unlock()
	if installed {
		return nil
	}
	if _, ok := prereq.WinFspInstalled(); !ok {
		return fmt.Errorf("WinFsp is not installed. It lets Windows show a vault as a drive; download it from %s", prereq.WinFspDownloadURL)
	}
	e.mu.Lock()
	e.winFsp = true
	e.mu.Unlock()
	return nil
}

// checkLocalFolder makes sure the folder of a local vault is there and is
// really this vault before its drive is connected. Without the check, rclone
// would create a missing folder anew with the first file written — on
// whatever disk has the drive letter now (measured with rclone 1.75.1).
// Without otherFolder, it must also be the very folder the vault was last
// connected to, not a copy.
func checkLocalFolder(ctx context.Context, client *rcd.Client, v settings.Vault, otherFolder bool) (drive.FolderID, error) {
	path := localPath(v)
	folder, err := drive.IdentifyFolder(path)
	if errors.Is(err, fs.ErrNotExist) {
		return drive.FolderID{}, &errFolderMissing{path: path}
	}
	if err != nil {
		return drive.FolderID{}, err
	}
	base, cfg := placeOf(v)
	check, err := vault.Verify(ctx, client, base, cfg)
	if err != nil {
		return drive.FolderID{}, err
	}
	switch check.Verdict {
	case vault.VerdictMismatch:
		return drive.FolderID{}, &errWrongFolder{name: v.Name, path: path}
	case vault.VerdictEmpty:
		return drive.FolderID{}, &errWrongFolder{name: v.Name, path: path, empty: true}
	}
	if !otherFolder && v.Folder != nil && (drive.FolderID{Volume: v.Folder.Volume, Index: v.Folder.Index}) != folder {
		return drive.FolderID{}, &errOtherFolder{name: v.Name, path: path}
	}
	if check.Verdict != vault.VerdictUnverifiable && !check.HasCheckFile {
		if err := vault.WriteCheckFile(ctx, client, remoteOf(v)); err != nil {
			return drive.FolderID{}, err
		}
	}
	return folder, nil
}

// currentFolderID returns the ID of a local vault's folder now, or the zero
// ID if it cannot be read; the zero ID is filled in by the next look.
func currentFolderID(v settings.Vault) drive.FolderID {
	if v.Kind != settings.KindLocal {
		return drive.FolderID{}
	}
	folder, _ := drive.IdentifyFolder(localPath(v))
	return folder
}

// checkSignInLocked asks pCloud whether it accepts the sign-in before a
// drive is mounted; otherwise the drive would appear but stay empty without
// a word of explanation. Other problems (offline, pCloud busy, a slow
// answer) do not stop the connect: rclone copes with them, and the drive
// works as soon as pCloud can be reached. An answer from the last minute is
// good enough, so that several pCloud drives do not ask one after another.
func (e *Engine) checkSignInLocked(ctx context.Context, client *rcd.Client) error {
	e.mu.Lock()
	problem := e.signInProblem
	e.mu.Unlock()
	if problem == "" && time.Since(e.lastUsage) < signInRecheckEvery {
		return nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, signInCheckTimeout)
	defer cancel()
	if err := e.readUsageLocked(checkCtx, client); pcloud.IsSignInProblem(err) {
		return err
	}
	return nil
}

// readStatus reads the status of a vault's drive; a failure gives an empty
// status, which the next look of the monitor replaces.
func (e *Engine) readStatus(ctx context.Context, v settings.Vault) drive.Status {
	e.mu.Lock()
	client := e.client
	e.mu.Unlock()
	if client == nil {
		return drive.Status{}
	}
	status, _ := drive.ReadStatus(ctx, client, remoteOf(v), v.Letter)
	return status
}

func (e *Engine) markConnected(v settings.Vault, status drive.Status, folder drive.FolderID) {
	e.update(v.ID, func(rt *vaultRuntime) {
		rt.want = true
		rt.state = StateConnected
		rt.message = ""
		rt.status = status
		rt.folder = folder
		rt.otherFolder = false
		rt.reconnectTry = 0
		rt.nextReconnect = time.Time{}
	})
	e.publish()
}

// Disconnect removes the drive of a vault. Without force it refuses while
// uploads are still running and returns an *UploadsPendingError.
func (e *Engine) Disconnect(ctx context.Context, id string, force bool) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	if e.isLocked() {
		return nil // the lock closes the drives by itself
	}
	v, ok := e.currentSettings().Vault(id)
	if !ok {
		return errUnknownVault
	}
	return e.disconnectVaultLocked(ctx, v, force)
}

// DisconnectAll removes every drive. Without force it refuses while any
// drive is still uploading, and nothing is disconnected then.
func (e *Engine) DisconnectAll(ctx context.Context, force bool) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	if e.isLocked() {
		return nil
	}
	return e.disconnectAllLocked(ctx, force)
}

func (e *Engine) disconnectAllLocked(ctx context.Context, force bool) error {
	s := e.currentSettings()
	if !force {
		if err := e.ensureNoUploadsAnywhere(ctx, s.Vaults); err != nil {
			return err
		}
	}
	e.stopLANLocked(ctx) // once, instead of changing it for every drive
	var first error
	for _, v := range s.Vaults {
		if err := e.disconnectVaultLocked(ctx, v, true); err != nil && first == nil {
			first = fmt.Errorf("%s (%s:): %w", v.Name, v.Letter, err)
		}
	}
	return first
}

func (e *Engine) disconnectVaultLocked(ctx context.Context, v settings.Vault, force bool) error {
	e.mu.Lock()
	client := e.client
	e.mu.Unlock()
	if client == nil {
		e.setDisconnected(v.ID)
		return nil
	}
	status, err := drive.ReadStatus(ctx, client, remoteOf(v), v.Letter)
	if err != nil {
		return err
	}
	if !status.Mounted {
		e.setDisconnected(v.ID)
		return nil
	}
	if !force {
		if err := e.ensureNoUploads(ctx, client, v, status); err != nil {
			return err
		}
	}
	e.withdrawFromLANLocked(ctx, v)
	if err := drive.Unmount(ctx, client, v.Letter); err != nil {
		e.syncLANLocked(ctx, "") // the drive stays: share it again
		return err
	}
	e.setDisconnected(v.ID)
	return nil
}

// ensureNoUploads returns an *UploadsPendingError if a drive is uploading,
// looking twice so that a file written a moment ago is not missed.
func (e *Engine) ensureNoUploads(ctx context.Context, client *rcd.Client, v settings.Vault, first drive.Status) error {
	if pending := first.PendingUploads(); pending > 0 {
		return &UploadsPendingError{Count: pending}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(settleTime):
	}
	second, err := drive.ReadStatus(ctx, client, remoteOf(v), v.Letter)
	if err != nil {
		return err
	}
	if pending := second.PendingUploads(); pending > 0 {
		return &UploadsPendingError{Count: pending}
	}
	return nil
}

// ensureNoUploadsAnywhere is ensureNoUploads for all connected drives,
// counting the pending files of all of them.
func (e *Engine) ensureNoUploadsAnywhere(ctx context.Context, vaults []settings.Vault) error {
	e.mu.Lock()
	client := e.client
	e.mu.Unlock()
	if client == nil {
		return nil
	}
	count := func() (int, bool, error) {
		total, mounted := 0, false
		for _, v := range vaults {
			status, err := drive.ReadStatus(ctx, client, remoteOf(v), v.Letter)
			if err != nil {
				return 0, false, err
			}
			total += status.PendingUploads()
			mounted = mounted || status.Mounted
		}
		return total, mounted, nil
	}
	pending, mounted, err := count()
	if err != nil || pending > 0 || !mounted {
		if pending > 0 {
			return &UploadsPendingError{Count: pending}
		}
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(settleTime):
	}
	if pending, _, err = count(); err != nil {
		return err
	}
	if pending > 0 {
		return &UploadsPendingError{Count: pending}
	}
	return nil
}

func (e *Engine) setDisconnected(id string) {
	e.update(id, func(rt *vaultRuntime) {
		rt.want = false
		rt.state = StateDisconnected
		rt.message = ""
		rt.status = drive.Status{}
		rt.folder = drive.FolderID{}
	})
	e.publish()
}

// watchVaultLocked is the monitor's look at one vault: its drive status,
// the folder of a connected local vault, and reconnecting a drive that was
// lost or whose folder came back.
func (e *Engine) watchVaultLocked(ctx context.Context, client *rcd.Client, s settings.Settings, v settings.Vault) {
	status, err := drive.ReadStatus(ctx, client, remoteOf(v), v.Letter)
	if err != nil {
		return
	}
	e.mu.Lock()
	rt := e.runtimeLocked(v.ID)
	rt.status = status
	want, state, folder := rt.want, rt.state, rt.folder
	e.mu.Unlock()

	local := v.Kind == settings.KindLocal
	if local && status.Mounted {
		if why, replaced := folderGone(v, folder); why != "" {
			e.closeLostFolderLocked(ctx, client, v, status, why, replaced)
			return
		}
		if folder == (drive.FolderID{}) {
			e.update(v.ID, func(rt *vaultRuntime) { rt.folder = currentFolderID(v) })
		}
		e.refreshSpace(v)
		e.countAutomaticallyLocked(ctx, client, v)
	}

	switch {
	case want && !status.Mounted && local:
		if _, err := drive.IdentifyFolder(localPath(v)); errors.Is(err, fs.ErrNotExist) {
			if state != StateWaiting {
				e.connectFailed(v, &errFolderMissing{path: localPath(v)})
			}
			return
		}
		// The folder is there (again): connect, after checking the keys.
		e.reconnectLocked(ctx, v, "the folder is there again", true)
	case want && !status.Mounted:
		e.reconnectLocked(ctx, v, "the drive disappeared", false)
	case want && status.Mounted && state != StateConnected:
		e.markConnected(v, status, currentFolderID(v))
	}
}

// folderGone tells why the folder of a connected local vault cannot be used
// any more, or returns "" while it is fine. The folder is the one the drive
// was connected to: a new folder of the same name — which rclone itself
// creates when it writes after the folder was moved away — is reported as
// replaced.
func folderGone(v settings.Vault, connected drive.FolderID) (why string, replaced bool) {
	path := localPath(v)
	now, err := drive.IdentifyFolder(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "The folder " + path + " disappeared while the drive was connected — was its drive unplugged without disconnecting first?", false
	case err != nil:
		return "", false // cannot tell; keep the drive
	case connected != (drive.FolderID{}) && now != connected:
		return "The folder " + path + " was moved or replaced while the drive was connected.", true
	}
	return "", false
}

// closeLostFolderLocked closes the drive of a local vault whose folder is
// gone, at once: every further write would make rclone create the folder
// anew. Files still waiting stay in the cache and are written when the
// vault connects again. A folder that is merely missing (drive unplugged)
// is waited for; a replaced folder is not connected again by itself — the
// user has to look at what happened first.
func (e *Engine) closeLostFolderLocked(ctx context.Context, client *rcd.Client, v settings.Vault, status drive.Status, why string, replaced bool) {
	e.withdrawFromLANLocked(ctx, v)
	_ = drive.Unmount(ctx, client, v.Letter)
	message := why + " The drive was closed to protect the vault."
	if pending := status.PendingUploads(); pending > 0 {
		message += fmt.Sprintf(" %d file(s) still wait on this PC and are written into the vault when it connects again.", pending)
	}
	if replaced {
		message += " Put the vault folder back in its place, then click Connect."
	}
	e.update(v.ID, func(rt *vaultRuntime) {
		rt.state = StateWaiting
		if replaced {
			rt.want = false
			rt.state = StateError
		}
		rt.message = message
		rt.status = drive.Status{}
		rt.folder = drive.FolderID{}
	})
	e.publish()
}

// refreshSpace reads the free space of a local vault's disk now and then.
func (e *Engine) refreshSpace(v settings.Vault) {
	e.mu.Lock()
	due := time.Since(e.runtimeLocked(v.ID).spaceAt) >= spaceEvery
	e.mu.Unlock()
	if !due {
		return
	}
	space, err := drive.DiskSpace(localPath(v))
	e.update(v.ID, func(rt *vaultRuntime) {
		rt.spaceAt = time.Now()
		if err == nil {
			rt.space = &space
		}
	})
}

// reconnectLocked brings a drive back after rclone or the mount got lost,
// with growing pauses between attempts. With always, it tries even when
// automatic reconnects are off: the user asked for the drive by connecting
// a vault that waits for its folder.
func (e *Engine) reconnectLocked(ctx context.Context, v settings.Vault, reason string, always bool) {
	if !always && !e.currentSettings().AutoReconnect {
		e.update(v.ID, func(rt *vaultRuntime) {
			rt.want = false
			rt.state = StateError
			rt.message = "The drive was lost: " + reason + ". Click Connect to try again."
		})
		e.publish()
		return
	}
	e.mu.Lock()
	rt := e.runtimeLocked(v.ID)
	if time.Now().Before(rt.nextReconnect) {
		e.mu.Unlock()
		return
	}
	delay := reconnectDelays[min(rt.reconnectTry, len(reconnectDelays)-1)]
	rt.reconnectTry++
	rt.nextReconnect = time.Now().Add(delay)
	attempt := rt.reconnectTry
	rt.state = StateReconnecting
	rt.message = fmt.Sprintf("Reconnecting (attempt %d): %s.", attempt, reason)
	e.mu.Unlock()
	e.publish()

	folder, err := e.mountLocked(ctx, v, false)
	var missing *errFolderMissing
	var wrong *errWrongFolder
	var other *errOtherFolder
	switch {
	case err == nil:
		e.markConnected(v, e.readStatus(ctx, v), folder)
		if err := e.rememberFolderLocked(v, folder); err != nil {
			e.update(v.ID, func(rt *vaultRuntime) { rt.message = err.Error() })
			e.publish()
		}
	case errors.As(err, &missing), errors.As(err, &wrong), errors.As(err, &other):
		e.connectFailed(v, err)
	default:
		e.update(v.ID, func(rt *vaultRuntime) {
			rt.want = true // keep trying
			rt.state = StateReconnecting
			rt.message = fmt.Sprintf("Reconnect attempt %d failed: %v. Next try in %s.", attempt, err, delay)
		})
		e.publish()
	}
}

// OpenDriveLetter returns the letter of a connected drive, for opening it
// in Explorer.
func (e *Engine) OpenDriveLetter(id string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, ok := e.settings.Vault(id)
	if !ok {
		return "", errUnknownVault
	}
	if !e.runtimeLocked(id).status.Mounted {
		return "", fmt.Errorf("drive %s: is not connected", v.Letter)
	}
	return v.Letter, nil
}

// describe returns "name (X:)" for messages.
func describe(v settings.Vault) string {
	return strings.TrimSpace(v.Name) + " (" + v.Letter + ":)"
}
