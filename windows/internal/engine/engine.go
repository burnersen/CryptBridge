// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

// Package engine is the heart of CryptBridge. It keeps the rclone process,
// the pCloud sign-in, the vaults and their drives together and watches over
// them. It knows nothing about windows or tray icons; those only call its
// methods and listen to its snapshots.
package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/appdir"
	"github.com/burnersen/CryptBridge/windows/internal/drive"
	"github.com/burnersen/CryptBridge/windows/internal/passlock"
	"github.com/burnersen/CryptBridge/windows/internal/pcloud"
	"github.com/burnersen/CryptBridge/windows/internal/prereq"
	"github.com/burnersen/CryptBridge/windows/internal/rcd"
	"github.com/burnersen/CryptBridge/windows/internal/settings"
)

const (
	// pollEvery is how often the drives are looked at.
	pollEvery = 2 * time.Second

	// usageEvery is how often pCloud is asked for the free space.
	usageEvery = 5 * time.Minute

	// signInRecheckEvery is how often pCloud is asked again while it refuses
	// the sign-in: the refusal can be a passing outage at pCloud.
	signInRecheckEvery = time.Minute

	// signInCheckTimeout limits the sign-in check before connecting. If
	// pCloud answers more slowly than this, the drive is connected anyway.
	signInCheckTimeout = 10 * time.Second

	// lostAfterFailures: so many failed health checks in a row mean the
	// rclone process is gone (one failure may just be a busy moment).
	lostAfterFailures = 2

	// maxLogSize: a larger rclone log is moved aside before rclone starts.
	maxLogSize = 10 << 20
)

// reconnectDelays is the waiting time before each automatic reconnect
// attempt; the last value repeats.
var reconnectDelays = []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute}

// Phase is what CryptBridge as a whole is doing, in one word. What each
// drive does is its VaultState.
type Phase string

const (
	PhaseStarting Phase = "starting"
	PhaseSetup    Phase = "setup" // no vault yet
	PhaseReady    Phase = "ready"
	PhaseError    Phase = "error"  // CryptBridge cannot work (no rclone, unreadable settings)
	PhaseLocked   Phase = "locked" // the app password is needed first
)

// Snapshot is everything a window or tray icon needs to show.
type Snapshot struct {
	Phase             Phase           `json:"phase"`
	Message           string          `json:"message"`
	Version           string          `json:"version"`
	SignedIn          bool            `json:"signedIn"`
	SignInProblem     string          `json:"signInProblem"` // why pCloud refuses the sign-in; "" while it accepts it
	PCloudHost        string          `json:"pcloudHost"`
	OwnPCloudApp      bool            `json:"ownPCloudApp"`
	PCloudClientID    string          `json:"pcloudClientId"` // not a secret; the client secret is never sent
	SignInAppID       string          `json:"signInAppId"`    // client ID of the current sign-in; "" = rclone's app
	Usage             *pcloud.Usage   `json:"usage"`          // the pCloud account
	Vaults            []VaultSnapshot `json:"vaults"`
	Rclone            *prereq.Rclone  `json:"rclone"`
	WinFsp            bool            `json:"winFsp"`
	AutoReconnect     bool            `json:"autoReconnect"`
	CacheDir          string          `json:"cacheDir"`
	CacheMaxSize      string          `json:"cacheMaxSize"`
	CacheMinFreeSpace string          `json:"cacheMinFreeSpace"`
	Theme             string          `json:"theme"`
	AppLock           bool            `json:"appLock"`        // an app password is set
	LockOffered       bool            `json:"lockOffered"`    // the app password was offered once
	Locked            bool            `json:"locked"`         // the app password is needed first
	UnlockConnects    []string        `json:"unlockConnects"` // letters of the drives that connect after unlocking
}

// PendingUploads counts the files of all drives that are not safely in
// their vault yet.
func (s Snapshot) PendingUploads() int {
	total := 0
	for _, v := range s.Vaults {
		total += v.Drive.PendingUploads()
	}
	return total
}

