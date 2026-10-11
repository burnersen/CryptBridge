// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/appdir"
	"github.com/burnersen/CryptBridge/windows/internal/drive"
	"github.com/burnersen/CryptBridge/windows/internal/engine"
	"github.com/burnersen/CryptBridge/windows/internal/pcloud"
	"github.com/burnersen/CryptBridge/windows/internal/prereq"
	"github.com/burnersen/CryptBridge/windows/internal/vault"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// shutdownTimeout limits how long quitting may take (unmount + stop rclone).
const shutdownTimeout = 30 * time.Second

// App connects the window and the tray icon to the engine. Its exported
// methods are called from the window's JavaScript.
type App struct {
	core        *engine.Engine
	paths       appdir.Paths
	startHidden bool
	tray        *tray

	// ctx is Wails' context (needed for dialogs and events); coreCtx lives
	// as long as the engine should run.
	ctx        context.Context
	coreCtx    context.Context
	stopCore   context.CancelFunc
	signInMu   sync.Mutex
	stopSignIn context.CancelFunc
	quitOnce   sync.Once
}

// NewApp wires the parts together; nothing runs before startup.
func NewApp(core *engine.Engine, paths appdir.Paths, startHidden bool) *App {
	app := &App{core: core, paths: paths, startHidden: startHidden}
	app.tray = newTray(app)
	return app
}

// startup is called by Wails once the window exists.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.coreCtx, a.stopCore = context.WithCancel(context.Background())
	a.core.Subscribe(func(s engine.Snapshot) {
		runtime.EventsEmit(ctx, "snapshot", s)
		a.tray.update(s)
	})
	a.tray.start()
	go func() {
		if err := a.core.Start(a.coreCtx); err != nil {
			a.showError("CryptBridge could not start", err)
		}
	}()
}

// shutdown is called by Wails when the program ends.
func (a *App) shutdown(context.Context) {
	a.tray.stop()
	if a.stopCore != nil {
		a.stopCore()
	}
}

// onSecondInstance: starting CryptBridge again just shows the window.
func (a *App) onSecondInstance(options.SecondInstanceData) {
	a.ShowWindow()
}

// ShowWindow brings the window to the front.
func (a *App) ShowWindow() {
	runtime.WindowShow(a.ctx)
	runtime.WindowUnminimise(a.ctx)
}

// GetSnapshot returns the current state for the window.
func (a *App) GetSnapshot() engine.Snapshot {
	return a.core.Snapshot()
}

// SignIn opens the pCloud sign-in in the browser and waits for it. The link
// is also sent to the window ("signin-link") in case the browser does not
// open by itself.
func (a *App) SignIn() error {
	ctx, cancel := context.WithCancel(a.coreCtx)
	a.signInMu.Lock()
	a.stopSignIn = cancel
	a.signInMu.Unlock()
	defer cancel()
	return a.core.SignIn(ctx, func(link string) { runtime.EventsEmit(a.ctx, "signin-link", link) })
}

// CancelSignIn stops a running sign-in.
func (a *App) CancelSignIn() {
	a.signInMu.Lock()
	defer a.signInMu.Unlock()
	if a.stopSignIn != nil {
		a.stopSignIn()
	}
}

// SignOut forgets the pCloud sign-in.
func (a *App) SignOut() error {
	return a.core.SignOut(a.coreCtx)
}

// SetPCloudApp stores the codes of the user's own pCloud app (both empty:
// rclone's built-in app again). It takes effect with the next sign-in.
func (a *App) SetPCloudApp(clientID, clientSecret string) error {
	return a.core.SetPCloudApp(clientID, clientSecret)
}

// PCloudRedirectURL is the address an own pCloud app must allow.
func (a *App) PCloudRedirectURL() string {
	return pcloud.RedirectURL
}

// NewPassword returns a strong random password.
func (a *App) NewPassword() (string, error) {
	return engine.NewPassword()
}

// DefaultVault returns the settings of a new vault in the given folder.
func (a *App) DefaultVault(path string) vault.Config {
	return vault.NewDefault(path)
}

// FindVaults scans pCloud for existing vaults.
func (a *App) FindVaults() ([]vault.Candidate, error) {
	return a.core.FindVaults(a.coreCtx)
}

// CheckVault tests keys against a folder without saving anything.
func (a *App) CheckVault(cfg vault.Config) (engine.VaultCheck, error) {
	return a.core.CheckVault(a.coreCtx, cfg)
}

// UseVault stores a vault (create = new vault in an empty folder).
func (a *App) UseVault(cfg vault.Config, create, acceptUnverifiable bool) (engine.VaultCheck, error) {
	return a.core.UseVault(a.coreCtx, cfg, create, acceptUnverifiable)
}

// Connect mounts the drive.
func (a *App) Connect() error {
	return a.core.Connect(a.coreCtx)
}

