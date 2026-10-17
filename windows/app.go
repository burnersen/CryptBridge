// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/appdir"
	"github.com/burnersen/CryptBridge/windows/internal/cloud"
	"github.com/burnersen/CryptBridge/windows/internal/drive"
	"github.com/burnersen/CryptBridge/windows/internal/engine"
	"github.com/burnersen/CryptBridge/windows/internal/prereq"
	"github.com/burnersen/CryptBridge/windows/internal/settings"
	"github.com/burnersen/CryptBridge/windows/internal/vault"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// shutdownTimeout limits how long quitting may take (unmount + stop rclone).
const shutdownTimeout = 30 * time.Second

// sessionEndTimeout: when the user signs out, Windows ends the program a
// few seconds after telling it.
const sessionEndTimeout = 4 * time.Second

// App connects the window and the tray icon to the engine. Its exported
// methods are called from the window's JavaScript.
type App struct {
	core        *engine.Engine
	paths       appdir.Paths
	startHidden bool
	tray        *tray
	session     atomic.Pointer[sessionWatcher] // empty if Windows' lock messages are not available

	// ctx is Wails' context (needed for dialogs and events); coreCtx lives
	// as long as the engine should run.
	ctx        context.Context
	coreCtx    context.Context
	stopCore   context.CancelFunc
	signInMu   sync.Mutex
	stopSignIn context.CancelFunc
	// signInAnswers carries the answer to a question rclone asked during a
	// sign-in (see askInWindow).
	signInAnswers chan string
	quitOnce      sync.Once
	themeMu       sync.Mutex
	theme         string // the theme the window shows now
}

// NewApp wires the parts together; nothing runs before startup.
func NewApp(core *engine.Engine, paths appdir.Paths, startHidden bool) *App {
	app := &App{core: core, paths: paths, startHidden: startHidden, signInAnswers: make(chan string)}
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
		a.applyTheme(s.Theme)
	})
	a.tray.start()
	go func() {
		if err := a.core.Start(a.coreCtx); err != nil {
			a.showError("CryptBridge could not start", err)
			return
		}
		a.askForPasswordIfWanted()
		session, err := startSessionWatcher(a.onSessionEvent)
		if err != nil {
			a.showError("CryptBridge cannot notice when Windows is locked", err)
			return
		}
		a.session.Store(session)
	}()
}

