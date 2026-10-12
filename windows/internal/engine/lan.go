// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/lan"
	"github.com/burnersen/CryptBridge/windows/internal/rcd"
	"github.com/burnersen/CryptBridge/windows/internal/settings"
)

// The LAN access: WebDAV servers in rclone show the connected drives to the
// other devices of the home network (see package lan). There are two
// accesses, each a server on a port of its own: the streaming access only
// reads, the full access may also change and delete. Both follow the drives:
// a drive that connects appears in the network, and a drive leaves the
// network before it disconnects — once its letter is free, another disk
// could get the letter and would be shown in its place. Locking the app ends
// the servers at once, even while drives still finish their uploads.

// lanRetryAfter: a server that failed to start is tried again after this
// pause, or at once when the settings or the connected drives change.
const lanRetryAfter = 30 * time.Second

// lanRole names one of the two LAN accesses.
type lanRole int

const (
	roleStreaming lanRole = iota // read only: for video players and TVs
	roleFull                     // read, write and delete: for file apps
	roleCount
)

// name is how messages call the access.
func (r lanRole) name() string {
	if r == roleFull {
		return "full access"
	}
	return "streaming access"
}

// in returns where the settings of this access are kept.
func (r lanRole) in(cfg *settings.LAN) *settings.LANAccess {
	if r == roleFull {
		return &cfg.Full
	}
	return &cfg.Streaming
}

// LANAccessStatus is what the window shows about one LAN access. The
// password is not part of it; the Settings page asks for it separately.
type LANAccessStatus struct {
	Enabled   bool     `json:"enabled"`
	Port      int      `json:"port"`
	User      string   `json:"user"`
	Running   bool     `json:"running"`
	Folders   []string `json:"folders"`   // what the devices see, one folder per connected drive
	Addresses []string `json:"addresses"` // e.g. http://192.168.1.20:8080/
	Problem   string   `json:"problem"`   // why the server does not run although it should
}

// LANStatus is what the window shows about the two LAN accesses.
type LANStatus struct {
	Streaming LANAccessStatus `json:"streaming"`
	Full      LANAccessStatus `json:"full"`
}

// Ports returns the ports of the accesses that are on: the ones the
// firewall has to let in.
func (s LANStatus) Ports() []int {
	var ports []int
	for _, access := range []LANAccessStatus{s.Streaming, s.Full} {
		if access.Enabled {
			ports = append(ports, access.Port)
		}
	}
	return ports
}

// lanRuntime is what the engine knows about the servers, guarded by mu.
type lanRuntime struct {
	pid      int // the rclone process looked at last; 0: look again
	accesses [roleCount]accessRuntime
}

// accessRuntime is what the engine knows about the server of one access.
type accessRuntime struct {
	server    *lan.Server // nil: no server runs
	problem   string
	failed    string    // key of the plan that failed to start
	failure   string    // why it failed
	retryAt   time.Time // when the failed plan may be tried again
	addresses []string
}

// lanStatusLocked describes the LAN accesses; the caller holds mu.
func (e *Engine) lanStatusLocked() LANStatus {
	return LANStatus{
		Streaming: e.accessStatusLocked(roleStreaming),
		Full:      e.accessStatusLocked(roleFull),
	}
}

// accessStatusLocked describes one LAN access; the caller holds mu.
func (e *Engine) accessStatusLocked(role lanRole) LANAccessStatus {
	cfg := *role.in(&e.settings.LAN)
	state := e.lan.accesses[role]
	status := LANAccessStatus{
		Enabled:   cfg.Enabled,
		Port:      cfg.Port,
		User:      cfg.User,
		Folders:   []string{},
		Addresses: []string{},
		Problem:   state.problem,
	}
	if state.server != nil {
		status.Running = true
		status.Folders = slices.Clone(state.server.Folders)
	}
	if cfg.Enabled && state.addresses != nil {
		status.Addresses = slices.Clone(state.addresses)
	}
	return status
}

// lanWish is the server one access should have now.
type lanWish struct {
	plan    lan.Plan
	want    bool   // false: no server should run
	problem string // why not, if the user can do something about it
}

// key identifies the wanted server; "" if none is wanted.
func (w lanWish) key() string {
	if !w.want {
		return ""
	}
	return w.plan.Key()
}