// Disconnect removes the drive. If uploads are still running and force is
// false, nothing happens and the number of pending uploads is returned, so
// that the window can ask the user.
func (a *App) Disconnect(force bool) (int, error) {
	err := a.core.Disconnect(a.coreCtx, force)
	var pending *engine.UploadsPendingError
	if errors.As(err, &pending) {
		return pending.Count, nil
	}
	return 0, err
}

// OpenDrive opens the drive in Explorer.
func (a *App) OpenDrive() error {
	letter := a.core.Snapshot().DriveLetter
	return exec.Command("explorer.exe", letter+`:\`).Start()
}

// FreeLetters lists drive letters that can be used, plus the current one.
func (a *App) FreeLetters() ([]string, error) {
	free, err := drive.FreeLetters()
	if err != nil {
		return nil, err
	}
	current := a.core.Snapshot().DriveLetter
	for _, l := range free {
		if l == current {
			return free, nil
		}
	}
	return append(free, current), nil
}

// UpdateOptions stores the settings from the window.
func (a *App) UpdateOptions(o engine.Options) error {
	return a.core.UpdateOptions(a.coreCtx, o)
}

// ChooseFolder lets the user pick a folder (for the cache).
func (a *App) ChooseFolder(current string) (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "Folder for the CryptBridge cache",
		DefaultDirectory: current,
	})
}

// AutostartEnabled reports whether CryptBridge starts with Windows.
func (a *App) AutostartEnabled() bool {
	return autostartEnabled()
}

// SetAutostart turns starting with Windows on or off.
func (a *App) SetAutostart(enabled bool) error {
	return setAutostart(enabled)
}

// OpenWinFspDownload opens the official WinFsp download page.
func (a *App) OpenWinFspDownload() {
	runtime.BrowserOpenURL(a.ctx, prereq.WinFspDownloadURL)
}

// OpenURL opens a link from the window in the default browser. Only the
// project's own pages and the official tool pages are allowed.
func (a *App) OpenURL(url string) error {
	for _, allowed := range []string{
		"https://github.com/burnersen/CryptBridge",
		"https://docs.pcloud.com/my_apps/", // where users request their own pCloud app
		"https://winfsp.dev/",
		"https://rclone.org/",
		"https://www.gnu.org/licenses/",
		"http://127.0.0.1:", // the pCloud sign-in link printed by rclone
	} {
		if strings.HasPrefix(url, allowed) {
			runtime.BrowserOpenURL(a.ctx, url)
			return nil
		}
	}
	return fmt.Errorf("refusing to open %q", url)
}

// Quit ends CryptBridge: disconnect, stop rclone, close. If uploads are
// still running and force is false, nothing happens and the number of
// pending uploads is returned.
func (a *App) Quit(force bool) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err := a.core.Shutdown(ctx, force)
	var pending *engine.UploadsPendingError
	if errors.As(err, &pending) {
		return pending.Count, nil
	}
	if err != nil {
		a.showError("rclone did not stop cleanly", err)
	}
	a.quitOnce.Do(func() { runtime.Quit(a.ctx) })
	return 0, nil
}

// quitFromTray asks before interrupting uploads, then quits.
func (a *App) quitFromTray() {
	pending, err := a.Quit(false)
	if err != nil || pending == 0 {
		return
	}
	a.ShowWindow()
	answer, _ := runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
		Type:  runtime.QuestionDialog,
		Title: "Uploads still running",
		Message: fmt.Sprintf("%d file(s) are still being uploaded to pCloud.\n\n"+
			"If you quit now, they stay on this PC and are uploaded the next time you connect. "+
			"Do not delete the cache folder in the meantime.\n\nQuit anyway?", pending),
	})
	if answer == "Yes" {
		_, _ = a.Quit(true)
	}
}

// toggleFromTray connects or disconnects, asking before interrupting uploads.
func (a *App) toggleFromTray() {
	snap := a.core.Snapshot()
	if snap.Phase != engine.PhaseConnected && snap.Phase != engine.PhaseReconnecting {
		if err := a.Connect(); err != nil {
			a.showError("Could not connect the drive", err)
		}
		return
	}
	pending, err := a.Disconnect(false)
	if err != nil {
		a.showError("Could not disconnect the drive", err)
		return
	}
	if pending == 0 {
		return
	}
	a.ShowWindow()
	answer, _ := runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
		Type:  runtime.QuestionDialog,
		Title: "Uploads still running",
		Message: fmt.Sprintf("%d file(s) are still being uploaded to pCloud.\n\n"+
			"If you disconnect now, they stay on this PC and are uploaded the next time you connect.\n\n"+
			"Disconnect anyway?", pending),
	})
	if answer == "Yes" {
		if _, err := a.Disconnect(true); err != nil {
			a.showError("Could not disconnect the drive", err)
		}
	}
}

// showError shows an error in a message box.
func (a *App) showError(title string, err error) {
	_, _ = runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
		Type:    runtime.ErrorDialog,
		Title:   title,
		Message: err.Error(),
	})
}