// shutdown is called by Wails when the program ends.
func (a *App) shutdown(context.Context) {
	if session := a.session.Load(); session != nil {
		session.stop()
	}
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

// applyTheme gives the window frame and background the chosen theme; the
// page follows the same choice by itself.
func (a *App) applyTheme(theme string) {
	a.themeMu.Lock()
	defer a.themeMu.Unlock()
	if theme == "" || theme == a.theme {
		return
	}
	a.theme = theme
	dark := theme == settings.ThemeDark || (theme == settings.ThemeAuto && windowsUsesDarkTheme())
	switch theme {
	case settings.ThemeLight:
		runtime.WindowSetLightTheme(a.ctx)
	case settings.ThemeDark:
		runtime.WindowSetDarkTheme(a.ctx)
	default:
		runtime.WindowSetSystemDefaultTheme(a.ctx)
	}
	if dark {
		runtime.WindowSetBackgroundColour(a.ctx, darkBackground.R, darkBackground.G, darkBackground.B, 255)
	} else {
		runtime.WindowSetBackgroundColour(a.ctx, lightBackground.R, lightBackground.G, lightBackground.B, 255)
	}
}

// onSessionEvent locks CryptBridge together with Windows (Win+L, screen
// lock, signing out) and asks for the app password when the user is back
// and drives were connected before.
func (a *App) onSessionEvent(event sessionEvent) {
	snap := a.core.Snapshot()
	if !snap.AppLock {
		return
	}
	switch event {
	case sessionLocked:
		if !snap.Locked {
			a.CancelSignIn()           // a sign-in waiting for the browser would hold up locking
			_ = a.core.Lock(a.coreCtx) // problems show up in the window
		}
	case sessionEnding:
		ctx, cancel := context.WithTimeout(a.coreCtx, sessionEndTimeout)
		defer cancel()
		a.CancelSignIn()
		_ = a.core.LockNow(ctx)
	case sessionUnlocked:
		a.askForPasswordIfWanted()
	}
}

// askForPasswordIfWanted shows the window with the password field when
// drives are to be connected after unlocking.
func (a *App) askForPasswordIfWanted() {
	if snap := a.core.Snapshot(); snap.Locked && len(snap.UnlockConnects) > 0 {
		a.ShowWindow()
	}
}

// Unlock opens CryptBridge with the app password.
func (a *App) Unlock(password string) error {
	return a.core.Unlock(a.coreCtx, password)
}

// Lock locks CryptBridge now.
func (a *App) Lock() error {
	a.CancelSignIn() // a sign-in waiting for the browser would hold up locking
	return a.core.Lock(a.coreCtx)
}

// EnableLock sets the app password.
func (a *App) EnableLock(password string) error {
	return a.core.EnableLock(password)
}

// ChangePassword replaces the app password.
func (a *App) ChangePassword(oldPassword, newPassword string) error {
	return a.core.ChangePassword(oldPassword, newPassword)
}

// DisableLock turns the app lock off.
func (a *App) DisableLock(password string) error {
	return a.core.DisableLock(password)
}

// DeclineLock: the user does not want an app password now.
func (a *App) DeclineLock() error {
	return a.core.DeclineLock()
}

// ResetApp forgets sign-in, vaults and app password (forgotten app password).
func (a *App) ResetApp() error {
	return a.core.Reset(a.coreCtx)
}

// Services lists the cloud services an account can be added for.
func (a *App) Services() []cloud.Service {
	return a.core.Services()
}

// AddAccount signs in to a cloud service in the browser and adds the
// account; it returns the new account's ID. See signInContext for what the
// window gets meanwhile.
func (a *App) AddAccount(req engine.NewAccount) (string, error) {
	ctx, done := a.signInContext()
	defer done()
	return a.core.AddAccount(ctx, req, a.showSignInLink, a.askInWindow)
}

// SignIn signs in to an existing account again.
func (a *App) SignIn(id string) error {
	ctx, done := a.signInContext()
	defer done()
	return a.core.SignIn(ctx, id, a.showSignInLink, a.askInWindow)
}

// signInContext starts a sign-in that CancelSignIn can stop. While it runs,
// the window gets the sign-in link ("signin-link") in case the browser does
// not open by itself, and any question rclone asks ("signin-question"),
// to be answered with AnswerSignInQuestion.
func (a *App) signInContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(a.coreCtx)
	a.signInMu.Lock()
	a.stopSignIn = cancel
	a.signInMu.Unlock()
	return ctx, cancel
}

func (a *App) showSignInLink(link string) {
	runtime.EventsEmit(a.ctx, "signin-link", link)
}

// askInWindow shows a question of rclone in the window and waits for the
// answer, or for the sign-in to be cancelled.
func (a *App) askInWindow(ctx context.Context, q cloud.Question) (string, error) {
	runtime.EventsEmit(a.ctx, "signin-question", q)
	select {
	case answer := <-a.signInAnswers:
		return answer, nil
	case <-ctx.Done():
		return "", errors.New("the sign-in was cancelled")
	}
}

// AnswerSignInQuestion answers the question the window shows. An answer
// while no question waits is dropped: the sign-in has ended meanwhile.
func (a *App) AnswerSignInQuestion(answer string) {
	select {
	case a.signInAnswers <- answer:
	default:
	}
}

// CancelSignIn stops a running sign-in.
func (a *App) CancelSignIn() {
	a.signInMu.Lock()
	defer a.signInMu.Unlock()
	if a.stopSignIn != nil {
		a.stopSignIn()
	}
}

// SignOut forgets the sign-in of an account.
func (a *App) SignOut(id string) error {
	return a.core.SignOut(a.coreCtx, id)
}

// SetAccountApp stores the codes of the user's own app for an account (both
// empty: rclone's app again, where allowed). It takes effect with the next
// sign-in.
func (a *App) SetAccountApp(id, clientID, clientSecret string) error {
	return a.core.SetAccountApp(id, clientID, clientSecret)
}

// RenameAccount changes the name an account is shown with.
func (a *App) RenameAccount(id, name string) error {
	return a.core.RenameAccount(id, name)
}