// UploadsPendingError is returned when disconnecting or quitting would
// interrupt uploads. The caller may ask the user and try again with force.
//
// What force does (measured 2026-10-10): a queued upload stops, the file
// stays in the cache folder on this PC, and rclone uploads it the next time
// the drive is connected with the same cache folder.
type UploadsPendingError struct {
	Count int
}

func (e *UploadsPendingError) Error() string {
	return fmt.Sprintf("%d file(s) are still being written into the vault", e.Count)
}

// settleTime is how long a disconnect waits for a second look at the
// uploads. A file Windows has just finished writing reaches rclone's upload
// queue only a moment later (measured: 20 to 300 ms).
const settleTime = 1500 * time.Millisecond

// Engine ties everything together. Create it with New, then call Start.
type Engine struct {
	paths   appdir.Paths
	version string
	ctx     context.Context // from Start; background work ends with it

	// actionMu serialises user actions and automatic reconnects, so that
	// "connect" and "disconnect" can never run at the same time.
	actionMu sync.Mutex

	// mu guards the fields below. It is only ever held for a moment.
	mu            sync.Mutex
	settings      settings.Settings
	rclone        *prereq.Rclone
	winFsp        bool
	client        *rcd.Client
	usage         *pcloud.Usage
	signInProblem string
	phase         Phase
	message       string
	vaults        map[string]*vaultRuntime // by vault ID
	listeners     []func(Snapshot)

	// App lock (see lock.go), guarded by mu.
	locked         bool          // no secrets in memory; the app password is needed
	key            *passlock.Key // from the app password while unlocked; nil without app lock
	closing        bool          // locked, but drives stay until their uploads are done
	unlockConnects []string      // IDs of the vaults to connect after unlocking

	// Guarded by actionMu (the monitor holds it while checking).
	failures       int
	lastUsage      time.Time
	unlockFailures int
	nextUnlock     time.Time
}

// New creates an engine; nothing happens until Start.
func New(paths appdir.Paths, version string) *Engine {
	return &Engine{paths: paths, version: version, phase: PhaseStarting, vaults: map[string]*vaultRuntime{}, ctx: context.Background()}
}

// Subscribe registers a function that receives every new snapshot. It is
// called from the engine's goroutines and must not block for long.
func (e *Engine) Subscribe(listener func(Snapshot)) {
	e.mu.Lock()
	e.listeners = append(e.listeners, listener)
	e.mu.Unlock()
}

// Snapshot returns the current state.
func (e *Engine) Snapshot() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.snapshotLocked()
}

func (e *Engine) snapshotLocked() Snapshot {
	s := e.settings
	snap := Snapshot{
		Phase:             e.phase,
		Message:           e.message,
		Version:           e.version,
		SignedIn:          s.SignedIn(),
		SignInProblem:     e.signInProblem,
		PCloudHost:        s.PCloud.Hostname,
		OwnPCloudApp:      s.PCloudApp.IsSet(),
		PCloudClientID:    s.PCloudApp.ClientID,
		SignInAppID:       s.SignInAppID,
		Usage:             e.usage,
		Vaults:            make([]VaultSnapshot, 0, len(s.Vaults)),
		Rclone:            e.rclone,
		WinFsp:            e.winFsp,
		AutoReconnect:     s.AutoReconnect,
		CacheDir:          s.Cache.Dir,
		CacheMaxSize:      s.Cache.MaxSize,
		CacheMinFreeSpace: s.Cache.MinFreeSpace,
		Theme:             s.Theme,
		AppLock:           s.AppLock,
		LockOffered:       s.LockOffered,
		Locked:            e.locked,
		UnlockConnects:    []string{},
	}
	for _, v := range s.Vaults {
		snap.Vaults = append(snap.Vaults, e.vaultSnapshotLocked(v))
		if slices.Contains(e.unlockConnects, v.ID) {
			snap.UnlockConnects = append(snap.UnlockConnects, v.Letter)
		}
	}
	return snap
}

// setPhase changes phase and message and tells every listener.
func (e *Engine) setPhase(phase Phase, message string) {
	e.mu.Lock()
	e.phase = phase
	e.message = message
	e.mu.Unlock()
	e.publish()
}

