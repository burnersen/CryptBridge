// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package engine

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/lan"
	"github.com/burnersen/CryptBridge/windows/internal/rcd"
	"github.com/burnersen/CryptBridge/windows/internal/settings"
)

// The LAN access: a WebDAV server in rclone shows the connected drives to the
// other devices of the home network (see package lan). It follows the
// drives: a drive that connects appears in the network, and a drive leaves
// the network before it disconnects — once its letter is free, another disk
// could get the letter and would be shown in its place. Locking the app ends
// the server at once, even while drives still finish their uploads.

// lanRetryAfter: a server that failed to start is tried again after this
// pause, or at once when the settings or the connected drives change.
const lanRetryAfter = 30 * time.Second

// LANStatus is what the window shows about the LAN access. The password is
// not part of it; the Settings page asks for it separately.
type LANStatus struct {
	Enabled      bool     `json:"enabled"`
	Port         int      `json:"port"`
	User         string   `json:"user"`
	AllowChanges bool     `json:"allowChanges"`
	Running      bool     `json:"running"`
	Folders      []string `json:"folders"`   // what the devices see, one folder per connected drive
	Addresses    []string `json:"addresses"` // e.g. http://192.168.1.20:8080/
	Problem      string   `json:"problem"`   // why the server does not run although it should
}

// lanRuntime is what the engine knows about the server, guarded by mu.
type lanRuntime struct {
	server    *lan.Server // nil: no server runs
	pid       int         // the rclone process looked at last; 0: look again
	problem   string
	failed    string    // key of the plan that failed to start
	failure   string    // why it failed
	retryAt   time.Time // when the failed plan may be tried again
	addresses []string
}

// lanStatusLocked describes the LAN access; the caller holds mu.
func (e *Engine) lanStatusLocked() LANStatus {
	cfg := e.settings.LAN
	status := LANStatus{
		Enabled:      cfg.Enabled,
		Port:         cfg.Port,
		User:         cfg.User,
		AllowChanges: cfg.AllowChanges,
		Folders:      []string{},
		Addresses:    []string{},
		Problem:      e.lan.problem,
	}
	if e.lan.server != nil {
		status.Running = true
		status.Folders = slices.Clone(e.lan.server.Folders)
	}
	if cfg.Enabled && e.lan.addresses != nil {
		status.Addresses = slices.Clone(e.lan.addresses)
	}
	return status
}

// lanPlanLocked returns the server that should run now, sharing every
// connected drive except the one with the ID except. want is false when no
// server should run; problem says why, if the user can do something about
// it. The caller holds mu.
func (e *Engine) lanPlanLocked(except string) (plan lan.Plan, want bool, problem string) {
	cfg := e.settings.LAN
	switch {
	case !cfg.Enabled || e.locked:
		return lan.Plan{}, false, ""
	case cfg.Password == "":
		return lan.Plan{}, false, "Set a password for the LAN access (Settings → LAN access)."
	}
	for _, v := range e.settings.Vaults {
		if v.ID == except || !v.HasKeys() || !e.runtimeLocked(v.ID).status.Mounted {
			continue
		}
		plan.Shares = append(plan.Shares, lan.Share{Name: v.Name, Root: v.Letter + ":/"})
	}
	plan.Config = lan.Config{Port: cfg.Port, User: cfg.User, Password: cfg.Password, AllowChanges: cfg.AllowChanges}
	return plan, len(plan.Shares) > 0, ""
}

// syncLANLocked starts, changes or stops the server so that it shows the
// connected drives — all but the one with the ID except, which is about to
// disconnect. It returns false if the server could not be brought into that
// state. The caller holds actionMu.
func (e *Engine) syncLANLocked(ctx context.Context, except string) bool {
	e.mu.Lock()
	client := e.client
	plan, want, problem := e.lanPlanLocked(except)
	cfg := e.settings.LAN
	state := e.lan
	before := e.lanStatusLocked()
	e.mu.Unlock()

	state.addresses = nil
	if cfg.Enabled {
		state.addresses = lan.Addresses(cfg.Port)
	}
	state.problem = problem
	ok := true
	if client == nil {
		state.server = nil // rclone is gone, and its server with it
		state.pid = 0
	} else {
		ok = reconcileLAN(ctx, client, &state, plan, want)
	}

	e.mu.Lock()
	e.lan = state
	changed := !reflect.DeepEqual(before, e.lanStatusLocked())
	e.mu.Unlock()
	if changed {
		e.publish()
	}
	return ok
}

