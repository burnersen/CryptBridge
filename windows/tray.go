// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"fmt"
	goruntime "runtime"
	"sync"

	"fyne.io/systray"
	"github.com/burnersen/CryptBridge/windows/internal/engine"
	"github.com/burnersen/CryptBridge/windows/internal/icon"
)

// tray is the icon in the notification area and its menu.
//
// Windows delivers the messages of the tray icon to the thread that created
// it, so the icon gets a thread of its own with its own message loop
// (systray.Run on a locked thread). The window of Wails runs on the main
// thread; the two never wait for each other.
type tray struct {
	app *App

	mu       sync.Mutex
	ready    bool
	pending  *engine.Snapshot // update that arrived before the menu existed
	lastIcon string

	status  *systray.MenuItem
	open    *systray.MenuItem
	toggle  *systray.MenuItem
	explore *systray.MenuItem
	quit    *systray.MenuItem
}

func newTray(app *App) *tray {
	return &tray{app: app}
}

// start runs the tray on its own thread.
func (t *tray) start() {
	go func() {
		goruntime.LockOSThread()
		systray.Run(t.onReady, nil)
	}()
}

// stop removes the icon.
func (t *tray) stop() {
	systray.Quit()
}

func (t *tray) onReady() {
	systray.SetIcon(icon.TrayICO(icon.Disconnected))
	systray.SetTooltip("CryptBridge")
	systray.SetOnTapped(t.app.ShowWindow)

	t.status = systray.AddMenuItem("CryptBridge", "")
	t.status.Disable()
	systray.AddSeparator()
	t.open = systray.AddMenuItem("Open CryptBridge", "Show the window")
	t.toggle = systray.AddMenuItem("Connect drive", "Connect or disconnect the vault drive")
	t.explore = systray.AddMenuItem("Open drive", "Open the drive in Explorer")
	t.explore.Disable()
	systray.AddSeparator()
	t.quit = systray.AddMenuItem("Quit CryptBridge", "Disconnect the drive and quit")

	go t.handleClicks()

	t.mu.Lock()
	t.ready = true
	pending := t.pending
	t.pending = nil
	t.mu.Unlock()
	if pending != nil {
		t.update(*pending)
	}
}

// handleClicks runs the menu actions. Each runs in its own goroutine so that
// a slow action (a mount can take seconds) never blocks the menu.
func (t *tray) handleClicks() {
	for {
		select {
		case <-t.open.ClickedCh:
			t.app.ShowWindow()
		case <-t.toggle.ClickedCh:
			go t.app.toggleFromTray()
		case <-t.explore.ClickedCh:
			go func() { _ = t.app.OpenDrive() }()
		case <-t.quit.ClickedCh:
			go t.app.quitFromTray()
		}
	}
}

// update shows a new snapshot in icon, tooltip and menu.
func (t *tray) update(s engine.Snapshot) {
	t.mu.Lock()
	if !t.ready {
		t.pending = &s
		t.mu.Unlock()
		return
	}
	state, colour := trayState(s)
	changed := state != t.lastIcon
	t.lastIcon = state
	t.mu.Unlock()

	if changed {
		systray.SetIcon(icon.TrayICO(colour))
	}
	text := trayText(s)
	systray.SetTooltip("CryptBridge – " + text)
	t.status.SetTitle(text)

	letter := s.DriveLetter + ":"
	connected := s.Phase == engine.PhaseConnected || s.Phase == engine.PhaseReconnecting
	if connected {
		t.toggle.SetTitle("Disconnect drive " + letter)
		t.explore.SetTitle("Open drive " + letter)
		t.explore.Enable()
	} else {
		t.toggle.SetTitle("Connect drive " + letter)
		t.explore.Disable()
	}
	if s.Phase == engine.PhaseSetup || s.Phase == engine.PhaseStarting {
		t.toggle.Disable()
	} else {
		t.toggle.Enable()
	}
}

// trayState picks the icon colour: green connected, amber while working or
// uploading (do not switch the PC off), red on errors, grey otherwise.
func trayState(s engine.Snapshot) (string, icon.Colour) {
	switch {
	case s.Phase == engine.PhaseError:
		return "failed", icon.Failed
	case s.Phase == engine.PhaseConnecting || s.Phase == engine.PhaseReconnecting || s.Phase == engine.PhaseStarting:
		return "busy", icon.Busy
	case s.Phase == engine.PhaseConnected && s.Drive.PendingUploads() > 0:
		return "busy", icon.Busy
	case s.Phase == engine.PhaseConnected:
		return "connected", icon.Connected
	}
	return "disconnected", icon.Disconnected
}

// trayText is the one-line status for tooltip and menu.
func trayText(s engine.Snapshot) string {
	letter := s.DriveLetter + ":"
	switch s.Phase {
	case engine.PhaseConnected:
		if n := s.Drive.PendingUploads(); n > 0 {
			return fmt.Sprintf("Connected (%s), uploading %d file(s)", letter, n)
		}
		return "Connected (" + letter + ")"
	case engine.PhaseConnecting:
		return "Connecting…"
	case engine.PhaseReconnecting:
		return "Reconnecting…"
	case engine.PhaseSetup:
		return "Setup needed"
	case engine.PhaseError:
		return "Problem – open CryptBridge for details"
	case engine.PhaseStarting:
		return "Starting…"
	}
	return "Disconnected"
}
