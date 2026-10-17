// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/burnersen/CryptBridge/windows/internal/cloud"
	"github.com/burnersen/CryptBridge/windows/internal/pcloud"
	"github.com/burnersen/CryptBridge/windows/internal/rcd"
	"github.com/burnersen/CryptBridge/windows/internal/settings"
	"github.com/burnersen/CryptBridge/windows/internal/vault"
)

var errUnknownAccount = errors.New("this cloud account is not known (was it removed?)")

// accountRuntime is what the engine knows about a cloud account beyond its
// settings. It is guarded by Engine.mu.
type accountRuntime struct {
	usage         *cloud.Usage
	signInProblem string    // why the service refuses the sign-in; "" while it accepts it
	lastAsked     time.Time // when the service was last asked for the free space
}

// AccountSnapshot is what the window shows about a cloud account.
type AccountSnapshot struct {
	ID                 string        `json:"id"`
	Service            string        `json:"service"`      // rclone's backend name
	ServiceTitle       string        `json:"serviceTitle"` // e.g. "Google Drive"
	Name               string        `json:"name"`
	AppRule            cloud.AppRule `json:"appRule"`
	SignedIn           bool          `json:"signedIn"`
	SignInProblem      string        `json:"signInProblem"`
	OwnApp             bool          `json:"ownApp"`
	ClientID           string        `json:"clientId"`           // of the own app; not a secret, the client secret is never sent
	SignedInWithOwnApp bool          `json:"signedInWithOwnApp"` // the current sign-in went through the own app
	Region             string        `json:"region"`             // pCloud: "EU" or "US"
	Usage              *cloud.Usage  `json:"usage"`
	VaultCount         int           `json:"vaultCount"`
}

// accountRuntimeLocked returns the runtime state of an account; the caller
// holds mu.
func (e *Engine) accountRuntimeLocked(id string) *accountRuntime {
	rt, ok := e.accounts[id]
	if !ok {
		rt = &accountRuntime{}
		e.accounts[id] = rt
	}
	return rt
}

// serviceOf returns the service of an account. A service this CryptBridge
// does not know is shown by its rclone name and offers nothing to change.
func serviceOf(a settings.Account) cloud.Service {
	service, err := cloud.ServiceOf(a.Service)
	if err != nil {
		return cloud.Service{Type: a.Service, Title: a.Service, App: cloud.AppNotOffered}
	}
	return service
}

func (e *Engine) accountSnapshotLocked(a settings.Account, s settings.Settings) AccountSnapshot {
	rt := e.accountRuntimeLocked(a.ID)
	service := serviceOf(a)
	snap := AccountSnapshot{
		ID:                 a.ID,
		Service:            a.Service,
		ServiceTitle:       service.Title,
		Name:               a.Name,
		AppRule:            service.App,
		SignedIn:           a.SignedIn(),
		SignInProblem:      rt.signInProblem,
		OwnApp:             a.App.IsSet(),
		ClientID:           a.App.ClientID,
		SignedInWithOwnApp: a.App.IsSet() && a.Remote["client_id"] == a.App.ClientID,
		Usage:              rt.usage,
	}
	switch a.Remote["hostname"] {
	case pcloud.HostEU:
		snap.Region = "EU"
	case pcloud.HostUS:
		snap.Region = "US"
	}
	for _, v := range s.Vaults {
		if v.Kind == settings.KindCloud && v.Account == a.ID {
			snap.VaultCount++
		}
	}
	return snap
}

// Services lists the cloud services an account can be added for.
func (e *Engine) Services() []cloud.Service {
	return cloud.Services()
}

