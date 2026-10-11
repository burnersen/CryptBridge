// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

// Package engine is the heart of CryptBridge. It keeps the rclone process,
// the pCloud sign-in, the vault and the drive together and watches over
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
	"github.com/burnersen/CryptBridge/windows/internal/pcloud"
	"github.com/burnersen/CryptBridge/windows/internal/prereq"
	"github.com/burnersen/CryptBridge/windows/internal/rcd"
	"github.com/burnersen/CryptBridge/windows/internal/settings"
)

// Names of the remotes in rclone's in-memory configuration.
const (
	pcloudRemote = "cbpcloud"
	vaultRemote  = "cbvault"
)

const (
	// pollEvery is how often the drive status is refreshed.
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

	// volumeName is how the drive appears in Explorer: "Vault (\\CryptBridge)".
	volumeName = `\\CryptBridge\Vault`
)

// reconnectDelays is the waiting time before each automatic reconnect
// attempt; the last value repeats.
var reconnectDelays = []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute}

// Phase is what the engine is doing, in one word.
type Phase string

const (
	PhaseStarting     Phase = "starting"
	PhaseSetup        Phase = "setup" // sign-in or vault still missing
	PhaseDisconnected Phase = "disconnected"
	PhaseConnecting   Phase = "connecting"
	PhaseConnected    Phase = "connected"
	PhaseReconnecting Phase = "reconnecting"
	PhaseError        Phase = "error"
)

// Snapshot is everything a window or tray icon needs to show.
type Snapshot struct {
	Phase             Phase          `json:"phase"`
	Message           string         `json:"message"`
	Version           string         `json:"version"`
	SignedIn          bool           `json:"signedIn"`
	SignInProblem     string         `json:"signInProblem"` // why pCloud refuses the sign-in; "" while it accepts it
	PCloudHost        string         `json:"pcloudHost"`
	OwnPCloudApp      bool           `json:"ownPCloudApp"`
	PCloudClientID    string         `json:"pcloudClientId"` // not a secret; the client secret is never sent
	SignInAppID       string         `json:"signInAppId"`    // client ID of the current sign-in; "" = rclone's app
	Usage             *pcloud.Usage  `json:"usage"`
	HasVault          bool           `json:"hasVault"`
	VaultPath         string         `json:"vaultPath"`
	Fingerprint       string         `json:"fingerprint"`
	DriveLetter       string         `json:"driveLetter"`
	Drive             drive.Status   `json:"drive"`
	Rclone            *prereq.Rclone `json:"rclone"`
	WinFsp            bool           `json:"winFsp"`
	AutoConnect       bool           `json:"autoConnect"`
	AutoReconnect     bool           `json:"autoReconnect"`
	CacheDir          string         `json:"cacheDir"`
	CacheMaxSize      string         `json:"cacheMaxSize"`
	CacheMinFreeSpace string         `json:"cacheMinFreeSpace"`
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
	return fmt.Sprintf("%d file(s) are still being uploaded to pCloud", e.Count)
}

// settleTime is how long a disconnect waits for a second look at the
// uploads. A file Windows has just finished writing reaches rclone's upload
// queue only a moment later (measured: 20 to 300 ms).
const settleTime = 1500 * time.Millisecond

// Engine ties everything together. Create it with New, then call Start.
type Engine struct {
	paths   appdir.Paths
	version string

	// actionMu serialises user actions and automatic reconnects, so that
	// "connect" and "disconnect" can never run at the same time.
	actionMu sync.Mutex

	// mu guards the fields below. It is only ever held for a moment.
	mu            sync.Mutex
	settings      settings.Settings
	rclone        *prereq.Rclone
	winFsp        bool
	client        *rcd.Client
	wantConnected bool
	fingerprint   string
	usage         *pcloud.Usage
	signInProblem string
	phase         Phase
	message       string
	driveStatus   drive.Status
	listeners     []func(Snapshot)

	// Guarded by actionMu (the monitor holds it while checking).
	failures      int
	nextReconnect time.Time
	reconnectTry  int
	lastUsage     time.Time
}