// reconcileLAN brings the server in rclone in line with the plan and
// records the outcome in state. It returns false on failure.
func reconcileLAN(ctx context.Context, client *rcd.Client, state *lanRuntime, plan lan.Plan, want bool) bool {
	if state.pid != client.PID() && !adoptLAN(ctx, client, state, plan, want) {
		return false
	}
	key := ""
	if want {
		key = plan.Key()
	}
	if state.server != nil && state.server.Key == key {
		return true
	}
	if state.server != nil {
		if err := lan.Stop(ctx, client, state.server.ID); err != nil {
			state.problem = err.Error()
			state.pid = 0 // look at what runs again next time
			return false
		}
		state.server = nil
	}
	if !want {
		return true
	}
	if key == state.failed && time.Now().Before(state.retryAt) {
		state.problem = state.failure
		return false
	}
	server, err := lan.Start(ctx, client, plan)
	if err != nil {
		state.failed, state.failure, state.retryAt = key, err.Error(), time.Now().Add(lanRetryAfter)
		state.problem = state.failure
		return false
	}
	state.failed = ""
	state.server = &server
	return true
}

// adoptLAN looks at the servers of a newly found rclone — one that kept
// running while the window was gone may still run the wanted server, which
// is then taken over without interrupting the devices. Every other server
// is stopped. It returns false if rclone could not be asked.
func adoptLAN(ctx context.Context, client *rcd.Client, state *lanRuntime, plan lan.Plan, want bool) bool {
	running, err := lan.Running(ctx, client)
	if err != nil {
		state.problem = err.Error()
		return false
	}
	state.server = nil
	state.failed = ""
	for _, found := range running {
		if want && state.server == nil && found.Key == plan.Key() {
			adopted := plan.Server(found.ID)
			state.server = &adopted
			continue
		}
		if err := lan.Stop(ctx, client, found.ID); err != nil {
			state.problem = err.Error()
			return false
		}
	}
	state.pid = client.PID()
	return true
}

// withdrawFromLANLocked takes a drive out of the server before the drive is
// disconnected. If the server cannot be changed, it is stopped: a server
// that might show another disk under the freed letter must not run. The
// caller holds actionMu.
func (e *Engine) withdrawFromLANLocked(ctx context.Context, v settings.Vault) {
	e.mu.Lock()
	server := e.lan.server
	e.mu.Unlock()
	if server == nil || !slices.Contains(server.Roots, v.Letter+":/") {
		return
	}
	if !e.syncLANLocked(ctx, v.ID) {
		e.stopLANLocked(ctx)
	}
}

// stopLANLocked ends the server at once: when locking, when quitting, and
// when it cannot be changed. The caller holds actionMu.
func (e *Engine) stopLANLocked(ctx context.Context) {
	e.mu.Lock()
	client := e.client
	e.mu.Unlock()
	problem := ""
	if client != nil {
		if err := lan.StopAll(ctx, client); err != nil {
			problem = err.Error()
		}
	}
	e.mu.Lock()
	e.lan.server = nil
	e.lan.pid = 0 // look at what runs before the next start
	e.lan.problem = problem
	e.mu.Unlock()
	e.publish()
}

// LANSettings returns the LAN settings with the password, for the Settings
// page.
func (e *Engine) LANSettings() (settings.LAN, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.locked {
		return settings.LAN{}, errLocked
	}
	return e.settings.LAN, nil
}

// UpdateLAN checks and stores the LAN settings and starts, changes or stops
// the server accordingly. A server that cannot start shows its problem in
// the snapshot; that is no error of saving.
func (e *Engine) UpdateLAN(ctx context.Context, change settings.LAN) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	if e.isLocked() {
		return errLocked
	}
	change.User = strings.TrimSpace(change.User)
	if err := lan.CheckPort(change.Port); err != nil {
		return err
	}
	if err := lan.CheckUser(change.User); err != nil {
		return err
	}
	if change.Enabled || change.Password != "" {
		if err := lan.CheckPassword(change.Password); err != nil {
			return err
		}
	}
	updated := e.currentSettings()
	updated.LAN = change
	if err := e.saveSettings(updated); err != nil {
		return err
	}
	e.mu.Lock()
	e.settings = updated
	e.lan.failed = "" // new settings deserve a new try at once
	e.mu.Unlock()
	e.syncLANLocked(ctx, "")
	e.publish()
	return nil
}