// NewAccount is what the window sends to add a cloud account.
type NewAccount struct {
	Service      string `json:"service"` // rclone's backend name
	Name         string `json:"name"`    // "" gives the service's name
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

// AddAccount signs in to a cloud service in the browser and adds the
// account. onLink receives the sign-in link in case the browser does not
// open; ask gets the questions rclone asks that CryptBridge cannot answer
// itself. It returns the ID of the new account.
func (e *Engine) AddAccount(ctx context.Context, req NewAccount, onLink func(string), ask cloud.AskFunc) (string, error) {
	service, err := cloud.ServiceOf(req.Service)
	if err != nil {
		return "", err
	}
	app, err := cloud.NewApp(service, req.ClientID, req.ClientSecret)
	if err != nil {
		return "", err
	}
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	if e.isLocked() {
		return "", errLocked
	}
	s := e.currentSettings()
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = defaultAccountName(service, s.Accounts)
	}
	if name, err = checkAccountName(name, "", s.Accounts); err != nil {
		return "", err
	}
	id, err := newAccountID(s.Accounts)
	if err != nil {
		return "", err
	}
	client, err := e.runningClientLocked(ctx)
	if err != nil {
		return "", err
	}

	account := settings.Account{ID: id, Service: service.Type, Name: name, App: app}
	if account.Remote, err = e.signInLocked(ctx, client, account, onLink, ask); err != nil {
		return "", err
	}
	s.Accounts = append(s.Accounts, account)
	if err := e.saveSettings(s); err != nil {
		_ = vault.Remove(context.WithoutCancel(ctx), client, remoteName(id))
		return "", err
	}
	e.mu.Lock()
	e.settings = s
	e.mu.Unlock()
	e.publish()
	return id, nil
}

// SignIn signs in to an account again, e.g. after the service ended the
// sign-in or with a new own app. Its drives must be disconnected; the others
// may stay.
func (e *Engine) SignIn(ctx context.Context, id string, onLink func(string), ask cloud.AskFunc) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	client, err := e.accountIdleClientLocked(ctx, id)
	if err != nil {
		return err
	}
	updated := e.currentSettings()
	index := slices.IndexFunc(updated.Accounts, func(a settings.Account) bool { return a.ID == id })
	if index < 0 {
		return errUnknownAccount
	}
	remote, err := e.signInLocked(ctx, client, updated.Accounts[index], onLink, ask)
	if err != nil {
		return err
	}
	updated.Accounts[index].Remote = remote
	if err := e.saveSettings(updated); err != nil {
		return err
	}
	e.mu.Lock()
	e.settings = updated
	rt := e.accountRuntimeLocked(id)
	rt.signInProblem = ""      // the service has just accepted the sign-in
	rt.lastAsked = time.Time{} // ask for the free space right away
	e.mu.Unlock()

	// The vaults sit on top of the account: register them again so that
	// they use the new sign-in.
	for _, v := range updated.Vaults {
		if v.Kind == settings.KindCloud && v.Account == id && v.HasKeys() {
			if err := configureVault(ctx, client, v); err != nil {
				return fmt.Errorf("vault %q: %w", v.Name, err)
			}
			e.refreshFingerprint(ctx, client, updated, v)
		}
	}
	e.settleLocked(ctx, false)
	return nil
}

// signInLocked runs the browser sign-in for an account and registers it
// with rclone. It returns rclone's settings of the account.
func (e *Engine) signInLocked(ctx context.Context, client *rcd.Client, a settings.Account, onLink func(string), ask cloud.AskFunc) (map[string]string, error) {
	service, err := cloud.ServiceOf(a.Service)
	if err != nil {
		return nil, err
	}
	if service.App == cloud.AppRequired && !a.App.IsSet() {
		return nil, fmt.Errorf("%s needs your own app: enter its client ID and client secret first", service.Title)
	}
	e.mu.Lock()
	exe := e.rclone.Path
	e.mu.Unlock()
	token, err := cloud.Authorize(ctx, exe, service, a.App, onLink)
	if err != nil {
		return nil, err
	}
	return cloud.Register(ctx, client, remoteName(a.ID), service, token, a.App, ask)
}

// SignOut forgets the sign-in of an account. The account with its own app
// and the keys of its vaults are kept.
func (e *Engine) SignOut(ctx context.Context, id string) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	client, err := e.accountIdleClientLocked(ctx, id)
	if err != nil {
		return err
	}
	updated := e.currentSettings()
	index := slices.IndexFunc(updated.Accounts, func(a settings.Account) bool { return a.ID == id })
	if index < 0 {
		return errUnknownAccount
	}
	for _, v := range updated.Vaults {
		if v.Kind == settings.KindCloud && v.Account == id {
			_ = vault.Remove(ctx, client, remoteName(v.ID)) // a missing remote needs no removing
		}
	}
	_ = vault.Remove(ctx, client, remoteName(id))

	updated.Accounts[index].Remote = nil
	if err := e.saveSettings(updated); err != nil {
		return err
	}
	e.mu.Lock()
	e.settings = updated
	delete(e.accounts, id)
	e.mu.Unlock()
	e.settleLocked(ctx, false)
	return nil
}