// New creates an engine; nothing happens until Start.
func New(paths appdir.Paths, version string) *Engine {
	return &Engine{paths: paths, version: version, phase: PhaseStarting}
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
	return Snapshot{
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
		HasVault:          s.HasVault(),
		VaultPath:         s.Vault.NormalisedPath(),
		Fingerprint:       e.fingerprint,
		DriveLetter:       s.Drive.Letter,
		Drive:             e.driveStatus,
		Rclone:            e.rclone,
		WinFsp:            e.winFsp,
		AutoConnect:       s.AutoConnect,
		AutoReconnect:     s.AutoReconnect,
		CacheDir:          s.Drive.CacheDir,
		CacheMaxSize:      s.Drive.CacheMaxSize,
		CacheMinFreeSpace: s.Drive.CacheMinFreeSpace,
	}
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

// Start loads the settings, finds rclone and WinFsp, starts or re-attaches
// rclone, connects the drive if wanted and starts watching. Problems are
// reported through the snapshot; only a broken installation is an error.
func (e *Engine) Start(ctx context.Context) error {
	if err := e.paths.Ensure(); err != nil {
		return fmt.Errorf("create CryptBridge folders: %w", err)
	}

	loaded, loadErr := settings.Load(e.paths.SettingsFile, e.paths.CacheDir)
	_, winFsp := prereq.WinFspInstalled()
	e.mu.Lock()
	e.settings = loaded
	e.winFsp = winFsp
	e.mu.Unlock()

	go e.monitor(ctx)

	if loadErr != nil {
		e.setPhase(PhaseError, "Your saved settings could not be read: "+loadErr.Error()+". Please set CryptBridge up again.")
	}

	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	if err := e.prepareLocked(ctx); err != nil {
		e.setPhase(PhaseError, err.Error())
		return nil
	}
	e.settlePhaseLocked(ctx, true)
	return nil
}

// prepareLocked makes sure rclone runs and knows the account and vault.
// The caller holds actionMu.
func (e *Engine) prepareLocked(ctx context.Context) error {
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
	cacheDir := e.settings.Drive.CacheDir
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

// ensureRemotesLocked registers account and vault with rclone unless they
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

	e.mu.Lock()
	s := e.settings
	e.mu.Unlock()

	if s.SignedIn() && !slices.Contains(answer.Remotes, pcloudRemote) {
		if err := pcloud.Configure(ctx, client, pcloudRemote, s.PCloud); err != nil {
			return err
		}
	}
	if s.HasVault() && !slices.Contains(answer.Remotes, vaultRemote) {
		if err := configureVault(ctx, client, s); err != nil {
			return err
		}
	}
	return nil
}

// settlePhaseLocked decides the phase after start or after setup changes,
// and connects automatically when the user wants that.
func (e *Engine) settlePhaseLocked(ctx context.Context, startup bool) {
	e.mu.Lock()
	s := e.settings
	client := e.client
	e.mu.Unlock()

	if !s.SignedIn() || !s.HasVault() {
		e.setPhase(PhaseSetup, "Sign in to pCloud and choose a vault to get started.")
		return
	}
	e.refreshFingerprint(ctx, client, s)

	status, err := drive.ReadStatus(ctx, client, vaultRemote+":", s.Drive.Letter)
	if err == nil && status.Mounted {
		// rclone kept the drive while the window was gone.
		e.mu.Lock()
		e.wantConnected = true
		e.driveStatus = status
		e.mu.Unlock()
		e.setPhase(PhaseConnected, "")
		return
	}
	if startup && s.AutoConnect {
		if err := e.connectLocked(ctx); err != nil {
			e.setPhase(PhaseError, err.Error())
		}
		return
	}
	e.setPhase(PhaseDisconnected, "")
}

// Connect mounts the drive.
func (e *Engine) Connect(ctx context.Context) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	err := e.connectLocked(ctx)
	if err != nil {
		e.setPhase(PhaseError, err.Error())
	}
	return err
}