// publish sends the current snapshot to every listener.
func (e *Engine) publish() {
	e.mu.Lock()
	snap := e.snapshotLocked()
	listeners := slices.Clone(e.listeners)
	e.mu.Unlock()
	for _, listener := range listeners {
		listener(snap)
	}
}

// currentSettings returns a copy of the settings that may be changed.
func (e *Engine) currentSettings() settings.Settings {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.settings.Clone()
}

// Start loads the settings, finds rclone and WinFsp, starts or re-attaches
// rclone, connects the drives that should connect and starts watching.
// Problems are reported through the snapshot; only a broken installation is
// an error.
func (e *Engine) Start(ctx context.Context) error {
	if err := e.paths.Ensure(); err != nil {
		return fmt.Errorf("create CryptBridge folders: %w", err)
	}
	e.ctx = ctx

	loaded, loadErr := settings.Load(e.paths.SettingsFile, e.paths.CacheDir)
	if loadErr != nil {
		loadErr = e.setAsideUnreadable(loadErr)
	}
	legacy := loadErr == nil && settings.IsLegacy(e.paths.SettingsFile)
	_, winFsp := prereq.WinFspInstalled()
	e.mu.Lock()
	e.settings = loaded
	e.winFsp = winFsp
	e.locked = loaded.AppLock
	e.unlockConnects = autoConnectIDs(loaded) // the first unlock counts as the start
	e.mu.Unlock()

	go e.monitor(ctx)

	if loadErr != nil {
		e.setPhase(PhaseError, loadErr.Error())
	}

	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	if loaded.AppLock {
		e.startLockedLocked(ctx)
		return nil
	}
	if legacy {
		// Write the new format now (the old file is kept as a backup), so
		// that every later change starts from the converted settings.
		if err := e.saveSettings(loaded); err != nil {
			e.setPhase(PhaseError, "Your settings could not be converted for this version: "+err.Error())
			return nil
		}
	}
	if err := e.prepareLocked(ctx); err != nil {
		e.setPhase(PhaseError, err.Error())
		return nil
	}
	e.settleLocked(ctx, true)
	return nil
}

// setAsideUnreadable keeps a settings file that could not be read under a
// new name, so that setting CryptBridge up again never overwrites it — it
// may come from a newer version, or be readable again later. It returns the
// message for the user.
func (e *Engine) setAsideUnreadable(loadErr error) error {
	kept, err := settings.SetAside(e.paths.SettingsFile)
	if err != nil {
		return fmt.Errorf("your saved settings could not be read: %v. CryptBridge could not keep them aside either (%v); "+
			"please copy %s somewhere safe before you set CryptBridge up again", loadErr, err, e.paths.SettingsFile)
	}
	return fmt.Errorf("your saved settings could not be read: %v. They were kept as %s. Please set CryptBridge up again", loadErr, kept)
}

// autoConnectIDs lists the vaults that connect when CryptBridge starts.
func autoConnectIDs(s settings.Settings) []string {
	var ids []string
	for _, v := range s.Vaults {
		if v.AutoConnect {
			ids = append(ids, v.ID)
		}
	}
	return ids
}

// prepareLocked makes sure rclone runs and knows the account and vaults.
// The caller holds actionMu.
func (e *Engine) prepareLocked(ctx context.Context) error {
	if e.isLocked() {
		return errLocked
	}
	if err := e.ensureRcloneFoundLocked(); err != nil {
		return err
	}
	client, err := e.ensureDaemonLocked(ctx)
	if err != nil {
		return err
	}
	return e.ensureRemotesLocked(ctx, client)
}

// ensureRcloneFoundLocked locates rclone.exe once.
func (e *Engine) ensureRcloneFoundLocked() error {
	e.mu.Lock()
	found := e.rclone != nil
	e.mu.Unlock()
	if found {
		return nil
	}
	rclone, err := prereq.FindRclone(e.paths.BinDir)
	if err != nil {
		return fmt.Errorf("%v. CryptBridge needs rclone %s or newer.", err, prereq.MinRcloneVersion)
	}
	e.mu.Lock()
	e.rclone = &rclone
	e.mu.Unlock()
	return nil
}