// SetAccountApp stores the codes of the user's own app for an account;
// empty codes mean rclone's app again where the service allows it. It takes
// effect with the next sign-in; the current sign-in keeps working until
// then, so the drives may stay connected.
func (e *Engine) SetAccountApp(id, clientID, clientSecret string) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	if e.isLocked() {
		return errLocked
	}
	updated := e.currentSettings()
	index := slices.IndexFunc(updated.Accounts, func(a settings.Account) bool { return a.ID == id })
	if index < 0 {
		return errUnknownAccount
	}
	service, err := cloud.ServiceOf(updated.Accounts[index].Service)
	if err != nil {
		return err
	}
	app, err := cloud.NewApp(service, clientID, clientSecret)
	if err != nil {
		return err
	}
	updated.Accounts[index].App = app
	if err := e.saveSettings(updated); err != nil {
		return err
	}
	e.mu.Lock()
	e.settings = updated
	e.mu.Unlock()
	e.publish()
	return nil
}

// RenameAccount changes the name an account is shown with.
func (e *Engine) RenameAccount(id, name string) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	if e.isLocked() {
		return errLocked
	}
	updated := e.currentSettings()
	index := slices.IndexFunc(updated.Accounts, func(a settings.Account) bool { return a.ID == id })
	if index < 0 {
		return errUnknownAccount
	}
	name, err := checkAccountName(name, id, updated.Accounts)
	if err != nil {
		return err
	}
	updated.Accounts[index].Name = name
	if err := e.saveSettings(updated); err != nil {
		return err
	}
	e.mu.Lock()
	e.settings = updated
	e.mu.Unlock()
	e.publish()
	return nil
}

// RemoveAccount makes CryptBridge forget an account: its sign-in and its own
// app. The files in the cloud are never touched. An account that still holds
// vaults cannot be removed: they would lose their place.
func (e *Engine) RemoveAccount(ctx context.Context, id string) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	if e.isLocked() {
		return errLocked
	}
	updated := e.currentSettings()
	a, ok := updated.Account(id)
	if !ok {
		return errUnknownAccount
	}
	for _, v := range updated.Vaults {
		if v.Kind == settings.KindCloud && v.Account == id {
			return fmt.Errorf("the vault %q is in %s. Remove its vaults from CryptBridge first", v.Name, a.Name)
		}
	}
	e.mu.Lock()
	client := e.client
	e.mu.Unlock()
	if client != nil {
		_ = vault.Remove(ctx, client, remoteName(id)) // a missing remote needs no removing
	}
	updated.Accounts = slices.DeleteFunc(updated.Accounts, func(other settings.Account) bool { return other.ID == id })
	if err := e.saveSettings(updated); err != nil {
		return err
	}
	e.mu.Lock()
	e.settings = updated
	delete(e.accounts, id)
	e.mu.Unlock()
	e.publish()
	return nil
}

// AccountSecrets returns a cloud account with its secrets (for the transfer
// to the Linux server). It needs the unlocked app.
func (e *Engine) AccountSecrets(id string) (settings.Account, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.locked {
		return settings.Account{}, errLocked
	}
	a, ok := e.settings.Account(id)
	if !ok {
		return settings.Account{}, errUnknownAccount
	}
	a.Remote = maps.Clone(a.Remote) // the caller must not change the settings
	return a, nil
}

// PCloudSignIn returns the sign-in of a pCloud account (for its region on
// the emergency sheet).
func (e *Engine) PCloudSignIn(id string) (pcloud.Account, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.locked {
		return pcloud.Account{}, errLocked
	}
	a, ok := e.settings.Account(id)
	if !ok {
		return pcloud.Account{}, errUnknownAccount
	}
	if a.Service != cloud.PCloud {
		return pcloud.Account{}, fmt.Errorf("%s is not a pCloud account", a.Name)
	}
	if !a.SignedIn() {
		return pcloud.Account{}, fmt.Errorf("sign in to %s first", a.Name)
	}
	return pcloud.Account{Hostname: a.Remote["hostname"], Token: a.Remote["token"]}, nil
}