func (e *Engine) connectLocked(ctx context.Context) error {
	e.mu.Lock()
	s := e.settings
	winFsp := e.winFsp
	e.mu.Unlock()

	if !s.SignedIn() || !s.HasVault() {
		return errors.New("sign in to pCloud and choose a vault first")
	}
	if !winFsp {
		if _, ok := prereq.WinFspInstalled(); !ok {
			return fmt.Errorf("WinFsp is not installed. It lets Windows show the vault as a drive; download it from %s", prereq.WinFspDownloadURL)
		}
		e.mu.Lock()
		e.winFsp = true
		e.mu.Unlock()
	}

	e.setPhase(PhaseConnecting, "")
	if err := e.prepareLocked(ctx); err != nil {
		return err
	}
	e.mu.Lock()
	client := e.client
	e.mu.Unlock()

	if status, err := drive.ReadStatus(ctx, client, vaultRemote+":", s.Drive.Letter); err == nil && status.Mounted {
		e.markConnected(status)
		return nil
	}
	if err := e.checkSignInLocked(ctx, client, s); err != nil {
		return err
	}
	err := drive.Mount(ctx, client, drive.Settings{
		Remote:            vaultRemote + ":",
		Letter:            s.Drive.Letter,
		VolumeName:        volumeName,
		CacheMaxSize:      s.Drive.CacheMaxSize,
		CacheMinFreeSpace: s.Drive.CacheMinFreeSpace,
	})
	if err != nil {
		return err
	}
	status, _ := drive.ReadStatus(ctx, client, vaultRemote+":", s.Drive.Letter)
	e.markConnected(status)
	return nil
}

// checkSignInLocked asks pCloud whether it accepts the sign-in before the
// drive is mounted; otherwise the drive would appear but stay empty without
// a word of explanation. Other problems (offline, pCloud busy, a slow
// answer) do not stop the connect: rclone copes with them, and the drive
// works as soon as pCloud can be reached. With automatic reconnects on, a
// refused connect is tried again in the background, so that the drive comes
// by itself once pCloud accepts the sign-in again.
func (e *Engine) checkSignInLocked(ctx context.Context, client *rcd.Client, s settings.Settings) error {
	checkCtx, cancel := context.WithTimeout(ctx, signInCheckTimeout)
	defer cancel()
	err := e.readUsageLocked(checkCtx, client)
	if !pcloud.IsSignInProblem(err) {
		return nil
	}
	if s.AutoReconnect {
		e.mu.Lock()
		e.wantConnected = true
		e.mu.Unlock()
	}
	return err
}

func (e *Engine) markConnected(status drive.Status) {
	e.mu.Lock()
	e.wantConnected = true
	e.driveStatus = status
	e.reconnectTry = 0
	e.mu.Unlock()
	e.setPhase(PhaseConnected, "")
}

// Disconnect removes the drive. Without force it refuses while uploads are
// still running and returns an *UploadsPendingError.
func (e *Engine) Disconnect(ctx context.Context, force bool) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	return e.disconnectLocked(ctx, force)
}

func (e *Engine) disconnectLocked(ctx context.Context, force bool) error {
	e.mu.Lock()
	client := e.client
	letter := e.settings.Drive.Letter
	e.mu.Unlock()
	if client == nil {
		e.setDisconnected()
		return nil
	}

	status, err := drive.ReadStatus(ctx, client, vaultRemote+":", letter)
	if err != nil {
		return err
	}
	if !status.Mounted {
		e.setDisconnected()
		return nil
	}
	if !force {
		if err := e.ensureNoUploads(ctx, client, letter, status); err != nil {
			return err
		}
	}
	if err := drive.Unmount(ctx, client, letter); err != nil {
		return err
	}
	e.setDisconnected()
	return nil
}