// ensureDaemonLocked returns a client for a running rclone, attaching to an
// existing one first and starting a new one only if needed.
func (e *Engine) ensureDaemonLocked(ctx context.Context) (*rcd.Client, error) {
	e.mu.Lock()
	client := e.client
	exe := e.rclone.Path
	cacheDir := e.settings.Cache.Dir
	logLevel := "" // rclone's default for CryptBridge: INFO
	if e.settings.AppLock {
		logLevel = lockedLogLevel
	}
	e.mu.Unlock()

	if client != nil && client.Ping(ctx) == nil {
		return client, nil
	}
	if attached, err := rcd.Attach(ctx, e.paths.StateFile); err == nil {
		client = attached
	} else {
		rotateLog(e.paths.RcloneLog)
		client, err = rcd.Start(ctx, rcd.Options{
			RcloneExe:  exe,
			StateFile:  e.paths.StateFile,
			LogFile:    e.paths.RcloneLog,
			LogLevel:   logLevel,
			CacheDir:   cacheDir,
			ConfigFile: "NUL", // keep the configuration in memory only
			ExtraArgs:  []string{"--buffer-size", drive.BufferSize},
		})
		if err != nil {
			return nil, fmt.Errorf("could not start rclone: %w", err)
		}
	}

	e.mu.Lock()
	e.client = client
	e.mu.Unlock()
	return client, nil
}

// ensureRemotesLocked registers account and vaults with rclone unless they
// are already there. An rclone that kept running after a crash of the
// window still knows them, and re-registering a mounted vault could detach
// the drive from its status.
func (e *Engine) ensureRemotesLocked(ctx context.Context, client *rcd.Client) error {
	var answer struct {
		Remotes []string `json:"remotes"`
	}
	if err := client.Call(ctx, "config/listremotes", nil, &answer); err != nil {
		return err
	}
	s := e.currentSettings()

	if s.SignedIn() && !slices.Contains(answer.Remotes, pcloudRemote) {
		if err := pcloud.Configure(ctx, client, pcloudRemote, s.PCloud); err != nil {
			return err
		}
	}
	for _, v := range s.Vaults {
		if !v.HasKeys() || slices.Contains(answer.Remotes, remoteName(v.ID)) {
			continue
		}
		if err := configureVault(ctx, client, v); err != nil {
			return fmt.Errorf("vault %q: %w", v.Name, err)
		}
	}
	return nil
}

// settleLocked decides the phase after the start or after setup changes,
// finds drives that rclone kept while the window was gone, and with startup
// connects the drives that should connect when CryptBridge starts.
func (e *Engine) settleLocked(ctx context.Context, startup bool) {
	s := e.currentSettings()
	if len(s.Vaults) == 0 {
		e.setPhase(PhaseSetup, "Add a vault to get started.")
		return
	}
	e.setPhase(PhaseReady, "")

	e.mu.Lock()
	client := e.client
	e.mu.Unlock()
	for _, v := range s.Vaults {
		if !v.HasKeys() {
			continue
		}
		e.refreshFingerprint(ctx, client, s, v)
		status, err := drive.ReadStatus(ctx, client, remoteOf(v), v.Letter)
		switch {
		case err == nil && status.Mounted:
			// rclone kept the drive while the window was gone.
			e.markConnected(v, status, currentFolderID(v))
		case startup && v.AutoConnect:
			_ = e.connectVaultLocked(ctx, v.ID) // problems show up at the drive
		}
	}
	e.publish()
}

// Shutdown disconnects every drive and stops rclone; used when the user
// quits CryptBridge. Without force it refuses while uploads are running.
func (e *Engine) Shutdown(ctx context.Context, force bool) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	if err := e.disconnectAllLocked(ctx, force); err != nil {
		return err
	}
	e.mu.Lock()
	client := e.client
	e.client = nil
	appLock := e.settings.AppLock
	e.mu.Unlock()
	if client != nil {
		if err := rcd.Stop(ctx, client, e.paths.StateFile); err != nil {
			return err
		}
	}
	if appLock {
		e.wipeTraces() // quitting leaves as little behind as locking
	}
	return nil
}

