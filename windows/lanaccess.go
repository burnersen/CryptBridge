// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/burnersen/CryptBridge/windows/internal/lan"
	"github.com/burnersen/CryptBridge/windows/internal/settings"
)

// The LAN access on the Settings page: its settings, and the Windows
// firewall rule that lets the devices of the home network in.

// LANSettings returns the LAN settings with the password.
func (a *App) LANSettings() (settings.LAN, error) {
	return a.core.LANSettings()
}

// UpdateLAN stores the LAN settings and starts, changes or stops the
// server.
func (a *App) UpdateLAN(change settings.LAN) error {
	return a.core.UpdateLAN(a.coreCtx, change)
}

// NewLANPassword returns a random password that is easy to type, also with
// the remote control of a TV.
func (a *App) NewLANPassword() (string, error) {
	return lan.NewPassword()
}

// LANFirewall tells what the Windows firewall does about the LAN access on
// the saved port.
func (a *App) LANFirewall() (lan.Firewall, error) {
	program, err := a.rcloneProgram()
	if err != nil {
		return lan.Firewall{}, err
	}
	return lan.ReadFirewall(a.coreCtx, a.core.Snapshot().LAN.Port, program)
}

// AllowLANFirewall creates the firewall rule for the saved port. Windows
// asks the user for permission first.
func (a *App) AllowLANFirewall() error {
	program, err := a.rcloneProgram()
	if err != nil {
		return err
	}
	return explainDeclined(lan.AllowThroughFirewall(a.core.Snapshot().LAN.Port, program))
}

// RemoveLANFirewall removes the firewall rule. Windows asks the user for
// permission first.
func (a *App) RemoveLANFirewall() error {
	return explainDeclined(lan.RemoveFirewallRule())
}

// RemoveOtherLANRules removes the other rules that let rclone in (see
// lan.Firewall.Others): exactly those found now for this rclone.exe, not a
// list from the window. Windows asks the user for permission first.
func (a *App) RemoveOtherLANRules() error {
	program, err := a.rcloneProgram()
	if err != nil {
		return err
	}
	state, err := lan.ReadFirewall(a.coreCtx, a.core.Snapshot().LAN.Port, program)
	if err != nil {
		return err
	}
	ids := make([]string, len(state.Others))
	for i, rule := range state.Others {
		ids[i] = rule.ID
	}
	return explainDeclined(lan.RemoveRules(ids))
}

// OpenNetworkSettings opens Windows' network settings, where a network is
// marked as private.
func (a *App) OpenNetworkSettings() error {
	return lan.OpenNetworkSettings()
}

// rcloneProgram returns the real path of the rclone.exe that serves the
// drives. The firewall checks the program that runs, so a link (as winget
// installs it) is followed to its target.
func (a *App) rcloneProgram() (string, error) {
	found := a.core.Snapshot().Rclone
	if found == nil {
		return "", errors.New("rclone is not installed yet")
	}
	program, err := filepath.EvalSymlinks(found.Path)
	if err != nil {
		return "", fmt.Errorf("find rclone.exe: %w", err)
	}
	return filepath.Abs(program)
}

// explainDeclined words a "no" to Windows' question for the user.
func explainDeclined(err error) error {
	if errors.Is(err, lan.ErrDeclined) {
		return errors.New("Windows did not get permission, so the firewall was not changed. Try again and answer Yes " +
			"when Windows asks whether the app may make changes")
	}
	return err
}