// ensureNoUploads returns an *UploadsPendingError if uploads are running,
// looking twice so that a file written a moment ago is not missed.
func (e *Engine) ensureNoUploads(ctx context.Context, client *rcd.Client, letter string, first drive.Status) error {
	if pending := first.PendingUploads(); pending > 0 {
		return &UploadsPendingError{Count: pending}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(settleTime):
	}
	second, err := drive.ReadStatus(ctx, client, vaultRemote+":", letter)
	if err != nil {
		return err
	}
	if pending := second.PendingUploads(); pending > 0 {
		return &UploadsPendingError{Count: pending}
	}
	return nil
}

func (e *Engine) setDisconnected() {
	e.mu.Lock()
	e.wantConnected = false
	e.driveStatus = drive.Status{}
	e.mu.Unlock()
	e.setPhase(PhaseDisconnected, "")
}

// Shutdown disconnects and stops rclone; used when the user quits
// CryptBridge. Without force it refuses while uploads are running.
func (e *Engine) Shutdown(ctx context.Context, force bool) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	if err := e.disconnectLocked(ctx, force); err != nil {
		return err
	}
	e.mu.Lock()
	client := e.client
	e.client = nil
	e.mu.Unlock()
	if client == nil {
		return nil
	}
	return rcd.Stop(ctx, client, e.paths.StateFile)
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
	want := e.wantConnected
	s := e.settings
	phase := e.phase
	e.mu.Unlock()

	if client == nil || client.Ping(ctx) != nil {
		e.failures++
		if e.failures >= lostAfterFailures && want {
			e.reconnectLocked(ctx, s, "rclone stopped unexpectedly")
		}
		return
	}
	e.failures = 0

	status, err := drive.ReadStatus(ctx, client, vaultRemote+":", s.Drive.Letter)
	if err != nil {
		return
	}
	e.mu.Lock()
	e.driveStatus = status
	e.mu.Unlock()

	switch {
	case want && !status.Mounted:
		e.reconnectLocked(ctx, s, "the drive disappeared")
	case want && status.Mounted && phase != PhaseConnected:
		e.markConnected(status)
	default:
		e.publish()
	}
	e.refreshUsage(ctx, client, s)
}

// reconnectLocked brings the drive back after rclone or the mount got lost,
// with growing pauses between attempts.
func (e *Engine) reconnectLocked(ctx context.Context, s settings.Settings, reason string) {
	if !s.AutoReconnect {
		e.mu.Lock()
		e.wantConnected = false
		e.client = nil
		e.mu.Unlock()
		e.setPhase(PhaseError, "The drive was lost: "+reason+". Click Connect to try again.")
		return
	}
	if time.Now().Before(e.nextReconnect) {
		return
	}
	delay := reconnectDelays[min(e.reconnectTry, len(reconnectDelays)-1)]
	e.reconnectTry++
	e.nextReconnect = time.Now().Add(delay)
	e.setPhase(PhaseReconnecting, fmt.Sprintf("Reconnecting (attempt %d): %s.", e.reconnectTry, reason))

	e.mu.Lock()
	if e.client != nil && e.client.Ping(ctx) != nil {
		e.client = nil
	}
	e.mu.Unlock()
	if err := e.connectLocked(ctx); err != nil {
		e.setPhase(PhaseReconnecting, fmt.Sprintf("Reconnect attempt %d failed: %v. Next try in %s.", e.reconnectTry, err, delay))
		e.mu.Lock()
		e.wantConnected = true // keep trying
		e.mu.Unlock()
	}
}

// refreshUsage asks pCloud for the free space now and then — every minute
// while pCloud refuses the sign-in, so that the warning goes away soon after
// pCloud accepts it again. The warning is kept apart from the phase: a
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