// monitor refreshes the status and reconnects when rclone got lost.
func (e *Engine) monitor(ctx context.Context) {
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		e.checkOnce(ctx)
	}
}

// checkOnce is one round of the monitor.
func (e *Engine) checkOnce(ctx context.Context) {
	if !e.actionMu.TryLock() {
		return // an action is running; it updates the status itself
	}
	defer e.actionMu.Unlock()

	e.mu.Lock()
	client := e.client
	locked, closing := e.locked, e.closing
	e.mu.Unlock()
	s := e.currentSettings()

	if locked {
		// Nothing to watch while locked, except drives that still finish
		// their uploads before they close.
		if closing {
			e.finishLockLocked(ctx, false)
		}
		return
	}
	if client == nil || client.Ping(ctx) != nil {
		e.failures++
		if e.failures >= lostAfterFailures {
			e.mu.Lock()
			e.client = nil
			e.mu.Unlock()
			for _, v := range s.Vaults {
				if e.wants(v.ID) {
					e.reconnectLocked(ctx, v, "rclone stopped unexpectedly", false)
				}
			}
		}
		return
	}
	e.failures = 0

	for _, v := range s.Vaults {
		if v.HasKeys() {
			e.watchVaultLocked(ctx, client, s, v)
		}
	}
	e.refreshUsage(ctx, client, s)
	e.publish()
}

// refreshUsage asks pCloud for the free space now and then — every minute
// while pCloud refuses the sign-in, so that the warning goes away soon after
// pCloud accepts it again. The warning is kept apart from the drives: a
// connected drive stays "connected" and the warning stays visible.
func (e *Engine) refreshUsage(ctx context.Context, client *rcd.Client, s settings.Settings) {
	e.mu.Lock()
	every := usageEvery
	if e.signInProblem != "" {
		every = signInRecheckEvery
	}
	e.mu.Unlock()
	if !s.SignedIn() || time.Since(e.lastUsage) < every {
		return
	}
	_ = e.readUsageLocked(ctx, client) // a refused sign-in is shown by readUsageLocked
}

// readUsageLocked asks pCloud for the free space. The answer also tells
// whether pCloud accepts the sign-in, and the warning is set or removed
// accordingly. The caller holds actionMu.
func (e *Engine) readUsageLocked(ctx context.Context, client *rcd.Client) error {
	e.lastUsage = time.Now()
	usage, err := pcloud.ReadUsage(ctx, client, pcloudRemote+":")
	if err != nil {
		return e.explain(err)
	}
	e.mu.Lock()
	e.usage = &usage
	e.signInProblem = ""
	e.mu.Unlock()
	e.publish()
	return nil
}

// explain turns pCloud's numbered errors into plain words and remembers a
// refused sign-in, so that the window keeps showing it.
func (e *Engine) explain(err error) error {
	err = pcloud.Classify(err)
	if !pcloud.IsSignInProblem(err) {
		return err
	}
	advice := signInAdvice(err)
	e.mu.Lock()
	changed := e.signInProblem != advice
	e.signInProblem = advice
	e.mu.Unlock()
	if changed {
		e.publish()
	}
	return err
}

// signInAdvice says what a refused sign-in means and what helps.
func signInAdvice(err error) string {
	if errors.Is(err, pcloud.ErrSignInRevoked) {
		return err.Error() + `. This follows a password change or "log out of all devices" — but pCloud has also ` +
			"caused it during outages that ended by themselves. CryptBridge asks pCloud again every minute and " +
			"removes this message as soon as the sign-in works again. If it lasts, sign in again."
	}
	return err.Error() + ". Please sign in again."
}

// rotateLog moves an oversized log aside so that it cannot grow forever.
func rotateLog(path string) {
	if info, err := os.Stat(path); err == nil && info.Size() > maxLogSize {
		_ = os.Rename(path, path+".old")
	}
}