// RemoveAccount makes CryptBridge forget an account without vaults.
func (a *App) RemoveAccount(id string) error {
	return a.core.RemoveAccount(a.coreCtx, id)
}

// NewPassword returns a strong random password.
func (a *App) NewPassword() (string, error) {
	return engine.NewPassword()
}

// DefaultVault returns the settings of a new vault in the given folder.
func (a *App) DefaultVault(path string) vault.Config {
	return vault.NewDefault(path)
}

// FindVaults scans a cloud account for existing vaults.
func (a *App) FindVaults(accountID string) ([]vault.Candidate, error) {
	return a.core.FindVaults(a.coreCtx, accountID)
}

// InspectFolder looks into a folder while the user chooses where a vault
// is (kind "cloud" with an account, or "local").
func (a *App) InspectFolder(kind, accountID, path string) (vault.Folder, error) {
	return a.core.InspectFolder(a.coreCtx, kind, accountID, path)
}

// ChooseVaultFolder lets the user pick a folder of this PC for a vault.
func (a *App) ChooseVaultFolder(current string) (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:                "Folder for the vault",
		DefaultDirectory:     current,
		CanCreateDirectories: true,
	})
}

// CheckVault tests keys against a folder without saving anything.
func (a *App) CheckVault(req engine.NewVault) (engine.VaultCheck, error) {
	return a.core.CheckVault(a.coreCtx, req)
}

// AddVault adds a vault (Create = new vault in an empty folder).
func (a *App) AddVault(req engine.NewVault) (engine.VaultCheck, error) {
	return a.core.AddVault(a.coreCtx, req)
}

// UpdateVault changes name, drive letter and "connect on start" of a vault.
func (a *App) UpdateVault(id string, change engine.VaultChange) error {
	return a.core.UpdateVault(id, change)
}

// RemoveVault makes CryptBridge forget a vault; its files stay untouched.
func (a *App) RemoveVault(id, confirmName string) error {
	return a.core.RemoveVault(a.coreCtx, id, confirmName)
}

// Connect connects the drive of a vault.
func (a *App) Connect(id string) error {
	return a.core.Connect(a.coreCtx, id)
}

// UseFolder connects a local vault to the folder now at its place, although
// it is not the folder it was last connected to.
func (a *App) UseFolder(id string) error {
	return a.core.UseFolder(a.coreCtx, id)
}

// ConnectAll connects every drive.
func (a *App) ConnectAll() error {
	return a.core.ConnectAll(a.coreCtx)
}

// Disconnect removes the drive of a vault. If uploads are still running and
// force is false, nothing happens and the number of pending uploads is
// returned, so that the window can ask the user.
func (a *App) Disconnect(id string, force bool) (int, error) {
	return pendingOf(a.core.Disconnect(a.coreCtx, id, force))
}

// DisconnectAll removes every drive, asking like Disconnect.
func (a *App) DisconnectAll(force bool) (int, error) {
	return pendingOf(a.core.DisconnectAll(a.coreCtx, force))
}

// pendingOf turns an *engine.UploadsPendingError into its count.
func pendingOf(err error) (int, error) {
	var pending *engine.UploadsPendingError
	if errors.As(err, &pending) {
		return pending.Count, nil
	}
	return 0, err
}

