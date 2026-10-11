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

// traySlots is how many vaults get their own entry in the tray menu; more
// can be reached through the window. Menu entries are created once, in
// order, and only shown or hidden afterwards.
const traySlots = 12

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
	slotIDs  [traySlots]string // the vault shown in each slot

	status        *systray.MenuItem
	open          *systray.MenuItem
	connectAll    *systray.MenuItem
	disconnectAll *systray.MenuItem
	slots         [traySlots]traySlot
	more          *systray.MenuItem
	lock          *systray.MenuItem
	quit          *systray.MenuItem
}

// traySlot is the entry of one vault with its submenu.
type traySlot struct {
	entry   *systray.MenuItem
	toggle  *systray.MenuItem
	explore *systray.MenuItem
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
	t.connectAll = systray.AddMenuItem("Connect all drives", "Connect every vault")
	t.disconnectAll = systray.AddMenuItem("Disconnect all drives", "Disconnect every vault")
	systray.AddSeparator()
	for i := range t.slots {
		entry := systray.AddMenuItem("", "")
		t.slots[i] = traySlot{
			entry:   entry,
			toggle:  entry.AddSubMenuItem("Connect", "Connect or disconnect this drive"),
			explore: entry.AddSubMenuItem("Open drive", "Open the drive in Explorer"),
		}
		entry.Hide()
	}
	t.more = systray.AddMenuItem("More vaults in the window…", "Show the window")
	t.more.Hide()
	systray.AddSeparator()
	t.lock = systray.AddMenuItem("Lock CryptBridge", "Close the drives and ask for the app password")
	t.lock.Hide()
	t.quit = systray.AddMenuItem("Quit CryptBridge", "Disconnect all drives and quit")

	go t.handleClicks()
	for i := range t.slots {
		go t.handleSlot(i)
	}

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
		case <-t.more.ClickedCh:
			t.app.ShowWindow()
		case <-t.connectAll.ClickedCh:
			go t.app.connectAllFromTray()
		case <-t.disconnectAll.ClickedCh:
			go t.app.disconnectAllFromTray()
		case <-t.lock.ClickedCh:
			go func() { _ = t.app.Lock() }()
		case <-t.quit.ClickedCh:
			go t.app.quitFromTray()
		}
	}
}

// handleSlot runs the actions of one vault's submenu.
func (t *tray) handleSlot(i int) {
	slot := t.slots[i]
	for {
		select {
		case <-slot.toggle.ClickedCh:
			if id := t.slotID(i); id != "" {
				go t.app.toggleFromTray(id)
			}
		case <-slot.explore.ClickedCh:
			if id := t.slotID(i); id != "" {
				go func() { _ = t.app.OpenDrive(id) }()
			}
		}
	}
}

func (t *tray) slotID(i int) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.slotIDs[i]
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
	for i := range t.slotIDs {
		t.slotIDs[i] = ""
		if i < len(s.Vaults) && !s.Locked {
			t.slotIDs[i] = s.Vaults[i].ID
		}
	}
	t.mu.Unlock()

	if changed {
		systray.SetIcon(icon.TrayICO(colour))
	}
	text := trayText(s)
	systray.SetTooltip("CryptBridge – " + text)
	t.status.SetTitle(text)

	usable := !s.Locked && len(s.Vaults) > 0 && s.Phase != engine.PhaseStarting
	enable(t.connectAll, usable)
	enable(t.disconnectAll, usable && connectedCount(s) > 0)
	if s.Locked {
		t.connectAll.SetTitle("Unlock…")
		enable(t.connectAll, true)
	} else {
		t.connectAll.SetTitle("Connect all drives")
	}

	for i := range t.slots {
		if i >= len(s.Vaults) || s.Locked {
			t.slots[i].entry.Hide()
			continue
		}
		t.showSlot(t.slots[i], s.Vaults[i])
	}
	if len(s.Vaults) > traySlots && !s.Locked {
		t.more.Show()
	} else {
		t.more.Hide()
	}
	if s.AppLock && !s.Locked {
		t.lock.Show()
	} else {
		t.lock.Hide()
	}
}