// runningClientLocked returns a running rclone that knows the signed-in
// accounts and the vaults.
func (e *Engine) runningClientLocked(ctx context.Context) (*rcd.Client, error) {
	if err := e.prepareLocked(ctx); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.client, nil
}

// accountIdleClientLocked returns a running rclone while no drive of the
// account is connected: the sign-in under them must not change while they
// are in use.
func (e *Engine) accountIdleClientLocked(ctx context.Context, id string) (*rcd.Client, error) {
	if e.isLocked() {
		return nil, errLocked
	}
	s := e.currentSettings()
	a, ok := s.Account(id)
	if !ok {
		return nil, errUnknownAccount
	}
	for _, v := range s.Vaults {
		if v.Kind != settings.KindCloud || v.Account != id {
			continue
		}
		e.mu.Lock()
		rt := e.runtimeLocked(v.ID)
		busy := rt.want || rt.status.Mounted
		e.mu.Unlock()
		if busy {
			return nil, fmt.Errorf("disconnect the drive %s of %s first", describe(v), a.Name)
		}
	}
	if err := e.ensureRcloneFoundLocked(ctx); err != nil {
		return nil, err
	}
	return e.ensureDaemonLocked(ctx)
}

// signedInClientLocked returns a running rclone that knows the account,
// which must be signed in.
func (e *Engine) signedInClientLocked(ctx context.Context, id string) (*rcd.Client, error) {
	if e.isLocked() {
		return nil, errLocked
	}
	a, ok := e.currentSettings().Account(id)
	if !ok {
		return nil, errUnknownAccount
	}
	if !a.SignedIn() {
		return nil, fmt.Errorf("sign in to %s first", a.Name)
	}
	return e.runningClientLocked(ctx)
}

// refreshUsage asks every signed-in service for the free space now and then
// — every minute while it refuses the sign-in, so that the warning goes
// away soon after it accepts it again. The warning is kept apart from the
// drives: a connected drive stays "connected" and the warning stays visible.
func (e *Engine) refreshUsage(ctx context.Context, client *rcd.Client, s settings.Settings) {
	for _, a := range s.Accounts {
		if !a.SignedIn() {
			continue
		}
		e.mu.Lock()
		rt := e.accountRuntimeLocked(a.ID)
		every := usageEvery
		if rt.signInProblem != "" {
			every = signInRecheckEvery
		}
		due := time.Since(rt.lastAsked) >= every
		e.mu.Unlock()
		if due {
			_ = e.readUsageLocked(ctx, client, a) // a refused sign-in is shown by readUsageLocked
		}
	}
}

// readUsageLocked asks the service of an account for the free space. The
// answer also tells whether the service accepts the sign-in, and the
// warning is set or removed accordingly. Asking may have made rclone renew
// the sign-in, which is kept then. The caller holds actionMu.
func (e *Engine) readUsageLocked(ctx context.Context, client *rcd.Client, a settings.Account) error {
	e.mu.Lock()
	e.accountRuntimeLocked(a.ID).lastAsked = time.Now()
	e.mu.Unlock()
	usage, err := cloud.ReadUsage(ctx, client, serviceOf(a), remoteName(a.ID)+":")
	if err != nil {
		return e.explainFor(a, err)
	}
	e.mu.Lock()
	rt := e.accountRuntimeLocked(a.ID)
	rt.usage = &usage
	rt.signInProblem = ""
	e.mu.Unlock()
	// A failed save is tried again with the next look; the stored sign-in
	// keeps working meanwhile (Microsoft accepts it for 90 days).
	_ = e.keepRenewedSignInLocked(ctx, client, a.ID)
	e.publish()
	return nil
}

// keepRenewedSignInLocked stores the sign-in of an account if rclone has
// renewed it. rclone renews tokens in its memory only, and Microsoft
// replaces the whole token on every renewal: without this, the account
// would need a new sign-in 90 days after the last one.
func (e *Engine) keepRenewedSignInLocked(ctx context.Context, client *rcd.Client, id string) error {
	s := e.currentSettings()
	index := slices.IndexFunc(s.Accounts, func(a settings.Account) bool { return a.ID == id })
	if index < 0 || !s.Accounts[index].SignedIn() {
		return nil
	}
	current, err := cloud.ReadParams(ctx, client, remoteName(id))
	if err != nil || maps.Equal(current, s.Accounts[index].Remote) {
		return err
	}
	s.Accounts[index].Remote = current
	if err := e.saveSettings(s); err != nil {
		return err
	}
	e.mu.Lock()
	e.settings = s
	e.mu.Unlock()
	return nil
}