// OpenDrive opens the drive of a vault in Explorer.
func (a *App) OpenDrive(id string) error {
	letter, err := a.core.OpenDriveLetter(id)
	if err != nil {
		return err
	}
	return exec.Command("explorer.exe", letter+`:\`).Start()
}

// FreeLetters lists the drive letters a vault can use: free in Windows and
// not given to another vault, plus its own. Use "" for a new vault.
func (a *App) FreeLetters(id string) ([]string, error) {
	free, err := drive.FreeLetters()
	if err != nil {
		return nil, err
	}
	letters := []string{}
	own := ""
	taken := map[string]bool{}
	for _, v := range a.core.Snapshot().Vaults {
		if v.ID == id {
			own = v.Letter
		} else {
			taken[v.Letter] = true
		}
	}
	for _, l := range free {
		if !taken[l] {
			letters = append(letters, l)
		}
	}
	if own != "" && !slices.Contains(letters, own) {
		letters = append(letters, own)
	}
	slices.Sort(letters)
	return letters, nil
}

// CountSize starts counting the size of a vault.
func (a *App) CountSize(id string) error {
	return a.core.CountSize(a.coreCtx, id)
}

// CancelCount stops counting the size of a vault.
func (a *App) CancelCount(id string) error {
	return a.core.CancelCount(a.coreCtx, id)
}

// UpdateOptions stores the settings from the window.
func (a *App) UpdateOptions(o engine.Options) error {
	return a.core.UpdateOptions(a.coreCtx, o)
}

// SystemTheme tells the window whether Windows uses its dark or its light
// theme for apps.
func (a *App) SystemTheme() string {
	if windowsUsesDarkTheme() {
		return settings.ThemeDark
	}
	return settings.ThemeLight
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
		"https://docs.pcloud.com/my_apps/",        // where users request their own pCloud app
		"https://console.cloud.google.com/",       // where users set up their own Google app
		"https://www.dropbox.com/developers/apps", // where users set up their own Dropbox app
		"https://winfsp.dev/",
		"https://github.com/winfsp/winfsp", // named in WinFsp's licence notice
		"https://rclone.org/",
		"https://www.gnu.org/licenses/",
		"http://127.0.0.1:", // the sign-in link printed by rclone
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
	a.CancelSignIn() // a sign-in waiting for the browser would hold up quitting
	pending, err := pendingOf(a.core.Shutdown(ctx, force))
	if pending > 0 {
		return pending, nil
	}
	if err != nil {
		a.showError("rclone did not stop cleanly", err)
	}
	a.quitOnce.Do(func() { runtime.Quit(a.ctx) })
	return 0, nil
}

// askAboutUploads asks whether to interrupt running uploads; the files stay
// on this PC and are written into the vault with the next connect.
func (a *App) askAboutUploads(pending int, action string) bool {
	a.ShowWindow()
	answer, _ := runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
		Type:  runtime.QuestionDialog,
		Title: "Uploads still running",
		Message: fmt.Sprintf("%d file(s) are still being written into the vault.\n\n"+
			"If you %s now, they stay on this PC and are written into the vault the next time you connect. "+
			"Do not delete the cache folder in the meantime.\n\n%s anyway?", pending, strings.ToLower(action), action),
	})
	return answer == "Yes"
}

// quitFromTray asks before interrupting uploads, then quits.
func (a *App) quitFromTray() {
	pending, err := a.Quit(false)
	if err != nil || pending == 0 {
		return
	}
	if a.askAboutUploads(pending, "Quit") {
		_, _ = a.Quit(true)
	}
}

// toggleFromTray connects or disconnects one drive, asking before
// interrupting uploads.
func (a *App) toggleFromTray(id string) {
	snap := a.core.Snapshot()
	if snap.Locked {
		a.ShowWindow() // the window asks for the app password
		return
	}
	connected := false
	for _, v := range snap.Vaults {
		if v.ID == id {
			connected = v.State == engine.StateConnected || v.State == engine.StateReconnecting || v.State == engine.StateWaiting
		}
	}
	if !connected {
		if err := a.Connect(id); err != nil {
			a.showError("Could not connect the drive", err)
		}
		return
	}
	a.disconnectFromTray(func(force bool) (int, error) { return a.Disconnect(id, force) })
}

// connectAllFromTray connects every drive.
func (a *App) connectAllFromTray() {
	if a.core.Snapshot().Locked {
		a.ShowWindow()
		return
	}
	if err := a.ConnectAll(); err != nil {
		a.showError("Not every drive could be connected", err)
	}
}

// disconnectAllFromTray disconnects every drive, asking before interrupting
// uploads.
func (a *App) disconnectAllFromTray() {
	a.disconnectFromTray(a.DisconnectAll)
}

func (a *App) disconnectFromTray(disconnect func(force bool) (int, error)) {
	pending, err := disconnect(false)
	if err != nil {
		a.showError("Could not disconnect", err)
		return
	}
	if pending == 0 || !a.askAboutUploads(pending, "Disconnect") {
		return
	}
	if _, err := disconnect(true); err != nil {
		a.showError("Could not disconnect", err)
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