// showSlot shows one vault: "Films (Z:) – connected", with its actions.
func (t *tray) showSlot(slot traySlot, v engine.VaultSnapshot) {
	letter := v.Letter + ":"
	slot.entry.SetTitle(fmt.Sprintf("%s (%s) – %s", v.Name, letter, stateWord(v)))
	slot.entry.Show()
	switch {
	case v.State == engine.StateWaiting:
		slot.toggle.SetTitle("Stop waiting for " + letter)
	case isActive(v):
		slot.toggle.SetTitle("Disconnect " + letter)
	default:
		slot.toggle.SetTitle("Connect " + letter)
	}
	enable(slot.toggle, v.HasKeys)
	enable(slot.explore, v.Drive.Mounted)
}

func enable(item *systray.MenuItem, on bool) {
	if on {
		item.Enable()
	} else {
		item.Disable()
	}
}

// isActive: the drive is connected or CryptBridge is trying to connect it,
// so the action offered is "disconnect".
func isActive(v engine.VaultSnapshot) bool {
	switch v.State {
	case engine.StateConnected, engine.StateReconnecting, engine.StateWaiting, engine.StateConnecting:
		return true
	}
	return false
}

func connectedCount(s engine.Snapshot) int {
	n := 0
	for _, v := range s.Vaults {
		if isActive(v) {
			n++
		}
	}
	return n
}

// stateWord is the state of a drive in a word or two.
func stateWord(v engine.VaultSnapshot) string {
	switch v.State {
	case engine.StateConnected:
		if n := v.Drive.PendingUploads(); n > 0 {
			return fmt.Sprintf("writing %d file(s)", n)
		}
		return "connected"
	case engine.StateConnecting:
		return "connecting…"
	case engine.StateReconnecting:
		return "reconnecting…"
	case engine.StateWaiting:
		return "waiting for its folder"
	case engine.StateError:
		return "problem"
	}
	return "disconnected"
}

// trayState picks the icon colour: green while drives are connected, amber
// while working or uploading (do not switch the PC off), red when CryptBridge
// or a drive has a problem, grey otherwise.
func trayState(s engine.Snapshot) (string, icon.Colour) {
	busy, connected := false, false
	for _, v := range s.Vaults {
		switch v.State {
		case engine.StateError:
			return "failed", icon.Failed
		case engine.StateConnecting, engine.StateReconnecting:
			busy = true
		case engine.StateConnected:
			connected = true
		}
	}
	switch {
	case s.Phase == engine.PhaseError:
		return "failed", icon.Failed
	case busy || s.Phase == engine.PhaseStarting || s.PendingUploads() > 0:
		return "busy", icon.Busy
	case connected:
		return "connected", icon.Connected
	}
	return "disconnected", icon.Disconnected
}

// trayText is the one-line status for tooltip and menu.
func trayText(s engine.Snapshot) string {
	uploads := ""
	if n := s.PendingUploads(); n > 0 {
		uploads = fmt.Sprintf(", writing %d file(s)", n)
	}
	switch s.Phase {
	case engine.PhaseStarting:
		return "Starting…"
	case engine.PhaseSetup:
		return "Setup needed"
	case engine.PhaseError:
		return "Problem – open CryptBridge for details"
	case engine.PhaseLocked:
		if uploads != "" {
			return "Locked" + uploads
		}
		return "Locked"
	}
	for _, v := range s.Vaults {
		if v.State == engine.StateError {
			return fmt.Sprintf("Problem with %s (%s:) – open CryptBridge", v.Name, v.Letter)
		}
	}
	connected := 0
	for _, v := range s.Vaults {
		if v.State == engine.StateConnected {
			connected++
		}
	}
	if len(s.Vaults) == 1 && connected == 1 {
		return "Connected (" + s.Vaults[0].Letter + ":)" + uploads
	}
	if connected == 0 {
		return "Disconnected"
	}
	return fmt.Sprintf("%d of %d drives connected%s", connected, len(s.Vaults), uploads)
}