// keepRenewedSignInsLocked stores every renewed sign-in; used before rclone
// stops and forgets them. A failure is passed on as the first error.
func (e *Engine) keepRenewedSignInsLocked(ctx context.Context, client *rcd.Client) error {
	var first error
	for _, a := range e.currentSettings().Accounts {
		if err := e.keepRenewedSignInLocked(ctx, client, a.ID); err != nil && first == nil {
			first = fmt.Errorf("%s: %w", a.Name, err)
		}
	}
	return first
}

// explainFor turns an error of an account's service into plain words and
// remembers a refused sign-in, so that the window keeps showing it.
func (e *Engine) explainFor(a settings.Account, err error) error {
	service := serviceOf(a)
	err = cloud.Classify(service, err)
	if !cloud.IsSignInProblem(err) {
		return err
	}
	advice := cloud.Advice(service, err)
	e.mu.Lock()
	rt := e.accountRuntimeLocked(a.ID)
	changed := rt.signInProblem != advice
	rt.signInProblem = advice
	e.mu.Unlock()
	if changed {
		e.publish()
	}
	return err
}

// explain does explainFor for the account of a cloud vault; other errors
// are returned unchanged.
func (e *Engine) explain(v settings.Vault, err error) error {
	if err == nil || v.Kind != settings.KindCloud {
		return err
	}
	a, ok := e.currentSettings().Account(v.Account)
	if !ok {
		return err
	}
	return e.explainFor(a, err)
}

// checkSignInLocked asks the service whether it accepts the sign-in before
// a drive is mounted; otherwise the drive would appear but stay empty
// without a word of explanation. Other problems (offline, service busy, a
// slow answer) are left to the check of the vault folder that follows,
// which needs the service anyway. An answer from the last minute is good
// enough, so that several drives of an account do not ask one after another.
func (e *Engine) checkSignInLocked(ctx context.Context, client *rcd.Client, a settings.Account) error {
	e.mu.Lock()
	rt := e.accountRuntimeLocked(a.ID)
	recent := rt.signInProblem == "" && time.Since(rt.lastAsked) < signInRecheckEvery
	e.mu.Unlock()
	if recent {
		return nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, signInCheckTimeout)
	defer cancel()
	if err := e.readUsageLocked(checkCtx, client, a); cloud.IsSignInProblem(err) {
		return err
	}
	return nil
}

// maxAccountNameLength keeps account names short enough for the window.
const maxAccountNameLength = 40

// checkAccountName cleans an account name and makes sure it is not used by
// another account.
func checkAccountName(name, id string, accounts []settings.Account) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", errors.New("give the account a name")
	case utf8.RuneCountInString(name) > maxAccountNameLength:
		return "", fmt.Errorf("the name may have at most %d characters", maxAccountNameLength)
	case strings.ContainsFunc(name, func(r rune) bool { return r < ' ' }):
		return "", errors.New("the name cannot contain control characters")
	}
	for _, other := range accounts {
		if other.ID != id && strings.EqualFold(other.Name, name) {
			return "", fmt.Errorf("another account is already called %q", other.Name)
		}
	}
	return name, nil
}

// defaultAccountName is the service's name, numbered from the second
// account of a service on: "Google Drive", "Google Drive 2", …
func defaultAccountName(service cloud.Service, accounts []settings.Account) string {
	taken := func(name string) bool {
		return slices.ContainsFunc(accounts, func(a settings.Account) bool { return strings.EqualFold(a.Name, name) })
	}
	name := service.Title
	for n := 2; taken(name); n++ {
		name = fmt.Sprintf("%s %d", service.Title, n)
	}
	return name
}

// newAccountID returns a random ID that no account has yet. Unlike a
// vault's ID it needs no meaning: nothing on disk is named after it.
func newAccountID(accounts []settings.Account) (string, error) {
	for {
		random := make([]byte, 4)
		if _, err := rand.Read(random); err != nil {
			return "", fmt.Errorf("create account ID: %w", err)
		}
		id := "a" + hex.EncodeToString(random)
		if !slices.ContainsFunc(accounts, func(a settings.Account) bool { return a.ID == id }) {
			return id, nil
		}
	}
}