// lanWishesLocked returns the servers that should run now, sharing every
// connected drive except the one with the ID except. The caller holds mu.
func (e *Engine) lanWishesLocked(except string) [roleCount]lanWish {
	var shares []lan.Share
	for _, v := range e.settings.Vaults {
		if v.ID == except || !v.HasKeys() || !e.runtimeLocked(v.ID).status.Mounted {
			continue
		}
		shares = append(shares, lan.Share{Name: v.Name, Root: v.Letter + ":/"})
	}
	var wishes [roleCount]lanWish
	for role := range roleCount {
		cfg := *role.in(&e.settings.LAN)
		switch {
		case !cfg.Enabled || e.locked:
			continue
		case cfg.Password == "":
			wishes[role].problem = "Set a password for the " + role.name() + " (Settings, LAN access)."
			continue
		}
		wishes[role] = lanWish{
			plan: lan.Plan{
				Config: lan.Config{Port: cfg.Port, User: cfg.User, Password: cfg.Password, AllowChanges: role == roleFull},
				Shares: shares,
			},
			want: len(shares) > 0,
		}
	}
	return wishes
}

// syncLANLocked starts, changes or stops the servers so that they show the
// connected drives — all but the one with the ID except, which is about to
// disconnect. It returns false if a server could not be brought into that
// state. The caller holds actionMu.
func (e *Engine) syncLANLocked(ctx context.Context, except string) bool {
	e.mu.Lock()
	client := e.client
	wishes := e.lanWishesLocked(except)
	cfg := e.settings.LAN
	state := e.lan
	before := e.lanStatusLocked()
	e.mu.Unlock()

	for role := range roleCount {
		access := &state.accesses[role]
		access.addresses = nil
		if wanted := role.in(&cfg); wanted.Enabled {
			access.addresses = lan.Addresses(wanted.Port)
		}
		access.problem = wishes[role].problem
	}
	ok := true
	if client == nil {
		for role := range roleCount {
			state.accesses[role].server = nil // rclone is gone, and its servers with it
		}
		state.pid = 0
	} else {
		ok = reconcileLAN(ctx, client, &state, wishes)
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

// reconcileLAN brings the servers in rclone in line with the wishes and
// records the outcome in state. It returns false on any failure. Servers
// that are no longer wanted stop before new ones start, so that the two
// accesses can swap their ports.
func reconcileLAN(ctx context.Context, client *rcd.Client, state *lanRuntime, wishes [roleCount]lanWish) bool {
	if state.pid != client.PID() && !adoptLAN(ctx, client, state, wishes) {
		return false
	}
	ok := true
	for role := range roleCount {
		access := &state.accesses[role]
		if access.server == nil || access.server.Key == wishes[role].key() {
			continue
		}
		if err := lan.Stop(ctx, client, access.server.ID); err != nil {
			access.problem = err.Error()
			state.pid = 0 // look at what runs again next time
			ok = false
			continue
		}
		access.server = nil
	}
	for role := range roleCount {
		if !startLAN(ctx, client, &state.accesses[role], wishes[role]) {
			ok = false
		}
	}
	return ok
}

// startLAN starts the wanted server of one access unless it runs already.
// It returns false on failure, also for a server that could not be stopped.
func startLAN(ctx context.Context, client *rcd.Client, access *accessRuntime, wish lanWish) bool {
	if access.server != nil {
		return access.server.Key == wish.key()
	}
	if !wish.want {
		return true
	}
	key := wish.key()
	if key == access.failed && time.Now().Before(access.retryAt) {
		access.problem = access.failure
		return false
	}
	server, err := lan.Start(ctx, client, wish.plan)
	if err != nil {
		access.failed, access.failure, access.retryAt = key, err.Error(), time.Now().Add(lanRetryAfter)
		access.problem = access.failure
		return false
	}
	access.failed = ""
	access.server = &server
	return true
}

// adoptLAN looks at the servers of a newly found rclone — one that kept
// running while the window was gone may still run the wanted servers, which
// are then taken over without interrupting the devices. Every other server
// is stopped. It returns false if rclone could not be asked.
func adoptLAN(ctx context.Context, client *rcd.Client, state *lanRuntime, wishes [roleCount]lanWish) bool {
	running, err := lan.Running(ctx, client)
	if err != nil {
		state.setProblem(err.Error())
		return false
	}
	for role := range roleCount {
		state.accesses[role].server = nil
		state.accesses[role].failed = ""
	}
	for _, found := range running {
		if role, wanted := state.waitingFor(wishes, found.Key); wanted {
			adopted := wishes[role].plan.Server(found.ID)
			state.accesses[role].server = &adopted
			continue
		}
		if err := lan.Stop(ctx, client, found.ID); err != nil {
			state.setProblem(err.Error())
			return false
		}
	}
	state.pid = client.PID()
	return true
}

// waitingFor returns the access that wants the server with this key and has
// none yet.
func (state *lanRuntime) waitingFor(wishes [roleCount]lanWish, key string) (lanRole, bool) {
	for role := range roleCount {
		if wishes[role].want && state.accesses[role].server == nil && wishes[role].key() == key {
			return role, true
		}
	}
	return 0, false
}

// setProblem records a problem that concerns both accesses.
func (state *lanRuntime) setProblem(problem string) {
	for role := range roleCount {
		state.accesses[role].problem = problem
	}
}

// withdrawFromLANLocked takes a drive out of the servers before the drive is
// disconnected. If a server cannot be changed, all are stopped: a server
// that might show another disk under the freed letter must not run. The
// caller holds actionMu.
func (e *Engine) withdrawFromLANLocked(ctx context.Context, v settings.Vault) {
	e.mu.Lock()
	shown := false
	for _, access := range e.lan.accesses {
		if access.server != nil && slices.Contains(access.server.Roots, v.Letter+":/") {
			shown = true
		}
	}
	e.mu.Unlock()
	if !shown {
		return
	}
	if !e.syncLANLocked(ctx, v.ID) {
		e.stopLANLocked(ctx)
	}
}

// stopLANLocked ends the servers at once: when locking, when quitting, and
// when they cannot be changed. The caller holds actionMu.
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
	for role := range roleCount {
		e.lan.accesses[role].server = nil
	}
	e.lan.pid = 0 // look at what runs before the next start
	e.lan.setProblem(problem)
	e.mu.Unlock()
	e.publish()
}

// LANSettings returns the LAN settings with the passwords, for the Settings
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
// the servers accordingly. A server that cannot start shows its problem in
// the snapshot; that is no error of saving.
func (e *Engine) UpdateLAN(ctx context.Context, change settings.LAN) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	if e.isLocked() {
		return errLocked
	}
	if err := checkLANSettings(&change); err != nil {
		return err
	}
	updated := e.currentSettings()
	updated.LAN = change
	if err := e.saveSettings(updated); err != nil {
		return err
	}
	e.mu.Lock()
	e.settings = updated
	for role := range roleCount {
		e.lan.accesses[role].failed = "" // new settings deserve a new try at once
	}
	e.mu.Unlock()
	e.syncLANLocked(ctx, "")
	e.publish()
	return nil
}

// checkLANSettings checks both accesses before they are stored, trimming
// the user names. The accesses need ports of their own, and a password of
// their own: the streaming password is typed on TVs, where others may see
// it, and must not open the full access.
func checkLANSettings(change *settings.LAN) error {
	for role := range roleCount {
		access := role.in(change)
		access.User = strings.TrimSpace(access.User)
		if err := lan.CheckPort(access.Port); err != nil {
			return fmt.Errorf("%s: %w", role.name(), err)
		}
		if err := lan.CheckUser(access.User); err != nil {
			return fmt.Errorf("%s: %w", role.name(), err)
		}
		if access.Enabled || access.Password != "" {
			if err := lan.CheckPassword(access.Password); err != nil {
				return fmt.Errorf("%s: %w", role.name(), err)
			}
		}
	}
	if change.Streaming.Port == change.Full.Port {
		return errors.New("the streaming access and the full access need different ports")
	}
	if change.Streaming.Password != "" && change.Streaming.Password == change.Full.Password {
		return errors.New("give the full access a password of its own: the streaming password is typed on TVs, " +
			"and whoever sees it could otherwise change and delete files, too")
	}
	return nil
}
